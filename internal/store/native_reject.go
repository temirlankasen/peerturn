package store

import (
	"context"
	"database/sql"

	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// RejectPendingDelivery closes only work proven not to have reached native submit.
// Invalid authority must not prevent storing its own terminal rejection.
func (s *Store) RejectPendingDelivery(ctx context.Context, id, reason string, now int64, notification ...NativeNotification) (protocol.ReceiptPayload, error) {
	r := protocol.ReceiptPayload{MessageID: id, AttemptID: id, Stage: "rejected", EvidenceKind: "adapter_preflight", ReasonCode: reason, At: now}
	if !protocol.ValidID(id) || now <= 0 || now > protocol.MaxSafeInteger || len(notification) > 1 {
		return r, failure(fault.InvalidEncoding)
	}
	switch reason {
	case "revoked", "expired", "invalid_encoding", "invalid_grant", "invalid_certificate", "scope_denied", "too_large":
	default:
		if !IsResponderRefusal(reason) {
			return r, failure(fault.InvalidEncoding)
		}
		r.EvidenceKind, r.Outcome = "responder_preflight", "responder_unavailable"
	}
	err := s.write(ctx, func(tx *sql.Tx) error {
		var grantID, state, route string
		if err := tx.QueryRowContext(ctx, `SELECT grant_id,state,route FROM inbox WHERE id=?`, id).Scan(&grantID, &state, &route); err != nil {
			return lookupError(err)
		}
		if IsResponderRefusal(reason) && route != string(envelope.Responder) {
			return failure(fault.ScopeDenied)
		}
		if state != "pending" && state != "intent" {
			return failure(InvalidState)
		}
		if state == "intent" {
			if err := tx.QueryRowContext(ctx, `SELECT id FROM delivery_attempts WHERE message_id=? AND state='intent'`, id).Scan(&r.AttemptID); err != nil {
				return lookupError(err)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE delivery_attempts SET state='rejected' WHERE id=?`, r.AttemptID); err != nil {
				return databaseError(err)
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE inbox SET state='quarantined' WHERE id=?`, id); err != nil {
			return databaseError(err)
		}
		if err := s.recordReceipt(ctx, tx, InboundReceipt, grantID, r); err != nil {
			return err
		}
		if len(notification) == 1 {
			if err := s.nativeNotification(ctx, tx, grantID, r, notification[0]); err != nil {
				return err
			}
		}
		return s.audit(ctx, tx, now, "adapter_reject", grantID, 0, id, "", reason)
	})
	return r, err
}
