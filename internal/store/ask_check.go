package store

import (
	"context"
	"database/sql"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// CheckAskAuthority rechecks a running fork immediately before the daemon's
// revoke-serialized write of peer input. It never starts or repeats a submission.
func (s *Store) CheckAskAuthority(ctx context.Context, runID string, now int64) error {
	if !protocol.ValidID(runID) || now <= 0 || now > protocol.MaxSafeInteger {
		return failure(fault.InvalidEncoding)
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		a, err := s.loadAsk(ctx, tx, runID)
		if err != nil {
			return err
		}
		if a.state != "running" || a.attemptState != "responder_started" {
			return failure(InvalidState)
		}
		_, err = s.authorizeAsk(ctx, tx, a, now)
		return err
	})
}
