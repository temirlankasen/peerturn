package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// CreateMailbox is called after admin authorization or with a claimed voucher.
func (s *Store) CreateMailbox(ctx context.Context, mailbox Mailbox, voucher string) error {
	if !validID(mailbox.ID) || !validID(mailbox.ReadKey) || len(voucher) > 256 {
		return invalid()
	}
	now := s.clockNow()
	return s.transact(ctx, now, func(tx *sql.Tx) error {
		var existing string
		err := tx.QueryRowContext(ctx, `SELECT read_key FROM mailboxes WHERE id=?`, mailbox.ID).Scan(&existing)
		if err == nil {
			if existing == mailbox.ReadKey {
				return nil
			}
			return conflict()
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return unavailable()
		}
		if voucher != "" {
			var allowed int
			err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM invites WHERE voucher_hash=? AND claim_reader=? AND expires_ns>? AND revoked=0 AND enrolled_mailbox IS NULL`, digest([]byte(voucher)), mailbox.ReadKey, now.UnixNano()).Scan(&allowed)
			if err != nil {
				return unavailable()
			}
			if allowed != 1 {
				return forbidden()
			}
		}
		if err := s.room(ctx, tx, 0); err != nil {
			return err
		}
		if roomErr := s.tenantRoom(ctx, tx, mailbox.ReadKey, 0); roomErr != nil {
			return roomErr
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO mailboxes(id,read_key) VALUES(?,?)`, mailbox.ID, mailbox.ReadKey); err != nil {
			return unavailable()
		}
		if voucher != "" {
			if _, err := tx.ExecContext(ctx, `UPDATE invites SET enrolled_mailbox=? WHERE voucher_hash=?`, mailbox.ID, digest([]byte(voucher))); err != nil {
				return unavailable()
			}
		}
		return nil
	})
}

// CheckReader verifies the exact routing key bound at enrollment.
func (s *Store) CheckReader(ctx context.Context, mailboxID, readKey string) error {
	return reader(ctx, s.db, mailboxID, readKey)
}

