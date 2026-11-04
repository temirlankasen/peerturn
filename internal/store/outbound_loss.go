package store

import (
	"context"
	"database/sql"

	"github.com/temirlankasen/peerturn/internal/identity"
)

// OutboundLoss reports whether an own data packet to recipient expired at or
// before before without a receiver receipt, so the peer may wait on its
// sequence. Data filed under either channel key of the pair counts.
func (s *Store) OutboundLoss(ctx context.Context, sender, recipient identity.Binding, before int64) (bool, error) {
	var lost bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM outbox WHERE channel IN (?,?) AND lane='data' AND expires_at<=? AND stage IN ('sender_stored','relay_stored','expired'))`, channelKey(sender, recipient), channelKey(recipient, sender), before).Scan(&lost)
	if err != nil {
		return false, failure(StorageUnavailable)
	}
	return lost, nil
}

// ParkedAtOrBelow returns the lowest sequence from sender still parked at or
// below floor, so an announced floor skips only sequences never received.
func (s *Store) ParkedAtOrBelow(ctx context.Context, sender, recipient identity.Binding, floor uint64) (uint64, bool, error) {
	var seq sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT MIN(sender_seq) FROM inbox WHERE channel=? AND state='parked' AND sender_seq<=?`, channelKey(sender, recipient), floor).Scan(&seq)
	if err != nil {
		return 0, false, failure(StorageUnavailable)
	}
	if !seq.Valid || seq.Int64 <= 0 {
		return 0, false, nil
	}
	return uint64(seq.Int64), true, nil
}

// OutboxLive reports whether an own packet still expires after before. A
// missing row is collected custody and is not live.
func (s *Store) OutboxLive(ctx context.Context, id string, before int64) (bool, error) {
	var live bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM outbox WHERE id=? AND expires_at>?)`, id, before).Scan(&live)
	if err != nil {
		return false, failure(StorageUnavailable)
	}
	return live, nil
}
