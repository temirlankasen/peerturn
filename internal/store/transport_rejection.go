package store

import (
	"context"
	"database/sql"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// MarkTransportRejected commits a verified permanent relay denial. Control rows
// have no peer-visible receipt namespace and are stopped without inventing one.
func (s *Store) MarkTransportRejected(ctx context.Context, id string, now int64) error {
	if !protocol.ValidID(id) || now <= 0 || now > protocol.MaxSafeInteger {
		return failure(fault.InvalidEncoding)
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		var lane, grantID, stage string
		if err := tx.QueryRowContext(ctx, `SELECT lane,grant_id,stage FROM outbox WHERE id=?`, id).Scan(&lane, &grantID, &stage); err != nil {
			return lookupError(err)
		}
		if stage != "sender_stored" {
			return nil
		}
		if lane == "data" {
			return s.recordReceipt(ctx, tx, OutboundReceipt, grantID, protocol.ReceiptPayload{MessageID: id, AttemptID: id, Stage: "rejected", EvidenceKind: "relay_denied", ReasonCode: "revoked", At: now})
		}
		_, err := tx.ExecContext(ctx, `UPDATE outbox SET stage='rejected' WHERE id=? AND stage='sender_stored'`, id)
		return databaseError(err)
	})
}
