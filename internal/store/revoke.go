package store

import (
	"context"
	"database/sql"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// GrantRevoke is the local authoritative linearization point. expectedEpoch makes
// identical retries idempotent without incrementing epochs on every lost response.
// Already admitted coauthor effects cannot be rolled back by this transaction.
func (s *Store) GrantRevoke(ctx context.Context, id string, expectedEpoch uint64, now int64, builders ...ReceiptBuilder) error {
	if len(builders) > 1 {
		return failure(fault.InvalidEncoding)
	}
	var builder ReceiptBuilder
	if len(builders) == 1 {
		builder = builders[0]
	}
	return s.grantRevoke(ctx, id, expectedEpoch, now, builder, true)
}

// GrantRevokeLocal commits owner revocation when a return signer is unavailable.
// It records local terminal evidence without claiming a peer notification exists.
func (s *Store) GrantRevokeLocal(ctx context.Context, id string, expectedEpoch uint64, now int64) error {
	return s.grantRevoke(ctx, id, expectedEpoch, now, nil, false)
}

func (s *Store) grantRevoke(ctx context.Context, id string, expectedEpoch uint64, now int64, builder ReceiptBuilder, notifications bool) error {
	if !protocol.ValidToken(id) || expectedEpoch == 0 || expectedEpoch >= protocol.MaxSafeInteger || now <= 0 || now > protocol.MaxSafeInteger {
		return failure(fault.InvalidEncoding)
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		var epoch uint64
		var revoked bool
		if err := tx.QueryRowContext(ctx, `SELECT epoch,revoked FROM grants WHERE id=?`, id).Scan(&epoch, &revoked); err != nil {
			return lookupError(err)
		}
		if revoked {
			if epoch == expectedEpoch || epoch == expectedEpoch+1 {
				return nil
			}
			return failure(fault.EpochMismatch)
		}
		if epoch != expectedEpoch {
			return failure(fault.EpochMismatch)
		}
		messages, err := revokeMessages(ctx, tx, id)
		if err != nil {
			return err
		}
		if notifications && len(messages) != 0 && builder == nil {
			return failure(ReceiptUnavailable)
		}
		if _, err = tx.ExecContext(ctx, `UPDATE grants SET epoch=epoch+1,revoked=1,lease_generation=lease_generation+1 WHERE id=?`, id); err != nil {
			return failure(StorageUnavailable)
		}
		if err = s.step("revoke_epoch"); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE inbox SET state='quarantined' WHERE grant_id=? AND state IN ('pending','offered','delivered','parked','intent','authorized','in_doubt','adapter_accepted','responder_started','dequeued')`, id); err != nil {
			return failure(StorageUnavailable)
		}
		if err = s.step("revoke_quarantine"); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE delivery_attempts SET state=CASE WHEN state='intent' AND EXISTS(SELECT 1 FROM audit WHERE action='delivery_intent' AND message_id=delivery_attempts.message_id AND reason=delivery_attempts.id) THEN 'not_sent' ELSE 'in_doubt' END WHERE message_id IN (SELECT id FROM inbox WHERE grant_id=?) AND state IN ('intent','authorized','adapter_accepted','responder_started')`, id); err != nil {
			return failure(StorageUnavailable)
		}
		if _, err = tx.ExecContext(ctx, `UPDATE responder_runs SET state='cancel_requested' WHERE message_id IN (SELECT id FROM inbox WHERE grant_id=?) AND state NOT IN ('reply_committed','failed','cancelled')`, id); err != nil {
			return failure(StorageUnavailable)
		}
		for _, message := range messages {
			r := protocol.ReceiptPayload{MessageID: message.id, AttemptID: message.attempt, Stage: message.stage, EvidenceKind: "local_revoke", At: now, ReasonCode: "revoked", Outcome: message.outcome}
			if notifications {
				err = s.revokeReceiptOutbox(ctx, tx, id, builder, r)
			} else {
				err = s.recordReceipt(ctx, tx, InboundReceipt, id, r)
			}
			if err != nil {
				return err
			}
			if err = s.audit(ctx, tx, now, "message_revoke", id, epoch+1, message.id, message.digest, message.stage); err != nil {
				return err
			}
		}
		if err = s.revokeOutbox(ctx, tx, id, epoch+1, now); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO revocations VALUES(?,?,?)`, id, epoch+1, now); err != nil {
			return failure(StorageUnavailable)
		}
		if err = s.step("revoke_leases"); err != nil {
			return err
		}
		reason := "revoked"
		if !notifications {
			reason = "notification_unavailable"
		}
		return s.audit(ctx, tx, now, "grant_revoke", id, epoch+1, "", "", reason)
	})
}

type revokedMessage struct{ id, digest, attempt, stage, outcome string }

func revokeMessages(ctx context.Context, tx *sql.Tx, grantID string) ([]revokedMessage, error) {
	rows, err := tx.QueryContext(ctx, `SELECT i.id,i.digest,COALESCE(a.id,i.id),COALESCE(a.state,''),i.state,i.route,EXISTS(SELECT 1 FROM audit WHERE action='delivery_intent' AND message_id=i.id AND reason=a.id) FROM inbox i LEFT JOIN delivery_attempts a ON a.message_id=i.id WHERE i.grant_id=? AND i.lane='data' AND i.state IN ('pending','offered','delivered','parked','intent','authorized','in_doubt','adapter_accepted','responder_started','dequeued') ORDER BY i.id`, grantID)
	if err != nil {
		return nil, failure(StorageUnavailable)
	}
	var messages []revokedMessage
	for rows.Next() {
		var m revokedMessage
		var state, inboxState, route string
		var currentIntent bool
		if err = rows.Scan(&m.id, &m.digest, &m.attempt, &state, &inboxState, &route, &currentIntent); err != nil {
			break
		}
		// A confirmed notice or explicit body read is immutable evidence. Revoke
		// removes future authority without claiming that observation never happened.
		if state == "" && route == "inbox_no_wake" && (inboxState == "offered" || inboxState == "delivered" || inboxState == "adapter_accepted") {
			continue
		}
		m.stage = "revoked"
		switch {
		case state == "dequeued":
			m.outcome = "dequeued"
		case (state == "intent" && currentIntent) || state == "not_sent":
			m.outcome = "not_sent"
		case state != "":
			m.stage, m.outcome = "in_doubt", "submit_unknown"
			if state == "adapter_accepted" || state == "responder_started" {
				m.outcome = "already_admitted"
			}
		}
		messages = append(messages, m)
	}
	iterErr, closeErr := rows.Err(), rows.Close()
	if err != nil || iterErr != nil || closeErr != nil {
		return nil, failure(StorageUnavailable)
	}
	return messages, nil
}

func (s *Store) revokeOutbox(ctx context.Context, tx *sql.Tx, grantID string, epoch uint64, now int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT id,digest FROM outbox WHERE grant_id=? AND lane='data' AND stage='sender_stored' ORDER BY id`, grantID)
	if err != nil {
		return failure(StorageUnavailable)
	}
	var messages []revokedMessage
	for rows.Next() {
		var m revokedMessage
		if err = rows.Scan(&m.id, &m.digest); err != nil {
			break
		}
		messages = append(messages, m)
	}
	iterErr, closeErr := rows.Err(), rows.Close()
	if err != nil || iterErr != nil || closeErr != nil {
		return failure(StorageUnavailable)
	}
	for _, m := range messages {
		r := protocol.ReceiptPayload{MessageID: m.id, AttemptID: m.id, Stage: "revoked", EvidenceKind: "local_revoke", ReasonCode: "revoked", At: now}
		if err = s.recordReceipt(ctx, tx, OutboundReceipt, grantID, r); err != nil {
			return err
		}
		if err = s.audit(ctx, tx, now, "outbox_revoke", grantID, epoch, m.id, m.digest, "revoked"); err != nil {
			return err
		}
	}
	return nil
}
