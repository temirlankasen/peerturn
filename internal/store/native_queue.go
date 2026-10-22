package store

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// NativeQueue retains exact queue IDs returned by the original native submission.
type NativeQueue struct {
	MessageID string
	ItemIDs   []string
}

// NativeQueues is also available after binding loss: local revoke must still try
// the exact original thread and daemon, without choosing a replacement session.
func (s *Store) NativeQueues(ctx context.Context, grantID string) ([]NativeQueue, error) {
	if !protocol.ValidToken(grantID) {
		return nil, failure(fault.InvalidEncoding)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT i.id,a.id,a.native_result FROM inbox i JOIN delivery_attempts a ON a.message_id=i.id WHERE i.grant_id=? AND a.state IN ('adapter_accepted','in_doubt') ORDER BY i.id`, grantID)
	if err != nil {
		return nil, failure(StorageUnavailable)
	}
	type stored struct {
		message, attempt string
		raw              []byte
	}
	var records []stored
	for rows.Next() {
		var r stored
		if err = rows.Scan(&r.message, &r.attempt, &r.raw); err != nil {
			break
		}
		records = append(records, r)
	}
	iterErr, closeErr := rows.Err(), rows.Close()
	if err != nil || iterErr != nil || closeErr != nil {
		return nil, failure(StorageUnavailable)
	}
	var queues []NativeQueue
	for _, r := range records {
		if len(r.raw) == 0 {
			continue
		}
		raw, openErr := s.unseal("delivery_attempts", r.attempt, r.raw)
		if openErr != nil {
			return nil, openErr
		}
		q := NativeQueue{MessageID: r.message}
		if err = json.Unmarshal(raw, &struct {
			ItemIDs *[]string `json:"item_ids"`
		}{&q.ItemIDs}); err != nil {
			return nil, failure(InvalidState)
		}
		if len(q.ItemIDs) > 0 {
			queues = append(queues, q)
		}
	}
	return queues, nil
}

// NativeDequeued records positive native deletion evidence before local revoke.
func (s *Store) NativeDequeued(ctx context.Context, id string) error {
	if !protocol.ValidID(id) {
		return failure(fault.InvalidEncoding)
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE delivery_attempts SET state='dequeued' WHERE message_id=? AND state IN ('adapter_accepted','in_doubt')`, id); err != nil {
			return failure(StorageUnavailable)
		}
		_, err := tx.ExecContext(ctx, `UPDATE inbox SET state='dequeued' WHERE id=? AND state IN ('adapter_accepted','in_doubt')`, id)
		return databaseError(err)
	})
}

// NativePeerStarted reports the failure fence even after a terminal revoke.
// A hook is start evidence only, never proof that the peer body was delivered.
func (s *Store) NativePeerStarted(ctx context.Context, id string, now int64) (bool, error) {
	if !protocol.ValidID(id) || now <= 0 || now > protocol.MaxSafeInteger {
		return false, failure(fault.InvalidEncoding)
	}
	breach := false
	err := s.write(ctx, func(tx *sql.Tx) error {
		var grantID, attempt string
		var revoked bool
		if err := tx.QueryRowContext(ctx, `SELECT i.grant_id,g.revoked,a.id FROM inbox i JOIN grants g ON g.id=i.grant_id JOIN delivery_attempts a ON a.message_id=i.id WHERE i.id=?`, id).Scan(&grantID, &revoked, &attempt); err != nil {
			return lookupError(err)
		}
		if !revoked {
			return nil
		}
		breach = true
		r := protocol.ReceiptPayload{MessageID: id, AttemptID: attempt, Stage: "in_doubt", EvidenceKind: "revoked_queue_started", Outcome: "revoked_started", ReasonCode: "revoked", At: now}
		if err := s.recordReceipt(ctx, tx, InboundReceipt, grantID, r); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE delivery_attempts SET state='in_doubt' WHERE id=?`, attempt); err != nil {
			return failure(StorageUnavailable)
		}
		return s.audit(ctx, tx, now, "revoked_queue_started", grantID, 0, id, "", "revoked_started")
	})
	return breach, err
}
