package store

import (
	"context"
	"database/sql"
	"errors"
)

func replayFloor(ctx context.Context, tx *sql.Tx, channel string) (uint64, error) {
	var floor uint64
	err := tx.QueryRowContext(ctx, `SELECT sender_seq FROM dedup WHERE key=? AND state='replay_floor'`, "replay_floor/"+channel).Scan(&floor)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return floor, databaseError(err)
}

func collectReplay(ctx context.Context, tx *sql.Tx, now int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT channel FROM dedup WHERE channel IS NOT NULL`)
	if err != nil {
		return failure(StorageUnavailable)
	}
	var channels []string
	for rows.Next() {
		var channel string
		if err = rows.Scan(&channel); err != nil {
			break
		}
		channels = append(channels, channel)
	}
	iterErr, closeErr := rows.Err(), rows.Close()
	if err != nil || iterErr != nil || closeErr != nil {
		return failure(StorageUnavailable)
	}
	for _, channel := range channels {
		if err = compactChannel(ctx, tx, channel, now); err != nil {
			return err
		}
	}
	// A durable contiguous custody frontier subsumes individual relay slots;
	// sender replay evidence remains separate and never comes from relay metadata.
	_, err = tx.ExecContext(ctx, `DELETE FROM dedup WHERE mailbox IS NOT NULL AND mailbox_seq<=(SELECT sender_seq FROM dedup f WHERE f.key='frontier/'||dedup.mailbox AND f.state='frontier')`)
	return databaseError(err)
}

func compactChannel(ctx context.Context, tx *sql.Tx, channel string, now int64) error {
	floor, err := replayFloor(ctx, tx, channel)
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT sender_seq,state FROM dedup WHERE channel=? AND sender_seq>? ORDER BY sender_seq`, channel, floor)
	if err != nil {
		return failure(StorageUnavailable)
	}
	for rows.Next() {
		var seq uint64
		var state string
		if err = rows.Scan(&seq, &state); err != nil {
			break
		}
		if seq != floor+1 || state == "parked" {
			break
		}
		floor = seq
	}
	iterErr, closeErr := rows.Err(), rows.Close()
	if err != nil || iterErr != nil || closeErr != nil {
		return failure(StorageUnavailable)
	}
	if floor != 0 {
		if _, err = tx.ExecContext(ctx, `INSERT INTO dedup(key,message_id,digest,sender_seq,state,retain_until) VALUES(?,'','',?,'replay_floor',0) ON CONFLICT(key) DO UPDATE SET sender_seq=excluded.sender_seq`, "replay_floor/"+channel, floor); err != nil {
			return failure(StorageUnavailable)
		}
	}
	// Sparse consumed tombstones above a missing sequence remain until that gap
	// closes. Deleting them by age alone would forget already consumed futures.
	_, err = tx.ExecContext(ctx, `DELETE FROM dedup WHERE channel=? AND sender_seq<=? AND state!='parked' AND retain_until<=?`, channel, floor, now)
	return databaseError(err)
}

func collectFinished(ctx context.Context, tx *sql.Tx, now int64) error {
	// Preserve ambiguous attempts, active responders and unresolved native cleanup
	// metadata. The inbox bodies have already been minimized independently.
	rows, err := tx.QueryContext(ctx, `SELECT i.id,i.grant_id,i.channel FROM inbox i
 WHERE MAX(i.expires_at+?,i.created_at+?)<=? AND i.state NOT IN ('pending','offered','parked','intent','authorized','in_doubt')
 AND NOT EXISTS(SELECT 1 FROM delivery_attempts a WHERE a.message_id=i.id AND a.state IN ('intent','authorized','in_doubt'))
 AND NOT EXISTS(SELECT 1 FROM responder_runs r WHERE r.message_id=i.id AND (r.state NOT IN ('completed','reply_committed','failed','cancelled') OR length(r.fork_native_id)>0))`, replayRetention, 2*replayRetention, now)
	if err != nil {
		return failure(StorageUnavailable)
	}
	type finished struct{ id, grant, channel string }
	var items []finished
	for rows.Next() {
		var item finished
		if err = rows.Scan(&item.id, &item.grant, &item.channel); err != nil {
			break
		}
		items = append(items, item)
	}
	iterErr, closeErr := rows.Err(), rows.Close()
	if err != nil || iterErr != nil || closeErr != nil {
		return failure(StorageUnavailable)
	}
	for _, item := range items {
		if _, err = tx.ExecContext(ctx, `DELETE FROM responder_runs WHERE message_id=?`, item.id); err != nil {
			return failure(StorageUnavailable)
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM delivery_attempts WHERE message_id=?`, item.id); err != nil {
			return failure(StorageUnavailable)
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM receipts WHERE direction='inbound' AND grant_id=? AND channel=? AND message_id=?`, item.grant, item.channel, item.id); err != nil {
			return failure(StorageUnavailable)
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM inbox WHERE id=?`, item.id); err != nil {
			return failure(StorageUnavailable)
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM receipts WHERE direction='outbound' AND EXISTS(SELECT 1 FROM outbox o WHERE o.id=receipts.message_id AND o.grant_id=receipts.grant_id AND o.channel=receipts.channel AND MAX(o.expires_at+?,o.created_at+?)<=? AND o.stage NOT IN ('sender_stored','in_doubt'))`, replayRetention, 2*replayRetention, now); err != nil {
		return failure(StorageUnavailable)
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM outbox WHERE MAX(expires_at+?,created_at+?)<=? AND stage NOT IN ('sender_stored','in_doubt')`, replayRetention, 2*replayRetention, now)
	return databaseError(err)
}
