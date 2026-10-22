package store

import (
	"context"
	"database/sql"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// RecoverInDoubt runs after the old adapter has stopped. Only durable native-call
// authorization is ambiguous; an unsubmitted reservation becomes reeligible.
func (s *Store) RecoverInDoubt(ctx context.Context, now int64) error {
	if now <= 0 || now > protocol.MaxSafeInteger {
		return failure(fault.InvalidEncoding)
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT a.id,a.message_id,a.state,i.grant_id FROM delivery_attempts a JOIN inbox i ON i.id=a.message_id WHERE a.state IN ('intent','authorized')`)
		if err != nil {
			return failure(StorageUnavailable)
		}
		type recovery struct{ attempt, message, state, grant string }
		var pending []recovery
		for rows.Next() {
			var item recovery
			if err = rows.Scan(&item.attempt, &item.message, &item.state, &item.grant); err != nil {
				break
			}
			pending = append(pending, item)
		}
		iterErr, closeErr := rows.Err(), rows.Close()
		if err != nil || iterErr != nil || closeErr != nil {
			return failure(StorageUnavailable)
		}
		for _, item := range pending {
			r := protocol.ReceiptPayload{MessageID: item.message, AttemptID: item.attempt, Stage: "in_doubt", EvidenceKind: "crash_recovery", At: now}
			attemptState, inboxState := "in_doubt", "in_doubt"
			var currentIntent bool
			if item.state == "intent" {
				if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM audit WHERE action='delivery_intent' AND message_id=? AND reason=?)`, item.message, item.attempt).Scan(&currentIntent); err != nil {
					return failure(StorageUnavailable)
				}
			}
			// Legacy intent rows predate durable authorization and cannot prove
			// whether the old adapter called submit. Missing evidence stays fenced.
			if currentIntent {
				r.Stage, r.Outcome, r.ReasonCode = "receiver_stored", "not_sent", "not_authorized"
				attemptState, inboxState = "not_sent", "pending"
				if err = s.resetUnsentReceipt(ctx, tx, item.grant, r); err != nil {
					return err
				}
			} else if err = s.recordReceipt(ctx, tx, InboundReceipt, item.grant, r); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE delivery_attempts SET state=? WHERE id=?`, attemptState, item.attempt); err != nil {
				return failure(StorageUnavailable)
			}
			if _, err = tx.ExecContext(ctx, `UPDATE inbox SET state=? WHERE id=? AND state IN ('intent','authorized')`, inboxState, item.message); err != nil {
				return failure(StorageUnavailable)
			}
			if err = s.audit(ctx, tx, now, "delivery_recovery", item.grant, 0, item.message, "", item.attempt+":"+attemptState); err != nil {
				return err
			}
		}
		return nil
	})
}

// resetUnsentReceipt replaces only storage evidence for a locally proven unsent
// intent. It cannot lower acceptance, terminal decisions or ambiguous evidence.
func (s *Store) resetUnsentReceipt(ctx context.Context, tx *sql.Tx, grantID string, r protocol.ReceiptPayload) error {
	channel, err := receiptChannel(ctx, tx, InboundReceipt, grantID, r.MessageID)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM receipts WHERE direction='inbound' AND grant_id=? AND channel=? AND message_id=? AND rank<=3 AND terminal=0`, grantID, channel, r.MessageID)
	if err != nil {
		return failure(StorageUnavailable)
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return failure(fault.Conflict)
	}
	return s.recordReceipt(ctx, tx, InboundReceipt, grantID, r)
}

// ResolveDelivery accepts an explicit owner assertion about the exact ambiguous
// local attempt. The daemon must keep this operation out of peer and MCP ingress.
func (s *Store) ResolveDelivery(ctx context.Context, id string, sent bool, now int64) (protocol.ReceiptPayload, error) {
	r := protocol.ReceiptPayload{MessageID: id, Stage: "receiver_stored", EvidenceKind: "native_reconciliation", Outcome: "not_sent", At: now}
	if !protocol.ValidID(id) || now <= 0 || now > protocol.MaxSafeInteger {
		return protocol.ReceiptPayload{}, failure(fault.InvalidEncoding)
	}
	if sent {
		r.Stage, r.Outcome = "adapter_accepted", "sent"
	}
	err := s.write(ctx, func(tx *sql.Tx) error {
		var grantID, state string
		var revoked bool
		if err := tx.QueryRowContext(ctx, `SELECT a.id,a.state,i.grant_id,g.revoked FROM delivery_attempts a JOIN inbox i ON i.id=a.message_id JOIN grants g ON g.id=i.grant_id WHERE i.id=?`, id).Scan(&r.AttemptID, &state, &grantID, &revoked); err != nil {
			return lookupError(err)
		}
		if revoked {
			r.ReasonCode = "revoked"
		}
		if state != "in_doubt" {
			return failure(fault.Conflict)
		}
		return s.reconcileReceipt(ctx, tx, InboundReceipt, grantID, r)
	})
	if err != nil {
		return protocol.ReceiptPayload{}, err
	}
	return r, nil
}