// PutPermit rejects epoch rollback and conflicting reuse of an existing epoch.
func (s *Store) PutPermit(ctx context.Context, mailboxID, readKey string, permit Permit) error {
	if !validID(permit.ID) || !validID(permit.WriterKey) || permit.Epoch < 1 || permit.ExpiresAt.IsZero() {
		return invalid()
	}
	return s.transact(ctx, s.clockNow(), func(tx *sql.Tx) error {
		if err := reader(ctx, tx, mailboxID, readKey); err != nil {
			return err
		}
		old, err := getPermit(ctx, tx, mailboxID, permit.ID)
		if err == nil {
			if permit.Epoch < old.Epoch {
				return conflict()
			}
			if permit.Epoch == old.Epoch {
				if permit.WriterKey == old.WriterKey && permit.ExpiresAt.Equal(old.ExpiresAt) && permit.Revoked == old.Revoked {
					return nil
				}
				return conflict()
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return unavailable()
		} else if roomErr := s.room(ctx, tx, 0); roomErr != nil {
			return roomErr
		} else if roomErr := s.permitRoom(ctx, tx, mailboxID); roomErr != nil {
			return roomErr
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO permits(mailbox_id,id,writer_key,epoch,expires_ns,revoked) VALUES(?,?,?,?,?,?) ON CONFLICT(mailbox_id,id) DO UPDATE SET writer_key=excluded.writer_key,epoch=excluded.epoch,expires_ns=excluded.expires_ns,revoked=excluded.revoked`, mailboxID, permit.ID, permit.WriterKey, permit.Epoch, permit.ExpiresAt.UnixNano(), permit.Revoked)
		if err != nil {
			return unavailable()
		}
		return nil
	})
}

// RevokePermit retains the epoch even when the original permit is absent.
func (s *Store) RevokePermit(ctx context.Context, mailboxID, readKey, permitID string, epoch int64) error {
	if !validID(permitID) || epoch < 1 {
		return invalid()
	}
	return s.transact(ctx, s.clockNow(), func(tx *sql.Tx) error {
		if err := reader(ctx, tx, mailboxID, readKey); err != nil {
			return err
		}
		old, err := getPermit(ctx, tx, mailboxID, permitID)
		if err == nil {
			if epoch < old.Epoch || (epoch == old.Epoch && !old.Revoked) {
				return conflict()
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return unavailable()
		} else if roomErr := s.room(ctx, tx, 0); roomErr != nil {
			return roomErr
		} else if roomErr := s.permitRoom(ctx, tx, mailboxID); roomErr != nil {
			return roomErr
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO permits(mailbox_id,id,writer_key,epoch,expires_ns,revoked) VALUES(?,?,'',?,0,1) ON CONFLICT(mailbox_id,id) DO UPDATE SET epoch=excluded.epoch,revoked=1`, mailboxID, permitID, epoch)
		if err != nil {
			return unavailable()
		}
		return nil
	})
}

// KnownMailboxKey admits a reader, active writer, or retained writer receipt retry.
func (s *Store) KnownMailboxKey(ctx context.Context, mailboxID, key string, now time.Time) (bool, error) {
	var known bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mailboxes WHERE id=? AND read_key=?) OR EXISTS(SELECT 1 FROM permits WHERE mailbox_id=? AND writer_key=? AND revoked=0 AND expires_ns>?) OR EXISTS(SELECT 1 FROM messages WHERE mailbox_id=? AND writer_key=?)`, mailboxID, key, mailboxID, key, now.UnixNano(), mailboxID, key).Scan(&known)
	if err != nil {
		return false, unavailable()
	}
	return known, nil
}

// DeleteMailbox removes custody and routing metadata only for its exact owner.
func (s *Store) DeleteMailbox(ctx context.Context, mailboxID, readKey string) error {
	return s.transact(ctx, s.clockNow(), func(tx *sql.Tx) error {
		if err := reader(ctx, tx, mailboxID, readKey); err != nil {
			return err
		}
		var items, size int64
		if err := tx.QueryRowContext(ctx, `SELECT 1+
		 (SELECT COUNT(*) FROM permits WHERE mailbox_id=?)+
		 (SELECT COUNT(*) FROM messages WHERE mailbox_id=?)+
		 (SELECT COUNT(*) FROM tickets WHERE mailbox_id=?),
		 COALESCE((SELECT SUM(LENGTH(packet)) FROM messages WHERE mailbox_id=?),0)`, mailboxID, mailboxID, mailboxID, mailboxID).Scan(&items, &size); err != nil {
			return unavailable()
		}
		for _, query := range []string{
			`DELETE FROM tickets WHERE mailbox_id=?`,
			`DELETE FROM messages WHERE mailbox_id=?`,
			`DELETE FROM permits WHERE mailbox_id=?`,
			`DELETE FROM mailboxes WHERE id=?`,
		} {
			if _, err := tx.ExecContext(ctx, query, mailboxID); err != nil {
				return unavailable()
			}
		}
		s.pending.items -= items
		s.pending.bytes -= size
		return nil
	})
}

func getPermit(ctx context.Context, tx queryer, mailboxID, permitID string) (Permit, error) {
	var permit Permit
	var expires int64
	err := tx.QueryRowContext(ctx, `SELECT id,writer_key,epoch,expires_ns,revoked FROM permits WHERE mailbox_id=? AND id=?`, mailboxID, permitID).Scan(&permit.ID, &permit.WriterKey, &permit.Epoch, &expires, &permit.Revoked)
	permit.ExpiresAt = time.Unix(0, expires).UTC()
	return permit, err
}

// GetPermit returns routing ACL metadata, never an E2E grant or scope.
func (s *Store) GetPermit(ctx context.Context, mailboxID, permitID string) (Permit, error) {
	permit, err := getPermit(ctx, s.db, mailboxID, permitID)
	if errors.Is(err, sql.ErrNoRows) {
		return Permit{}, forbidden()
	}
	if err != nil {
		return Permit{}, unavailable()
	}
	return permit, nil
}

func checkPermit(ctx context.Context, tx queryer, mailboxID, permitID, writerKey string, epoch int64, now time.Time) error {
	permit, err := getPermit(ctx, tx, mailboxID, permitID)
	if errors.Is(err, sql.ErrNoRows) {
		return forbidden()
	}
	if err != nil {
		return unavailable()
	}
	if permit.Revoked || permit.WriterKey != writerKey || permit.Epoch != epoch || !permit.ExpiresAt.After(now) {
		return forbidden()
	}
	return nil
}

// CheckPermit revalidates a writer against the current monotonic ACL epoch.
func (s *Store) CheckPermit(ctx context.Context, mailboxID, permitID, writerKey string, epoch int64, now time.Time) error {
	return checkPermit(ctx, s.db, mailboxID, permitID, writerKey, epoch, now)
}

// HighSeq is routing metadata for already-authorized notification streams.
func (s *Store) HighSeq(ctx context.Context, mailboxID string) (int64, error) {
	var seq int64
	err := s.db.QueryRowContext(ctx, `SELECT next_seq FROM mailboxes WHERE id=?`, mailboxID).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, forbidden()
	}
	if err != nil {
		return 0, unavailable()
	}
	return seq, nil
}
