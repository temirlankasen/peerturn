package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/temirlankasen/peerturn/internal/fault"
)

func (s *Store) rejectedRetry(ctx context.Context, tx *sql.Tx, r InboxRequest, digest string, result *Custody) (bool, error) {
	var oldID, oldDigest, state string
	err := tx.QueryRowContext(ctx, `SELECT message_id,digest,state FROM dedup WHERE mailbox=? AND mailbox_seq=?`, r.Mailbox, r.MailboxSeq).Scan(&oldID, &oldDigest, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, failure(StorageUnavailable)
	}
	if !strings.HasPrefix(state, "rejected/") {
		return false, nil
	}
	if oldID != result.MessageID || oldDigest != digest {
		return false, failure(fault.Conflict)
	}
	parts := strings.SplitN(state, "/", 3)
	if len(parts) != 3 || parts[2] != r.GrantID {
		return false, failure(fault.Conflict)
	}
	result.Duplicate, result.Stage, result.Reason = true, "rejected", fault.Code(parts[1])
	result.Frontier, err = ackFrontier(ctx, tx, r.Mailbox)
	return true, err
}

func (s *Store) compactedCustody(ctx context.Context, tx *sql.Tx, r InboxRequest, result *Custody) (bool, error) {
	frontier, err := ackFrontier(ctx, tx, r.Mailbox)
	if err != nil || r.MailboxSeq > frontier {
		return false, err
	}
	var retained int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM dedup WHERE mailbox=? AND mailbox_seq=?`, r.Mailbox, r.MailboxSeq).Scan(&retained); err != nil {
		return false, failure(StorageUnavailable)
	}
	if retained != 0 {
		return false, nil
	}
	// The compacted frontier proves transport custody only. It cannot recover
	// a removed per-message decision or authorize another admission/native effect.
	result.Stage, result.Duplicate, result.Frontier = "custody_committed", true, frontier
	return true, nil
}
