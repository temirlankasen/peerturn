package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/grant"
)

const replayRetention int64 = 7 * 24 * 60 * 60

func retainUntil(expiry, now int64) int64 { return max(expiry+replayRetention, now+2*replayRetention) }

func (s *Store) openSnapshot(ctx context.Context, tx *sql.Tx, r InboxRequest, g grant.Grant) (envelope.OpenOptions, error) {
	o := r.Open
	var sealed []byte
	if err := tx.QueryRowContext(ctx, `SELECT signed,epoch FROM grants WHERE id=?`, r.GrantID).Scan(&sealed, &o.CurrentGrantEpoch); err != nil {
		return o, lookupError(err)
	}
	raw, err := s.unseal("grants", r.GrantID, sealed)
	if err != nil {
		return o, err
	}
	o.SignedGrant = string(raw)
	channel := channelKey(g.Subject, g.Issuer)
	floor, err := replayFloor(ctx, tx, channel)
	if err != nil {
		return o, err
	}
	o.Replay = envelope.ReplaySnapshot{NextSeq: floor + 1}
	rows, err := tx.QueryContext(ctx, `SELECT message_id,digest,sender_seq,state FROM dedup WHERE channel=? ORDER BY sender_seq`, channel)
	if err != nil {
		return o, failure(StorageUnavailable)
	}
	for rows.Next() {
		var rec envelope.ReplayRecord
		var state string
		if err = rows.Scan(&rec.MessageID, &rec.Digest, &rec.SenderSeq, &state); err != nil {
			break
		}
		if state == "parked" {
			o.Replay.Pending = append(o.Replay.Pending, rec)
		} else {
			o.Replay.Records = append(o.Replay.Records, rec)
			if rec.SenderSeq == o.Replay.NextSeq {
				o.Replay.NextSeq++
			}
		}
	}
	iterErr := rows.Err()
	closeErr := rows.Close()
	if err != nil || iterErr != nil || closeErr != nil {
		return o, failure(StorageUnavailable)
	}
	o.LookupConversation = func(id string) (*envelope.Conversation, error) {
		c := &envelope.Conversation{ID: id, Peer: g.Subject}
		// A conversation crosses both directions; grants pin the exact peer pair.
		err = tx.QueryRowContext(ctx, `SELECT id,budget_id,hop FROM (
 SELECT id,budget_id,hop FROM inbox WHERE channel=? AND conversation_id=? AND state!='parked' AND lane='data'
 UNION ALL
 SELECT id,budget_id,hop FROM outbox WHERE channel=? AND conversation_id=? AND lane='data'
) ORDER BY hop DESC LIMIT 1`, channel, id, channelKey(g.Issuer, g.Subject), id).Scan(&c.ParentID, &c.BudgetID, &c.Hop)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, failure(StorageUnavailable)
		}
		return c, nil
	}
	o.Budget, err = budgetSnapshot(ctx, tx, g, r.Open.Now)
	return o, err
}

func budgetSnapshot(ctx context.Context, tx *sql.Tx, g grant.Grant, now int64) (envelope.BudgetSnapshot, error) {
	var b envelope.BudgetSnapshot
	var burstStart, floor int64
	err := tx.QueryRowContext(ctx, `SELECT minute_start,messages,burst_start,burst,clock_floor FROM grants WHERE id=?`, g.GrantID).Scan(&b.MinuteStart, &b.MessagesInMinute, &burstStart, &b.BurstUsed, &floor)
	if err != nil {
		return b, lookupError(err)
	}
	if now < floor {
		return b, failure(fault.BudgetExceeded)
	}
	if now-b.MinuteStart >= 60 {
		b.MinuteStart = now
		b.MessagesInMinute = 0
	}
	// One token per six seconds with capacity five is the baseline burst bucket.
	refilled := uint64(max(0, now-burstStart) / 6)
	if refilled >= b.BurstUsed {
		b.BurstUsed = 0
	} else {
		b.BurstUsed -= refilled
	}
	err = tx.QueryRowContext(ctx, `SELECT turn_window,turns FROM sessions WHERE id=?`, bindingKey(g.Issuer)).Scan(&b.TurnWindowStart, &b.TurnsInTenMinutes)
	if err != nil {
		return b, lookupError(err)
	}
	if now-b.TurnWindowStart >= 600 {
		b.TurnWindowStart = now
		b.TurnsInTenMinutes = 0
	}
	return b, nil
}

func chargeBudget(ctx context.Context, tx *sql.Tx, g grant.Grant, now int64, model bool) error {
	b, err := budgetSnapshot(ctx, tx, g, now)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE grants SET minute_start=?,messages=?,burst_start=?,burst=?,clock_floor=? WHERE id=?`, b.MinuteStart, b.MessagesInMinute+1, now, b.BurstUsed+1, now, g.GrantID)
	if err != nil {
		return failure(StorageUnavailable)
	}
	if model {
		_, err = tx.ExecContext(ctx, `UPDATE sessions SET turn_window=?,turns=? WHERE id=?`, b.TurnWindowStart, b.TurnsInTenMinutes+1, bindingKey(g.Issuer))
	}
	return databaseError(err)
}
