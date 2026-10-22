package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func checkStream(ctx context.Context, tx queryer, ticket Ticket, now time.Time) error {
	if ticket.ReaderKey == "" || ticket.WriterKey != "" || ticket.PermitID != "" || ticket.PermitEpoch != 0 {
		return forbidden()
	}
	return reader(ctx, tx, ticket.MailboxID, ticket.ReaderKey)
}

// CheckStream restricts mailbox-wide notifications to the exact reader.
func (s *Store) CheckStream(ctx context.Context, ticket Ticket, now time.Time) error {
	return checkStream(ctx, s.db, ticket, now)
}

// PutTicket binds an opaque one-use upgrade capability to notification access.
func (s *Store) PutTicket(ctx context.Context, ticket Ticket, now time.Time) error {
	if !validID(ticket.ID) || !validID(ticket.MailboxID) || !ticket.ExpiresAt.After(now) || ticket.ExpiresAt.After(now.Add(time.Minute)) {
		return invalid()
	}
	return s.transact(ctx, now, func(tx *sql.Tx) error {
		if err := checkStream(ctx, tx, ticket, now); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM tickets WHERE id_hash=?`, digest([]byte(ticket.ID))).Scan(&count); err != nil {
			return unavailable()
		}
		if count != 0 {
			return conflict()
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM tickets WHERE mailbox_id=?`, ticket.MailboxID).Scan(&count); err != nil {
			return unavailable()
		}
		if count >= 8 {
			return quota()
		}
		if err := s.mailboxRoom(ctx, tx, ticket.MailboxID, 0); err != nil {
			return err
		}
		if err := s.room(ctx, tx, 0); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO tickets(id_hash,mailbox_id,reader_key,permit_id,writer_key,permit_epoch,expires_ns) VALUES(?,?,?,?,?,?,?)`, digest([]byte(ticket.ID)), ticket.MailboxID, ticket.ReaderKey, ticket.PermitID, ticket.WriterKey, ticket.PermitEpoch, ticket.ExpiresAt.UnixNano())
		if err != nil {
			return unavailable()
		}
		return nil
	})
}

// PeekTicket checks a capability before reserving a stream, without consuming it.
func (s *Store) PeekTicket(ctx context.Context, id string, now time.Time) (Ticket, error) {
	if !validID(id) {
		return Ticket{}, invalid()
	}
	return peekTicket(ctx, s.db, id, now)
}

func peekTicket(ctx context.Context, tx queryer, id string, now time.Time) (Ticket, error) {
	ticket := Ticket{ID: id}
	var expires int64
	var consumed bool
	err := tx.QueryRowContext(ctx, `SELECT mailbox_id,reader_key,permit_id,writer_key,permit_epoch,expires_ns,consumed FROM tickets WHERE id_hash=?`, digest([]byte(id))).Scan(&ticket.MailboxID, &ticket.ReaderKey, &ticket.PermitID, &ticket.WriterKey, &ticket.PermitEpoch, &expires, &consumed)
	if errors.Is(err, sql.ErrNoRows) {
		return Ticket{}, forbidden()
	}
	if err != nil {
		return Ticket{}, unavailable()
	}
	ticket.ExpiresAt = time.Unix(0, expires).UTC()
	if consumed || !ticket.ExpiresAt.After(now) {
		return Ticket{}, forbidden()
	}
	if err := checkStream(ctx, tx, ticket, now); err != nil {
		return Ticket{}, err
	}
	return ticket, nil
}

// ConsumeTicket commits single use and rechecks ACLs before stream admission.
func (s *Store) ConsumeTicket(ctx context.Context, id string, now time.Time) (Ticket, error) {
	if !validID(id) {
		return Ticket{}, invalid()
	}
	ticket := Ticket{ID: id}
	err := s.transact(ctx, now, func(tx *sql.Tx) error {
		var expires int64
		var consumed bool
		err := tx.QueryRowContext(ctx, `SELECT mailbox_id,reader_key,permit_id,writer_key,permit_epoch,expires_ns,consumed FROM tickets WHERE id_hash=?`, digest([]byte(id))).Scan(&ticket.MailboxID, &ticket.ReaderKey, &ticket.PermitID, &ticket.WriterKey, &ticket.PermitEpoch, &expires, &consumed)
		if errors.Is(err, sql.ErrNoRows) {
			return forbidden()
		}
		if err != nil {
			return unavailable()
		}
		ticket.ExpiresAt = time.Unix(0, expires).UTC()
		if consumed || !ticket.ExpiresAt.After(now) {
			return forbidden()
		}
		if err := checkStream(ctx, tx, ticket, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tickets SET consumed=1 WHERE id_hash=?`, digest([]byte(id))); err != nil {
			return unavailable()
		}
		return nil
	})
	if err != nil {
		return Ticket{}, err
	}
	return ticket, nil
}
