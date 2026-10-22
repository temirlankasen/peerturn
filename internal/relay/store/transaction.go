package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// SetClock supplies one clock for admission and expiry, including provisioning.
func (s *Store) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now != nil {
		s.now = now
	}
}

func (s *Store) clockNow() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now().UTC()
}

// Sweep reclaims expired ciphertext and replay rows on the relay maintenance cadence.
func (s *Store) Sweep(ctx context.Context, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now.Before(s.nextSweep) {
		return nil
	}
	if err := s.sweep(ctx, now); err != nil {
		return err
	}
	s.commits++
	s.needsCheckpoint = true
	return s.checkpoint(ctx)
}

func (s *Store) transact(ctx context.Context, now time.Time, fn func(*sql.Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.transactLocked(ctx, now, fn)
}

func (s *Store) transactLocked(ctx context.Context, now time.Time, fn func(*sql.Tx) error) error {
	if s.needsCheckpoint {
		if err := s.checkpoint(ctx); err != nil {
			return err
		}
	}
	// Cleanup commits independently: a rejected admission cannot restore expired rows.
	if !now.Before(s.nextSweep) {
		if err := s.sweep(ctx, now); err != nil {
			return err
		}
	}
	s.pending = s.stats
	if err := s.transaction(ctx, fn); err != nil {
		return err
	}
	s.stats = s.pending
	s.commits++
	s.needsCheckpoint = true
	// A committed receipt remains valid even if the checkpoint cannot finish.
	if err := s.checkpoint(ctx); err != nil {
		return nil
	}
	return nil
}

func (s *Store) transaction(ctx context.Context, fn func(*sql.Tx) error) (resultErr error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return unavailable()
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) && resultErr == nil {
			resultErr = unavailable()
		}
	}()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return unavailable()
	}
	return nil
}

func (s *Store) checkpoint(ctx context.Context) error {
	var busy, logFrames, checkpointed int
	statement := `PRAGMA wal_checkpoint(PASSIVE)`
	if s.commits%64 == 0 {
		statement = `PRAGMA wal_checkpoint(TRUNCATE)`
	}
	if err := s.db.QueryRowContext(ctx, statement).Scan(&busy, &logFrames, &checkpointed); err != nil || busy != 0 {
		return unavailable()
	}
	s.needsCheckpoint = false
	return nil
}

func (s *Store) sweep(ctx context.Context, now time.Time) error {
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		for _, query := range []string{
			`DELETE FROM nonces WHERE expires_ns<=?`,
			`DELETE FROM tickets WHERE expires_ns<=?`,
			`UPDATE messages SET packet=NULL WHERE expires_ns<=? AND packet IS NOT NULL`,
			`UPDATE invites SET packet=NULL,claim_packet=NULL WHERE expires_ns<=? AND (packet IS NOT NULL OR claim_packet IS NOT NULL)`,
		} {
			if _, err := tx.ExecContext(ctx, query, now.UnixNano()); err != nil {
				return unavailable()
			}
		}
		// Existing message rows are the dedup ledger; only ACKed rows may disappear.
		_, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE expires_ns<=? AND seq<=(SELECT ack_seq FROM mailboxes WHERE id=mailbox_id)`, now.Add(-DedupGrace).UnixNano())
		if err != nil {
			return unavailable()
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := s.refreshAccounting(ctx); err != nil {
		return err
	}
	s.nextSweep = now.Add(time.Minute)
	var next sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT MIN(expires_ns) FROM (
	 SELECT expires_ns FROM nonces UNION ALL SELECT expires_ns FROM tickets
	)`).Scan(&next); err != nil {
		return unavailable()
	}
	if next.Valid && time.Unix(0, next.Int64).Before(s.nextSweep) {
		s.nextSweep = time.Unix(0, next.Int64)
	}
	return nil
}
