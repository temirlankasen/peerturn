package store

import (
	"context"
	"database/sql"

	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// ConsumeInform records a notice actually attached by the host. No body is offered.
func (s *Store) ConsumeInform(ctx context.Context, id, attempt string, now int64) error {
	r := protocol.ReceiptPayload{MessageID: id, AttemptID: attempt, Stage: "offered", EvidenceKind: "hook_notice", Outcome: "offered", At: now}
	return s.InformResults(ctx, []InformResult{{Receipt: r}})
}

// PendingRoute selects a bounded scope lane without starvation behind other lanes.
func (s *Store) PendingRoute(ctx context.Context, route envelope.Routing, limit int) ([]InboxEntry, error) {
	if limit <= 0 || limit > 100 {
		return nil, failure(fault.InvalidEncoding)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT i.id,i.grant_id,i.body,i.route,i.state,i.conversation_id,i.parent_id,i.budget_id,i.hop FROM inbox i JOIN grants g ON g.id=i.grant_id WHERE i.state='pending' AND i.lane='data' AND i.route=? AND i.expires_at>? AND g.revoked=0 AND g.expires_at>? AND g.epoch=i.epoch ORDER BY i.created_at,i.id LIMIT ?`, route, s.clock().Unix(), s.clock().Unix(), limit)
	if err != nil {
		return nil, failure(StorageUnavailable)
	}
	var items []InboxEntry
	for rows.Next() {
		var item InboxEntry
		if err = rows.Scan(&item.MessageID, &item.GrantID, &item.Body, &item.Routing, &item.State, &item.ConversationID, &item.ParentID, &item.BudgetID, &item.Hop); err != nil {
			break
		}
		items = append(items, item)
	}
	iterErr, closeErr := rows.Err(), rows.Close()
	if err != nil || iterErr != nil || closeErr != nil {
		return nil, failure(StorageUnavailable)
	}
	for i := range items {
		items[i].Body, err = s.unseal("inbox", items[i].MessageID, items[i].Body)
		if err != nil {
			return nil, err
		}
	}
	return items, nil
}

// PendingInformCount includes every retained unoffered no-wake notice, not one page.
func (s *Store) PendingInformCount(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM inbox i JOIN grants g ON g.id=i.grant_id WHERE i.state='pending' AND i.route=? AND i.expires_at>? AND g.revoked=0 AND g.expires_at>? AND g.epoch=i.epoch`, envelope.InboxNoWake, s.clock().Unix(), s.clock().Unix()).Scan(&count)
	return count, databaseError(err)
}

// DeliverInform records that this body was included in an inbox tool/CLI response.
// It does not claim sampling, a completed turn or an answer from the model.
func (s *Store) DeliverInform(ctx context.Context, id, attempt string, now int64) error {
	r := protocol.ReceiptPayload{MessageID: id, AttemptID: attempt, Stage: "delivered", EvidenceKind: "inbox_body_returned", Outcome: "delivered", At: now}
	return s.InformResults(ctx, []InformResult{{Receipt: r}})
}

// InformResult is a body-free observation with an optional reciprocal receipt.
type InformResult struct {
	Receipt      protocol.ReceiptPayload
	Notification *NativeNotification
}

// InformResults commits the complete bounded notice/read observation atomically.
// Preparing a notice is read-only; only successful hook stdout is acknowledged.
func (s *Store) InformResults(ctx context.Context, results []InformResult) error {
	if len(results) > 24 {
		return failure(fault.InvalidEncoding)
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		for _, result := range results {
			if err := s.informResult(ctx, tx, result); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) informResult(ctx context.Context, tx *sql.Tx, result InformResult) error {
	r := result.Receipt
	if (r.Stage != "offered" && r.Stage != "delivered") || r.Validate() != nil || (r.Outcome != "" && r.Outcome != r.Stage) {
		return failure(fault.InvalidEncoding)
	}
	var grantID, state string
	var route envelope.Routing
	var epoch uint64
	var expiry int64
	if err := tx.QueryRowContext(ctx, `SELECT grant_id,state,route,epoch,expires_at FROM inbox WHERE id=?`, r.MessageID).Scan(&grantID, &state, &route, &epoch, &expiry); err != nil {
		return lookupError(err)
	}
	if route != envelope.InboxNoWake {
		return failure(fault.ScopeDenied)
	}
	if r.At >= expiry {
		return failure(fault.Expired)
	}
	g, err := s.authorization(ctx, tx, grantID, r.At)
	if err != nil {
		return err
	}
	if err = grantEpoch(g.GrantEpoch, epoch); err != nil {
		return err
	}
	if state == "delivered" || (state == "offered" && r.Stage == "offered") {
		return nil
	}
	if state != "pending" && state != "offered" {
		return failure(InvalidState)
	}
	if state == "offered" {
		var previousAttempt string
		if err = tx.QueryRowContext(ctx, `SELECT attempt_id FROM receipts WHERE direction='inbound' AND grant_id=? AND message_id=?`, grantID, r.MessageID).Scan(&previousAttempt); err != nil {
			return lookupError(err)
		}
		if result.Notification != nil && r.AttemptID != previousAttempt {
			return failure(fault.Conflict)
		}
		r.AttemptID = previousAttempt
	}
	r.Outcome = r.Stage
	if err = s.recordReceipt(ctx, tx, InboundReceipt, grantID, r); err != nil {
		return err
	}
	if result.Notification != nil {
		if err = s.nativeNotification(ctx, tx, grantID, r, *result.Notification); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE inbox SET state=? WHERE id=?`, r.Stage, r.MessageID)
	return databaseError(err)
}

// RejectNative quarantines one failed pending item without blocking other peers.
func (s *Store) RejectNative(ctx context.Context, id, reason string, now int64) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		var grantID string
		if err := tx.QueryRowContext(ctx, `SELECT grant_id FROM inbox WHERE id=? AND state='pending'`, id).Scan(&grantID); err != nil {
			return lookupError(err)
		}
		r := protocol.ReceiptPayload{MessageID: id, AttemptID: id, Stage: "rejected", EvidenceKind: "native_item_rejected", ReasonCode: reason, At: now}
		if err := s.recordReceipt(ctx, tx, InboundReceipt, grantID, r); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE inbox SET state='rejected' WHERE id=?`, id)
		return databaseError(err)
	})
}
