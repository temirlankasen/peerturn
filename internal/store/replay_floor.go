package store

import (
	"context"
	"database/sql"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/identity"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// RaiseReplayFloor skips sender sequences consumed under a superseded grant.
// A relinked peer keeps its channel and counter, so packets lost with the old
// link would otherwise park every later message. The floor never moves down.
func (s *Store) RaiseReplayFloor(ctx context.Context, sender, recipient identity.Binding, floor uint64) error {
	if floor == 0 {
		return nil
	}
	if floor > protocol.MaxSafeInteger {
		return failure(fault.InvalidEncoding)
	}
	channel := channelKey(sender, recipient)
	return s.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO dedup(key,message_id,digest,sender_seq,state,retain_until) VALUES(?,'','',?,'replay_floor',0) ON CONFLICT(key) DO UPDATE SET sender_seq=max(sender_seq,excluded.sender_seq)`, "replay_floor/"+channel, floor)
		return databaseError(err)
	})
}
