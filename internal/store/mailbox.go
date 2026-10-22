package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"github.com/temirlankasen/peerturn/internal/fault"
)

// MaxMailboxLookahead bounds durable custody gaps independently of sender gaps.
const MaxMailboxLookahead uint64 = 4096

// MaxMailboxRecords bounds recent slot conflict evidence plus sparse lookahead.
// Older contiguous custody is represented by the frontier alone.
const MaxMailboxRecords = 2 * MaxMailboxLookahead

func (s *Store) commitMailbox(ctx context.Context, tx *sql.Tx, r InboxRequest, id, digest string, expiry int64) (uint64, error) {
	frontier, err := ackFrontier(ctx, tx, r.Mailbox)
	if err != nil {
		return 0, err
	}
	if r.MailboxSeq > frontier && r.MailboxSeq-frontier > MaxMailboxLookahead {
		return 0, failure(Backpressure)
	}
	if r.MailboxSeq <= frontier && frontier-r.MailboxSeq >= MaxMailboxLookahead {
		return frontier, nil
	}
	key := "mailbox/" + r.Mailbox + "/" + strconv.FormatUint(r.MailboxSeq, 10)
	var oldID, oldDigest string
	err = tx.QueryRowContext(ctx, `SELECT message_id,digest FROM dedup WHERE key=?`, key).Scan(&oldID, &oldDigest)
	if err == nil {
		if oldID != id || oldDigest != digest {
			return 0, failure(fault.Conflict)
		}
	} else {
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, failure(StorageUnavailable)
		}
		decision := r.decision
		if decision == "" {
			decision = "custody"
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO dedup(key,message_id,digest,mailbox,mailbox_seq,state,retain_until) VALUES(?,?,?,?,?,?,?)`, key, id, digest, r.Mailbox, r.MailboxSeq, decision, retainUntil(expiry, r.Open.Now))
		if err != nil {
			return 0, failure(StorageUnavailable)
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT mailbox_seq FROM dedup WHERE mailbox=? AND mailbox_seq>? ORDER BY mailbox_seq`, r.Mailbox, frontier)
	if err != nil {
		return 0, failure(StorageUnavailable)
	}
	for rows.Next() {
		var seq uint64
		if err = rows.Scan(&seq); err != nil {
			break
		}
		if seq != frontier+1 {
			break
		}
		frontier = seq
	}
	iterErr := rows.Err()
	closeErr := rows.Close()
	if err != nil || iterErr != nil || closeErr != nil {
		return 0, failure(StorageUnavailable)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO dedup(key,message_id,digest,sender_seq,state,retain_until) VALUES(?,'','',?,'frontier',0) ON CONFLICT(key) DO UPDATE SET sender_seq=excluded.sender_seq`, "frontier/"+r.Mailbox, frontier)
	if err != nil {
		return 0, failure(StorageUnavailable)
	}
	if frontier >= MaxMailboxLookahead {
		_, err = tx.ExecContext(ctx, `DELETE FROM dedup WHERE mailbox=? AND mailbox_seq<=?`, r.Mailbox, frontier-MaxMailboxLookahead)
	}
	return frontier, databaseError(err)
}

func ackFrontier(ctx context.Context, tx *sql.Tx, mailbox string) (uint64, error) {
	var frontier uint64
	err := tx.QueryRowContext(ctx, `SELECT sender_seq FROM dedup WHERE key=?`, "frontier/"+mailbox).Scan(&frontier)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return frontier, databaseError(err)
}

// AckFrontier reports the highest contiguous committed custody sequence.
func (s *Store) AckFrontier(ctx context.Context, mailbox string) (uint64, error) {
	var frontier uint64
	err := s.write(ctx, func(tx *sql.Tx) error { var err error; frontier, err = ackFrontier(ctx, tx, mailbox); return err })
	if err != nil {
		return 0, err
	}
	return frontier, nil
}
