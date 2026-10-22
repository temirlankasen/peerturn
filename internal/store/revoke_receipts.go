package store

import (
	"context"
	"database/sql"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// RevokeReceipts lets the daemon prebuild the exact signed native-attempt outcome
// under its operation lock before GrantRevoke commits the matching decisions.
func (s *Store) RevokeReceipts(ctx context.Context, grantID string, now int64) ([]protocol.ReceiptPayload, error) {
	if !protocol.ValidToken(grantID) || now <= 0 || now > protocol.MaxSafeInteger {
		return nil, failure(fault.InvalidEncoding)
	}
	var receipts []protocol.ReceiptPayload
	err := s.write(ctx, func(tx *sql.Tx) error {
		messages, err := revokeMessages(ctx, tx, grantID)
		if err != nil {
			return err
		}
		for _, m := range messages {
			receipts = append(receipts, protocol.ReceiptPayload{MessageID: m.id, AttemptID: m.attempt, Stage: m.stage, EvidenceKind: "local_revoke", ReasonCode: "revoked", Outcome: m.outcome, At: now})
		}
		return nil
	})
	return receipts, err
}
