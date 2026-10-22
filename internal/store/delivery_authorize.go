package store

import (
	"context"
	"database/sql"

	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// AuthorizeDelivery rechecks the exact persisted attempt immediately before the
// native call; revoke, expiry and changed lease generations close admission.
func (s *Store) AuthorizeDelivery(ctx context.Context, id, attempt string, now int64) error {
	if !protocol.ValidID(id) || !protocol.ValidID(attempt) || now <= 0 || now > protocol.MaxSafeInteger {
		return failure(fault.InvalidEncoding)
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		var grantID, state string
		var expiry int64
		var epoch, generation, current uint64
		if err := tx.QueryRowContext(ctx, `SELECT i.grant_id,i.expires_at,a.state,a.grant_epoch,a.lease_generation,g.lease_generation FROM inbox i JOIN delivery_attempts a ON a.message_id=i.id JOIN grants g ON g.id=i.grant_id WHERE i.id=? AND a.id=?`, id, attempt).Scan(&grantID, &expiry, &state, &epoch, &generation, &current); err != nil {
			return lookupError(err)
		}
		if expiry <= now {
			return failure(fault.Expired)
		}
		if state != "intent" || generation != current {
			return failure(fault.Revoked)
		}
		g, err := s.authorization(ctx, tx, grantID, now)
		if err != nil {
			return err
		}
		if err = grantEpoch(g.GrantEpoch, epoch); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE delivery_attempts SET state='authorized' WHERE id=?`, attempt); err != nil {
			return failure(StorageUnavailable)
		}
		if _, err = tx.ExecContext(ctx, `UPDATE inbox SET state='authorized' WHERE id=?`, id); err != nil {
			return failure(StorageUnavailable)
		}
		return s.audit(ctx, tx, now, "delivery_authorized", grantID, epoch, id, "", attempt)
	})
}

// ReleaseDelivery relinquishes authorization only when the adapter has not called
// submit. Once a native call begins, only its result or reconciliation may settle it.
func (s *Store) ReleaseDelivery(ctx context.Context, id, attempt string, now int64) error {
	if !protocol.ValidID(id) || !protocol.ValidID(attempt) || now <= 0 || now > protocol.MaxSafeInteger {
		return failure(fault.InvalidEncoding)
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		var state, grantID string
		var epoch uint64
		if err := tx.QueryRowContext(ctx, `SELECT a.state,i.grant_id,a.grant_epoch FROM delivery_attempts a JOIN inbox i ON i.id=a.message_id WHERE a.id=? AND i.id=?`, attempt, id).Scan(&state, &grantID, &epoch); err != nil {
			return lookupError(err)
		}
		if state == "intent" {
			return nil
		}
		if state != "authorized" {
			return failure(fault.Conflict)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE delivery_attempts SET state='intent' WHERE id=?`, attempt); err != nil {
			return failure(StorageUnavailable)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE inbox SET state='intent' WHERE id=? AND state='authorized'`, id); err != nil {
			return failure(StorageUnavailable)
		}
		return s.audit(ctx, tx, now, "delivery_released", grantID, epoch, id, "", attempt)
	})
}

// IsDeliveryAuthorized checks the exact attempt before accepting native evidence.
func (s *Store) IsDeliveryAuthorized(ctx context.Context, id, attempt string) (bool, error) {
	if !protocol.ValidID(id) || !protocol.ValidID(attempt) {
		return false, failure(fault.InvalidEncoding)
	}
	var state string
	err := s.db.QueryRowContext(ctx, `SELECT state FROM delivery_attempts WHERE id=? AND message_id=?`, attempt, id).Scan(&state)
	if err != nil {
		return false, lookupError(err)
	}
	return state == "authorized", nil
}

// UncertainDeliveries keeps native admission closed across helper replacement
// until trusted reconciliation resolves every ambiguous live outcome. A fork's
// discarded answer cannot imply an ambiguous effect in the owner's live session.
func (s *Store) UncertainDeliveries(ctx context.Context) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM delivery_attempts a JOIN inbox i ON i.id=a.message_id WHERE a.state='in_doubt' AND i.route<>?`, envelope.Responder).Scan(&count)
	return count > 0, databaseError(err)
}
