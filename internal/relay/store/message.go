package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"math"
	"time"
)

// PutMessage checks live permission, deduplication, quotas, and seq atomically.
func (s *Store) PutMessage(ctx context.Context, message Message, now time.Time) (Receipt, error) {
	if !validID(message.ID) || !validID(message.MailboxID) || !validID(message.PermitID) || !validID(message.WriterKey) || message.PermitEpoch < 1 || !validPacket(message.Packet) || message.ExpiresAt.IsZero() {
		return Receipt{}, invalid()
	}
	var receipt Receipt
	s.mu.Lock()
	defer s.mu.Unlock()
	var existingMailbox string
	var existingDigest []byte
	err := s.db.QueryRowContext(ctx, `SELECT mailbox_id,seq,digest FROM messages WHERE writer_key=? AND message_id=?`, message.WriterKey, message.ID).Scan(&existingMailbox, &receipt.Seq, &existingDigest)
	if err == nil {
		if existingMailbox != message.MailboxID || !bytes.Equal(existingDigest, digest(message.Packet)) {
			return Receipt{}, conflict()
		}
		receipt.Duplicate = true
		return receipt, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Receipt{}, unavailable()
	}
	err = s.transactLocked(ctx, now, func(tx *sql.Tx) error {
		if permissionErr := checkPermit(ctx, tx, message.MailboxID, message.PermitID, message.WriterKey, message.PermitEpoch, now); permissionErr != nil {
			return permissionErr
		}
		if !message.ExpiresAt.After(now) {
			return &Error{Code: "expired_message", Status: 410}
		}
		if message.ExpiresAt.Before(now.Truncate(time.Second).Add(MinMessageTTL)) || message.ExpiresAt.After(now.Add(MaxMessageTTL)) {
			return invalid()
		}
		if roomErr := s.room(ctx, tx, len(message.Packet)); roomErr != nil {
			return roomErr
		}
		if mailboxErr := s.mailboxRoom(ctx, tx, message.MailboxID, len(message.Packet)); mailboxErr != nil {
			return mailboxErr
		}
		if byteErr := s.messageBytesRoom(ctx, tx, message); byteErr != nil {
			return byteErr
		}
		var count, size int64
		err = tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(LENGTH(packet)),0) FROM messages WHERE mailbox_id=? AND seq>(SELECT ack_seq FROM mailboxes WHERE id=?)`, message.MailboxID, message.MailboxID).Scan(&count, &size)
		if err != nil {
			return unavailable()
		}
		if count >= int64(s.limits.MailboxItems) || size > s.limits.MailboxBytes-int64(len(message.Packet)) {
			return quota()
		}
		err = tx.QueryRowContext(ctx, `SELECT next_seq FROM mailboxes WHERE id=?`, message.MailboxID).Scan(&receipt.Seq)
		if err != nil || receipt.Seq == math.MaxInt64 {
			return unavailable()
		}
		receipt.Seq++
		_, err = tx.ExecContext(ctx, `INSERT INTO messages(mailbox_id,seq,writer_key,message_id,permit_id,permit_epoch,packet,digest,expires_ns) VALUES(?,?,?,?,?,?,?,?,?)`, message.MailboxID, receipt.Seq, message.WriterKey, message.ID, message.PermitID, message.PermitEpoch, message.Packet, digest(message.Packet), message.ExpiresAt.UnixNano())
		if err != nil {
			return unavailable()
		}
		_, err = tx.ExecContext(ctx, `UPDATE mailboxes SET next_seq=? WHERE id=?`, receipt.Seq, message.MailboxID)
		if err != nil {
			return unavailable()
		}
		return nil
	})
	if err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

// Fetch issues only a contiguous range; tombstones let a reader cross expiry.
func (s *Store) Fetch(ctx context.Context, mailboxID, readKey string, after int64, limit int, now time.Time) ([]Message, error) {
	if after < 0 || limit < 1 || limit > 100 {
		return nil, invalid()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := reader(ctx, s.db, mailboxID, readKey); err != nil {
		return nil, err
	}
	var issued int64
	if err := s.db.QueryRowContext(ctx, `SELECT issued_seq FROM mailboxes WHERE id=?`, mailboxID).Scan(&issued); err != nil {
		return nil, unavailable()
	}
	if after > issued {
		return nil, invalid()
	}
	batch, err := fetchMessages(ctx, s.db, mailboxID, after, limit, now)
	if err != nil {
		return nil, err
	}
	if len(batch) > 0 && batch[len(batch)-1].Seq > issued {
		if err := s.transactLocked(ctx, now, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `UPDATE mailboxes SET issued_seq=? WHERE id=?`, batch[len(batch)-1].Seq, mailboxID); err != nil {
				return unavailable()
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	return batch, nil
}

func fetchMessages(ctx context.Context, tx *sql.DB, mailboxID string, after int64, limit int, now time.Time) (batch []Message, resultErr error) {
	rows, err := tx.QueryContext(ctx, `SELECT seq,writer_key,message_id,permit_id,permit_epoch,packet,expires_ns FROM messages WHERE mailbox_id=? AND seq>? ORDER BY seq LIMIT ?`, mailboxID, after, limit)
	if err != nil {
		return nil, unavailable()
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && resultErr == nil {
			batch, resultErr = nil, unavailable()
		}
	}()
	batch = make([]Message, 0)
	pageBytes := 0
	for rows.Next() {
		message := Message{MailboxID: mailboxID}
		var expires int64
		if err := rows.Scan(&message.Seq, &message.WriterKey, &message.ID, &message.PermitID, &message.PermitEpoch, &message.Packet, &expires); err != nil {
			return nil, unavailable()
		}
		message.ExpiresAt = time.Unix(0, expires).UTC()
		message.Tombstone = message.Packet == nil || !message.ExpiresAt.After(now)
		if message.Tombstone {
			message.Packet = nil
		}
		if len(batch) > 0 && pageBytes+len(message.Packet) > 1<<20 {
			break
		}
		pageBytes += len(message.Packet)
		batch = append(batch, message)
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable()
	}
	return batch, nil
}

// Ack records custody only within the contiguous range this reader was issued.
func (s *Store) Ack(ctx context.Context, mailboxID, readKey string, through int64, now time.Time) error {
	if through < 0 {
		return invalid()
	}
	return s.transact(ctx, now, func(tx *sql.Tx) error {
		if err := reader(ctx, tx, mailboxID, readKey); err != nil {
			return err
		}
		var issued, acked int64
		if err := tx.QueryRowContext(ctx, `SELECT issued_seq,ack_seq FROM mailboxes WHERE id=?`, mailboxID).Scan(&issued, &acked); err != nil {
			return unavailable()
		}
		if through > issued {
			return invalid()
		}
		if through <= acked {
			return nil
		}
		var removed int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(LENGTH(packet)),0) FROM messages WHERE mailbox_id=? AND seq<=?`, mailboxID, through).Scan(&removed); err != nil {
			return unavailable()
		}
		if _, err := tx.ExecContext(ctx, `UPDATE messages SET packet=NULL WHERE mailbox_id=? AND seq<=?`, mailboxID, through); err != nil {
			return unavailable()
		}
		if _, err := tx.ExecContext(ctx, `UPDATE mailboxes SET ack_seq=? WHERE id=?`, through, mailboxID); err != nil {
			return unavailable()
		}
		s.pending.bytes -= removed
		return nil
	})
}
