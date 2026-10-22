package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// ReceiptDirection separates evidence about received and sent submissions.
type ReceiptDirection string

// Receipt directions keep local receiver evidence separate from peer evidence
// about our submissions even when message identifiers collide.
const (
	InboundReceipt  ReceiptDirection = "inbound"
	OutboundReceipt ReceiptDirection = "outbound"
)

var receiptRanks = map[string]int{
	"sender_stored": 1, "relay_stored": 2, "receiver_stored": 3,
	"adapter_accepted": 4, "offered": 4, "delivered": 5, "responder_started": 4, "model_input": 5, "responder_input": 5,
	"turn_completed": 6, "responder_completed": 6, "reply_redacted": 7, "reply": 8,
	"in_doubt": 9, "rejected": 10, "expired": 10, "revoked": 10, "cancelled": 10, "responder_failed": 10,
}

// RecordReceipt records trusted local evidence for an existing submission.
// Authenticated peer evidence must instead pass the reverse-channel check.
func (s *Store) RecordReceipt(ctx context.Context, r protocol.ReceiptPayload) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		direction, grantID, err := receiptScope(ctx, tx, r.MessageID)
		if err != nil {
			return err
		}
		return s.recordReceipt(ctx, tx, direction, grantID, r)
	})
}

func (s *Store) recordReceipt(ctx context.Context, tx *sql.Tx, direction ReceiptDirection, grantID string, r protocol.ReceiptPayload) error {
	return s.saveReceipt(ctx, tx, direction, grantID, r, false)
}

