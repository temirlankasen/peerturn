package store

import (
	"context"
	"database/sql"

	"github.com/temirlankasen/peerturn/internal/fault"
)

// RecheckParked revalidates locally retained futures after a gap is filled. Custody
// ACK may already have removed them from the relay, so it uses the original local
// ciphertext, never fresh encryption or an automatic native resubmit.
// Trusted keys/transport context and a side-effect-free receipt builder come from r.
func (s *Store) RecheckParked(ctx context.Context, r InboxRequest) ([]Custody, error) {
	type parked struct {
		id     string
		packet []byte
		seq    uint64
	}
	var pending []parked
	err := s.write(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id,packet,mailbox_seq FROM inbox WHERE grant_id=? AND state='parked' AND mailbox=? ORDER BY sender_seq LIMIT ?`, r.GrantID, r.Mailbox, 64)
		if err != nil {
			return failure(StorageUnavailable)
		}
		for rows.Next() {
			var p parked
			if err = rows.Scan(&p.id, &p.packet, &p.seq); err != nil {
				break
			}
			pending = append(pending, p)
		}
		iterErr := rows.Err()
		closeErr := rows.Close()
		if err != nil || iterErr != nil || closeErr != nil {
			return failure(StorageUnavailable)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var results []Custody
	for _, p := range pending {
		r.Packet, err = s.unseal("inbox_packet", p.id, p.packet)
		if err != nil {
			return results, err
		}
		r.MailboxSeq = p.seq
		var result Custody
		result, err = s.inboxCommit(ctx, r, true)
		if err != nil {
			return results, err
		}
		results = append(results, result)
		if result.Stage == string(fault.SequenceGap) {
			break
		}
	}
	return results, nil
}
