package store

import (
	"context"
	"database/sql"
	"errors"

	peercrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/fault"
)

// Owner revocation has a bounded reserve independent of the normal receipt
// backlog. Filling a peer queue must not prevent its owner from fencing it.
func (s *Store) putRevokeOutbox(ctx context.Context, tx *sql.Tx, id, grantID string, raw []byte, expiry, now int64) error {
	var existing string
	err := tx.QueryRowContext(ctx, `SELECT digest FROM outbox WHERE id=?`, id).Scan(&existing)
	if err == nil {
		return failure(fault.Conflict)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return failure(StorageUnavailable)
	}
	var count, used int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(bytes),0) FROM outbox WHERE grant_id=? AND priority=1`, grantID).Scan(&count, &used); err != nil {
		return failure(StorageUnavailable)
	}
	if count >= MaxRetainedMessages || len(raw) > MaxRetainedBytes-used {
		return failure(Backpressure)
	}
	var collision int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM inbox WHERE id=?`, id).Scan(&collision); err != nil {
		return failure(StorageUnavailable)
	}
	if collision != 0 {
		return failure(fault.Conflict)
	}
	sealed, err := s.seal("outbox", id, raw)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO outbox(id,grant_id,digest,packet,lane,stage,expires_at,bytes,created_at,channel,priority) VALUES(?,?,?,?,'control','sender_stored',?,?,?,(SELECT issuer||':'||subject FROM grants WHERE id=?),1)`, id, grantID, peercrypto.Digest(raw), sealed, expiry, len(raw), now, grantID)
	return databaseError(err)
}
