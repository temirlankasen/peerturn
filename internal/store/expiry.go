package store

import (
	"context"
	"database/sql"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// Expire minimizes local content at TTL and collects finished records after their
// replay horizon. Unknown native outcomes retain their attempt IDs and failure
// fences, but never require keeping a decryptable copy of the peer's message.
// Audit retention uses the store clock independently of the supplied TTL clock.
func (s *Store) Expire(ctx context.Context, now int64) error {
	if now <= 0 || now > protocol.MaxSafeInteger {
		return failure(fault.InvalidEncoding)
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		if err := s.expireInbox(ctx, tx, now); err != nil {
			return err
		}
		if err := s.step("expiry_tombstones"); err != nil {
			return err
		}
		if err := s.expireOutbox(ctx, tx, now); err != nil {
			return err
		}
		if err := collectReplay(ctx, tx, now); err != nil {
			return err
		}
		if err := collectFinished(ctx, tx, now); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM audit WHERE at<?`, s.clock().Unix()-30*24*60*60)
		return databaseError(err)
	})
}

func (s *Store) expireInbox(ctx context.Context, tx *sql.Tx, now int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT id,grant_id,state FROM inbox WHERE expires_at<=? AND (bytes>0 OR state IN ('pending','offered','parked','quarantined','handled'))`, now)
	if err != nil {
		return failure(StorageUnavailable)
	}
	type expired struct{ id, grant, state string }
	var items []expired
	for rows.Next() {
		var item expired
		if err = rows.Scan(&item.id, &item.grant, &item.state); err != nil {
			break
		}
		items = append(items, item)
	}
	iterErr, closeErr := rows.Err(), rows.Close()
	if err != nil || iterErr != nil || closeErr != nil {
		return failure(StorageUnavailable)
	}
	for _, item := range items {
		state := item.state
		switch state {
		case "pending", "offered", "parked", "quarantined", "handled":
			state = "expired"
			if err = s.recordReceipt(ctx, tx, InboundReceipt, item.grant, protocol.ReceiptPayload{MessageID: item.id, AttemptID: item.id, Stage: state, EvidenceKind: "local_expiry", At: now}); err != nil {
				return err
			}
		}
		body, sealErr := s.seal("inbox", item.id, nil)
		if sealErr != nil {
			return sealErr
		}
		packet, sealErr := s.seal("inbox_packet", item.id, nil)
		if sealErr != nil {
			return sealErr
		}
		if _, err = tx.ExecContext(ctx, `UPDATE inbox SET state=?,body=?,packet=?,bytes=0 WHERE id=?`, state, body, packet, item.id); err != nil {
			return failure(StorageUnavailable)
		}
		if _, err = tx.ExecContext(ctx, `UPDATE dedup SET state='tombstone' WHERE message_id=? AND channel IS NOT NULL`, item.id); err != nil {
			return failure(StorageUnavailable)
		}
	}
	return nil
}

func (s *Store) expireOutbox(ctx context.Context, tx *sql.Tx, now int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT id,grant_id FROM outbox WHERE expires_at<=? AND stage='sender_stored'`, now)
	if err != nil {
		return failure(StorageUnavailable)
	}
	type expired struct{ id, grant string }
	var items []expired
	for rows.Next() {
		var item expired
		if err = rows.Scan(&item.id, &item.grant); err != nil {
			break
		}
		items = append(items, item)
	}
	iterErr, closeErr := rows.Err(), rows.Close()
	if err != nil || iterErr != nil || closeErr != nil {
		return failure(StorageUnavailable)
	}
	for _, item := range items {
		if err = s.recordReceipt(ctx, tx, OutboundReceipt, item.grant, protocol.ReceiptPayload{MessageID: item.id, AttemptID: item.id, Stage: "expired", EvidenceKind: "local_expiry", At: now}); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE outbox SET packet=X'',bytes=0 WHERE expires_at<=?`, now)
	return databaseError(err)
}