func (s *Store) saveReceipt(ctx context.Context, tx *sql.Tx, direction ReceiptDirection, grantID string, r protocol.ReceiptPayload, reconcile bool) error {
	rank, ok := receiptRanks[r.Stage]
	if !ok {
		return failure(fault.InvalidEncoding)
	}
	check := r
	check.Stage = "receiver_stored"
	if err := check.Validate(); err != nil {
		return err
	}
	channel, err := receiptChannel(ctx, tx, direction, grantID, r.MessageID)
	if err != nil {
		return err
	}
	var oldRank int
	var terminal bool
	var oldStage, oldAttempt string
	var oldEvidence []byte
	err = tx.QueryRowContext(ctx, `SELECT rank,terminal,stage,attempt_id,evidence FROM receipts WHERE direction=? AND grant_id=? AND channel=? AND message_id=?`, direction, grantID, channel, r.MessageID).Scan(&oldRank, &terminal, &oldStage, &oldAttempt, &oldEvidence)
	var old protocol.ReceiptPayload
	if err == nil {
		raw, openErr := s.unseal("receipts", receiptKey(direction, grantID, channel, r.MessageID), oldEvidence)
		if openErr != nil {
			return openErr
		}
		if json.Unmarshal(raw, &old) != nil {
			return failure(InvalidState)
		}
	}
	if reconcile {
		if err != nil || oldStage != "in_doubt" || oldAttempt != r.AttemptID || !reconciledReceipt(r) || r.EvidenceKind != "native_reconciliation" {
			return failure(fault.Conflict)
		}
	} else if err == nil {
		// Content expiry and future revocation do not undo a confirmed notice
		// attachment or body read, including legacy M3 receipts after quarantine.
		informObserved := oldStage == "offered" || oldStage == "delivered" ||
			(oldStage == "adapter_accepted" && (old.Outcome == "offered" || old.Outcome == "delivered"))
		if direction == InboundReceipt && informObserved && (r.Stage == "expired" || r.Stage == "revoked") {
			return nil
		}
		if old.Outcome == "not_sent" && oldAttempt == r.AttemptID && rank > oldRank && rank < 10 {
			return failure(fault.Conflict)
		}
		informDelivered := oldStage == "adapter_accepted" && r.Stage == oldStage && oldAttempt == r.AttemptID && old.Outcome == "offered" && r.Outcome == "delivered" && r.EvidenceKind == "inbox_body_returned"
		breach := direction == InboundReceipt && r.EvidenceKind == "revoked_queue_started" && r.Stage == "in_doubt" && (oldStage == "revoked" || oldStage == "in_doubt")
		if (terminal || oldRank >= rank) && !informDelivered && !breach {
			return nil
		}
	}
	// Native progress changes effect evidence, never the revoked grant authority.
	if receiptRevokesGrant(old) {
		r.ReasonCode = "revoked"
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return failure(StorageUnavailable)
	}
	raw, err := codec.Canonical(r)
	if err != nil {
		return err
	}
	sealed, err := s.seal("receipts", receiptKey(direction, grantID, channel, r.MessageID), raw)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO receipts(direction,grant_id,channel,message_id,attempt_id,stage,rank,terminal,evidence,at) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(direction,grant_id,channel,message_id) DO UPDATE SET attempt_id=excluded.attempt_id,stage=excluded.stage,rank=excluded.rank,terminal=excluded.terminal,evidence=excluded.evidence,at=excluded.at`, direction, grantID, channel, r.MessageID, r.AttemptID, r.Stage, rank, rank >= 9, sealed, r.At)
	if err != nil {
		return failure(StorageUnavailable)
	}
	if direction == OutboundReceipt && rank >= 2 {
		_, err = tx.ExecContext(ctx, `UPDATE outbox SET stage=? WHERE id=? AND grant_id=? AND channel=?`, r.Stage, r.MessageID, grantID, channel)
	}
	return databaseError(err)
}

// Receipt reports committed evidence, never an inferred model outcome.
func (s *Store) Receipt(ctx context.Context, id string) (protocol.ReceiptPayload, error) {
	var direction ReceiptDirection
	var grantID, channel string
	var sealed []byte
	if err := s.db.QueryRowContext(ctx, `SELECT direction,grant_id,channel,evidence FROM receipts WHERE message_id=?`, id).Scan(&direction, &grantID, &channel, &sealed); err != nil {
		return protocol.ReceiptPayload{}, lookupError(err)
	}
	raw, err := s.unseal("receipts", receiptKey(direction, grantID, channel, id), sealed)
	if err != nil {
		return protocol.ReceiptPayload{}, err
	}
	var r protocol.ReceiptPayload
	if err = json.Unmarshal(raw, &r); err != nil {
		return r, failure(InvalidState)
	}
	return r, nil
}

// ReconcileReceipt replaces only an in_doubt decision using explicit trusted
// native evidence. Ordinary delayed receipts cannot clear the ambiguity fence.
func (s *Store) ReconcileReceipt(ctx context.Context, direction ReceiptDirection, grantID string, r protocol.ReceiptPayload) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		return s.reconcileReceipt(ctx, tx, direction, grantID, r)
	})
}

func (s *Store) reconcileReceipt(ctx context.Context, tx *sql.Tx, direction ReceiptDirection, grantID string, r protocol.ReceiptPayload) error {
	if err := s.saveReceipt(ctx, tx, direction, grantID, r, true); err != nil {
		return err
	}
	if direction == InboundReceipt {
		attemptState, inboxState := r.Stage, r.Stage
		if r.Stage == "receiver_stored" && r.Outcome == "not_sent" {
			attemptState, inboxState = "not_sent", "pending"
		}
		result, err := tx.ExecContext(ctx, `UPDATE delivery_attempts SET state=? WHERE id=? AND message_id=? AND state='in_doubt'`, attemptState, r.AttemptID, r.MessageID)
		if err != nil {
			return failure(StorageUnavailable)
		}
		changed, err := result.RowsAffected()
		if err != nil || changed != 1 {
			return failure(fault.Conflict)
		}
		if _, err = tx.ExecContext(ctx, `UPDATE inbox SET state=? WHERE id=? AND grant_id=? AND state='in_doubt'`, inboxState, r.MessageID, grantID); err != nil {
			return failure(StorageUnavailable)
		}
	}
	return s.audit(ctx, tx, r.At, "receipt_reconcile", grantID, 0, r.MessageID, "", r.AttemptID+":"+r.Stage+":"+r.Outcome)
}

func reconciledReceipt(r protocol.ReceiptPayload) bool {
	if r.Stage == "receiver_stored" {
		return r.Outcome == "not_sent"
	}
	switch r.Stage {
	case "adapter_accepted", "responder_started", "model_input", "responder_input", "turn_completed", "responder_completed", "reply_redacted", "reply", "rejected", "expired", "revoked", "cancelled", "responder_failed":
		return true
	default:
		return false
	}
}
