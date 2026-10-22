package store

import (
	"context"
	"database/sql"
)

// The charge bounds row keys, indexes, B-tree splits, and overflow-page overhead.
const rowCharge = 4096

type accounting struct {
	items  int64
	bytes  int64
	nonces [3]int
}

func (s *Store) refreshAccounting(ctx context.Context) error {
	if err := s.db.QueryRowContext(ctx, metadataCount).Scan(&s.stats.items); err != nil {
		return unavailable()
	}
	if err := s.db.QueryRowContext(ctx, storedBytes).Scan(&s.stats.bytes); err != nil {
		return unavailable()
	}
	for lane := range s.stats.nonces {
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM nonces WHERE LENGTH(key_hash)=?`, 32+lane).Scan(&s.stats.nonces[lane]); err != nil {
			return unavailable()
		}
	}
	return nil
}

func (s *Store) room(_ context.Context, _ *sql.Tx, packetBytes int) error {
	return s.reserve(1, int64(packetBytes))
}

func (s *Store) reserve(items, packetBytes int64) error {
	count, size := s.pending.items+items, s.pending.bytes+packetBytes
	if count > int64(s.limits.MetadataItems) || count*rowCharge+size > s.dataBudget {
		return quota()
	}
	s.pending.items, s.pending.bytes = count, size
	return nil
}

func (s *Store) metadataCapacity() int {
	return min(s.limits.MetadataItems, max(1, int(s.dataBudget/rowCharge)))
}

func (s *Store) chargedCap(divisor int64) int64 {
	return s.dataBudget / divisor
}

func (s *Store) tenantRoom(ctx context.Context, tx *sql.Tx, readKey string, packetBytes int) error {
	var count, size int64
	err := tx.QueryRowContext(ctx, `SELECT
	 (SELECT COUNT(*) FROM mailboxes WHERE read_key=?)+
	 (SELECT COUNT(*) FROM permits WHERE mailbox_id IN (SELECT id FROM mailboxes WHERE read_key=?))+
	 (SELECT COUNT(*) FROM messages WHERE mailbox_id IN (SELECT id FROM mailboxes WHERE read_key=?))+
	 (SELECT COUNT(*) FROM tickets WHERE mailbox_id IN (SELECT id FROM mailboxes WHERE read_key=?)),
	 COALESCE((SELECT SUM(LENGTH(packet)) FROM messages WHERE mailbox_id IN (SELECT id FROM mailboxes WHERE read_key=?)),0)`, readKey, readKey, readKey, readKey, readKey).Scan(&count, &size)
	if err != nil {
		return unavailable()
	}
	if count >= int64(max(3, s.metadataCapacity()/4)) || (count+1)*rowCharge+size+int64(packetBytes) > s.chargedCap(4) {
		return quota()
	}
	return nil
}

func (s *Store) mailboxRoom(ctx context.Context, tx *sql.Tx, mailbox string, packetBytes int) error {
	var local, size int64
	var readKey string
	err := tx.QueryRowContext(ctx, `SELECT
	 (SELECT COUNT(*) FROM permits WHERE mailbox_id=?)+
	 (SELECT COUNT(*) FROM messages WHERE mailbox_id=?)+
	 (SELECT COUNT(*) FROM tickets WHERE mailbox_id=?),
	 COALESCE((SELECT SUM(LENGTH(packet)) FROM messages WHERE mailbox_id=?),0),
	 (SELECT read_key FROM mailboxes WHERE id=?)`, mailbox, mailbox, mailbox, mailbox, mailbox).Scan(&local, &size, &readKey)
	if err != nil {
		return unavailable()
	}
	if local >= int64(max(2, s.metadataCapacity()/8)) || (local+1)*rowCharge+size+int64(packetBytes) > s.chargedCap(8) {
		return quota()
	}
	return s.tenantRoom(ctx, tx, readKey, packetBytes)
}

func (s *Store) permitRoom(ctx context.Context, tx *sql.Tx, mailbox string) error {
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM permits WHERE mailbox_id=?`, mailbox).Scan(&count); err != nil {
		return unavailable()
	}
	if count >= min(32, max(1, s.metadataCapacity()/16)) {
		return quota()
	}
	return s.mailboxRoom(ctx, tx, mailbox, 0)
}

func (s *Store) messageBytesRoom(ctx context.Context, tx *sql.Tx, message Message) error {
	var count, size int64
	err := tx.QueryRowContext(ctx, `SELECT
	 COUNT(*),COALESCE(SUM(LENGTH(packet)),0) FROM messages WHERE writer_key=?`, message.WriterKey).Scan(&count, &size)
	if err != nil {
		return unavailable()
	}
	if count >= int64(max(1, s.metadataCapacity()/8)) || (count+1)*rowCharge+size+int64(len(message.Packet)) > s.chargedCap(8) {
		return quota()
	}
	return nil
}
