package store

import (
	"context"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// ExpiredOutboxGrants identifies sender custody whose reserved sequence cannot
// be skipped. The caller must fence those peers before minimizing expired rows.
// This reads metadata only; it neither opens packets nor changes custody.
func (s *Store) ExpiredOutboxGrants(ctx context.Context, now int64) ([]string, error) {
	if now <= 0 || now > protocol.MaxSafeInteger {
		return nil, failure(fault.InvalidEncoding)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT grant_id FROM outbox WHERE stage='sender_stored' AND expires_at<=? ORDER BY grant_id LIMIT 4097`, now)
	if err != nil {
		return nil, failure(StorageUnavailable)
	}
	var grants []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		grants = append(grants, id)
	}
	iterErr, closeErr := rows.Err(), rows.Close()
	if err != nil || iterErr != nil || closeErr != nil {
		return nil, failure(StorageUnavailable)
	}
	if len(grants) > 4096 {
		return nil, failure(Backpressure)
	}
	return grants, nil
}
