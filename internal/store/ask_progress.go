package store

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// AdvanceAsk records trusted supervisor evidence, never a shell-exit inference.
// Native IDs stay sealed locally; peer notification packets are prepared by the
// daemon with its signing keys, outside the child process. Terminal evidence is
// committed before its notification, whose failure never rolls back settlement.
func (s *Store) AdvanceAsk(ctx context.Context, runID string, r protocol.ReceiptPayload, nativeID []byte, notification ...NativeNotification) error {
	if !protocol.ValidID(runID) || len(nativeID) > 4096 || len(notification) > 1 {
		return failure(fault.InvalidEncoding)
	}
	check := r
	check.Stage = "receiver_stored"
	if err := check.Validate(); err != nil {
		return err
	}
	terminal := askTerminalState(r.Stage)
	err := s.write(ctx, func(tx *sql.Tx) error {
		a, err := s.loadAsk(ctx, tx, runID)
		if err != nil {
			return err
		}
		if r.MessageID != a.MessageID || r.AttemptID != a.AttemptID {
			return failure(fault.Conflict)
		}
		if a.state == "reply_committed" || a.state == "failed" || a.state == "cancelled" || a.state == "in_doubt" || a.state == "cancel_requested" {
			if terminal != "" && a.state == terminal && a.attemptState == r.Stage && len(nativeID) == 0 {
				return s.matchAskReceipt(ctx, tx, a, r)
			}
			if a.state != "cancel_requested" || r.Stage != "cancelled" {
				return failure(fault.Conflict)
			}
		}
		state := ""
		switch r.Stage {
		case "responder_started":
			if a.state != "reserved" || a.attemptState != "authorized" || len(nativeID) == 0 {
				return failure(InvalidState)
			}
			state = "running"
		case "model_input":
			if a.state != "running" || a.attemptState != "responder_started" {
				return failure(InvalidState)
			}
			state = "running"
		case "turn_completed":
			if a.state != "running" || a.attemptState != "model_input" {
				return failure(InvalidState)
			}
			state = "completed"
		case "reply_redacted":
			if a.state != "completed" || a.attemptState != "turn_completed" {
				return failure(InvalidState)
			}
			state = "redacting"
		case "cancelled":
			state = "cancelled"
		case "responder_failed":
			state = "failed"
		case "in_doubt":
			state = "in_doubt"
		default:
			return failure(InvalidState)
		}
		if state != "cancelled" && state != "failed" && state != "in_doubt" {
			if _, err = s.authorizeAsk(ctx, tx, a, r.At); err != nil {
				return err
			}
		}
		if len(nativeID) != 0 {
			if err = s.rememberAskFork(ctx, tx, runID, nativeID); err != nil {
				return err
			}
		}
		if terminal != "" {
			return s.setAskStage(ctx, tx, a, state, r, nil)
		}
		return s.setAskStage(ctx, tx, a, state, r, notification)
	})
	if err != nil || terminal == "" || len(notification) == 0 {
		return err
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		a, loadErr := s.loadAsk(ctx, tx, runID)
		if loadErr != nil {
			return loadErr
		}
		if a.state != terminal || a.attemptState != r.Stage {
			return failure(fault.Conflict)
		}
		if matchErr := s.matchAskReceipt(ctx, tx, a, r); matchErr != nil {
			return matchErr
		}
		return s.nativeNotification(ctx, tx, a.grantID, r, notification[0])
	})
}

func askTerminalState(stage string) string {
	switch stage {
	case "responder_failed":
		return "failed"
	case "cancelled", "in_doubt":
		return stage
	default:
		return ""
	}
}

func (s *Store) matchAskReceipt(ctx context.Context, tx *sql.Tx, a askRecord, r protocol.ReceiptPayload) error {
	var sealed []byte
	if err := tx.QueryRowContext(ctx, `SELECT evidence FROM receipts WHERE direction='inbound' AND grant_id=? AND message_id=?`, a.grantID, a.MessageID).Scan(&sealed); err != nil {
		return lookupError(err)
	}
	channel, err := receiptChannel(ctx, tx, InboundReceipt, a.grantID, a.MessageID)
	if err != nil {
		return err
	}
	raw, err := s.unseal("receipts", receiptKey(InboundReceipt, a.grantID, channel, a.MessageID), sealed)
	if err != nil {
		return err
	}
	var previous protocol.ReceiptPayload
	if json.Unmarshal(raw, &previous) != nil {
		return failure(InvalidState)
	}
	if previous != r {
		return failure(fault.Conflict)
	}
	return nil
}

func (s *Store) setAskStage(ctx context.Context, tx *sql.Tx, a askRecord, state string, r protocol.ReceiptPayload, notification []NativeNotification) error {
	for _, q := range []struct {
		query string
		args  []any
	}{
		{`UPDATE responder_runs SET state=? WHERE run_id=?`, []any{state, a.RunID}},
		{`UPDATE delivery_attempts SET state=? WHERE id=?`, []any{r.Stage, a.AttemptID}},
		{`UPDATE inbox SET state=? WHERE id=?`, []any{r.Stage, a.MessageID}},
	} {
		if _, err := tx.ExecContext(ctx, q.query, q.args...); err != nil {
			return failure(StorageUnavailable)
		}
	}
	if err := s.recordReceipt(ctx, tx, InboundReceipt, a.grantID, r); err != nil {
		return err
	}
	if len(notification) == 1 {
		if err := s.nativeNotification(ctx, tx, a.grantID, r, notification[0]); err != nil {
			return err
		}
	}
	return s.audit(ctx, tx, r.At, "ask_progress", a.grantID, a.epoch, a.MessageID, "", r.Stage)
}
