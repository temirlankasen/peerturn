package store

import (
	"context"
	"database/sql"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// RecoverAskRuns discards every incomplete run after supervision is lost,
// including legacy in_doubt runs. The paid attempt is terminal, never replayed;
// losing an uncommitted fork answer does not keep its parent/conversation busy.
func (s *Store) RecoverAskRuns(ctx context.Context, now int64) error {
	if now <= 0 || now > protocol.MaxSafeInteger {
		return failure(fault.InvalidEncoding)
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT run_id FROM responder_runs WHERE state NOT IN ('reply_committed','failed','cancelled')`)
		if err != nil {
			return failure(StorageUnavailable)
		}
		var ids []string
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				break
			}
			ids = append(ids, id)
		}
		iterErr, closeErr := rows.Err(), rows.Close()
		if err != nil || iterErr != nil || closeErr != nil {
			return failure(StorageUnavailable)
		}
		for _, id := range ids {
			a, loadErr := s.loadAsk(ctx, tx, id)
			if loadErr != nil {
				return loadErr
			}
			r := protocol.ReceiptPayload{MessageID: a.MessageID, AttemptID: a.AttemptID, Stage: "responder_failed",
				EvidenceKind: "responder_recovery", Outcome: "responder_failed", ReasonCode: "runtime_error", At: now}
			var stage string
			if err = tx.QueryRowContext(ctx, `SELECT stage FROM receipts WHERE direction='inbound' AND grant_id=? AND message_id=?`, a.grantID, a.MessageID).Scan(&stage); err != nil {
				return lookupError(err)
			}
			if stage == "in_doubt" {
				// Supervision loss proves no answer may be forwarded. Reconcile
				// the old ambiguity without claiming the native run never began.
				r.EvidenceKind = "native_reconciliation"
				if err = s.reconcileReceipt(ctx, tx, InboundReceipt, a.grantID, r); err != nil {
					return err
				}
			}
			if err = s.setAskStage(ctx, tx, a, "failed", r, nil); err != nil {
				return err
			}
		}
		return nil
	})
}
