package store

import (
	"context"
	"database/sql"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// AuthorizeAsk rechecks the exact reserved run immediately before child spawn.
// It binds native-call authorization to the still-current parent context as well
// as grant, lease generation, and TTL; reservation alone never authorizes spawn.
func (s *Store) AuthorizeAsk(ctx context.Context, runID string, now int64) error {
	if !protocol.ValidID(runID) || now <= 0 || now > protocol.MaxSafeInteger {
		return failure(fault.InvalidEncoding)
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		a, err := s.loadAsk(ctx, tx, runID)
		if err != nil {
			return err
		}
		if a.state != "reserved" || (a.attemptState != "intent" && a.attemptState != "authorized") {
			return failure(fault.Conflict)
		}
		if _, err = s.authorizeAsk(ctx, tx, a, now); err != nil {
			return err
		}
		if a.attemptState == "authorized" {
			return nil
		}
		if _, err = tx.ExecContext(ctx, `UPDATE delivery_attempts SET state='authorized' WHERE id=?`, a.AttemptID); err != nil {
			return failure(StorageUnavailable)
		}
		if _, err = tx.ExecContext(ctx, `UPDATE inbox SET state='authorized' WHERE id=?`, a.MessageID); err != nil {
			return failure(StorageUnavailable)
		}
		return s.audit(ctx, tx, now, "ask_authorized", a.grantID, a.epoch, a.MessageID, "", runID)
	})
}
