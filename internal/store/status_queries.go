package store

import (
	"context"

	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/identity"
)

// UncertainDeliveryIDs names the deliveries whose native outcome is unknown,
// so status can tell the owner which message to settle instead of a bare
// in_doubt state.
func (s *Store) UncertainDeliveryIDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT a.message_id FROM delivery_attempts a JOIN inbox i ON i.id=a.message_id WHERE a.state='in_doubt' AND i.route<>? ORDER BY a.created_at`, envelope.Responder)
	if err != nil {
		return nil, failure(StorageUnavailable)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			return
		}
	}()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, failure(StorageUnavailable)
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return nil, failure(StorageUnavailable)
	}
	return ids, nil
}

// OutboundWaiting counts own data packets to recipient that no receiver has
// confirmed yet and reports when the oldest was created. A message that sits
// at the relay for hours means the peer's daemon is not fetching its mail.
func (s *Store) OutboundWaiting(ctx context.Context, sender, recipient identity.Binding, now int64) (int, int64, error) {
	var count int
	var since int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(MIN(created_at),0) FROM outbox WHERE channel=? AND lane='data' AND stage IN ('sender_stored','relay_stored') AND expires_at>?`, channelKey(sender, recipient), now).Scan(&count, &since)
	if err != nil {
		return 0, 0, failure(StorageUnavailable)
	}
	return count, since, nil
}
