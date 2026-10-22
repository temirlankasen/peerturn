package store

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/fault"
)

func fillAskControls(t *testing.T, s *Store, count, size int) {
	t.Helper()
	_, err := s.db.ExecContext(t.Context(), `WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<?) INSERT INTO outbox(id,grant_id,digest,packet,lane,stage,expires_at,bytes,created_at) SELECT printf('%032x',x+65536),'grant_reverse','capacity_fixture',X'01','control','sender_stored',?,?,? FROM n`, count, testNow+300, size, testNow)
	if err != nil {
		t.Fatal(err)
	}
}

func nextAsk(t *testing.T, s *Store, f *testFixture, r AskReservation) AskReservation {
	t.Helper()
	commitOne(t, s, f, 2)
	r.RunID, r.MessageID, r.AttemptID = strings.Repeat("3", 32), fmt.Sprintf("%032x", 2), strings.Repeat("4", 32)
	if _, err := s.DeliveryIntent(t.Context(), r.MessageID, r.AttemptID, testNow); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestAskTerminalSettlementSurvivesControlBackpressure(t *testing.T) {
	for _, stage := range []string{"responder_failed", "cancelled", "in_doubt"} {
		t.Run(stage, func(t *testing.T) {
			s, f, r := askFixture(t)
			installReturnGrant(t, s, f)
			reserveAsk(t, s, r)
			if err := s.AuthorizeAsk(t.Context(), r.RunID, testNow); err != nil {
				t.Fatal(err)
			}
			if err := s.AdvanceAsk(t.Context(), r.RunID, askEvidence(r, "responder_started"), []byte("child")); err != nil {
				t.Fatal(err)
			}
			receipt := askEvidence(r, stage)
			notice := nativeNoticeFixture(t, s, f, receipt)
			fillAskControls(t, s, MaxControlMessages, 1)
			if err := s.AdvanceAsk(t.Context(), r.RunID, receipt, nil, notice); !fault.IsCode(err, Backpressure) {
				t.Fatalf("notification capacity: %v", err)
			}
			want := map[string]string{"responder_failed": "failed", "cancelled": "cancelled", "in_doubt": "in_doubt"}[stage]
			var state string
			if err := s.db.QueryRowContext(t.Context(), `SELECT state FROM responder_runs WHERE run_id=?`, r.RunID).Scan(&state); err != nil || state != want {
				t.Fatalf("terminal state rolled back with notification: %s %v", state, err)
			}
			if got, err := s.Receipt(t.Context(), r.MessageID); err != nil || got != receipt {
				t.Fatalf("terminal evidence rolled back: %+v %v", got, err)
			}
			if _, err := s.db.ExecContext(t.Context(), `DELETE FROM outbox WHERE grant_id='grant_reverse'`); err != nil {
				t.Fatal(err)
			}
			if err := s.AdvanceAsk(t.Context(), r.RunID, receipt, nil, notice); err != nil {
				t.Fatalf("same terminal notification cannot retry: %v", err)
			}
			changed := receipt
			changed.ReasonCode = "runtime_error"
			if err := s.AdvanceAsk(t.Context(), r.RunID, changed, nil, notice); !fault.IsCode(err, fault.Conflict) {
				t.Fatalf("terminal evidence changed: %v", err)
			}
			if stage != "in_doubt" {
				reserveAsk(t, s, nextAsk(t, s, f, r))
			}
		})
	}
}

func TestAskRecoverySettlesIncompleteRunsAndFreesParent(t *testing.T) {
	stages := []string{"reserved", "authorized", "responder_started", "model_input", "turn_completed", "reply_redacted", "in_doubt"}
	for i, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			s, f, r := askFixture(t)
			installReturnGrant(t, s, f)
			reserveAsk(t, s, r)
			if i > 0 {
				if err := s.AuthorizeAsk(t.Context(), r.RunID, testNow); err != nil {
					t.Fatal(err)
				}
			}
			for _, current := range stages[2:max(2, i+1)] {
				if err := s.AdvanceAsk(t.Context(), r.RunID, askEvidence(r, current), []byte("child")); err != nil {
					t.Fatal(err)
				}
			}
			fillAskControls(t, s, MaxControlMessages, 1)
			if err := s.RecoverAskRuns(t.Context(), testNow+1); err != nil {
				t.Fatal(err)
			}
			var run, attempt, inbox string
			if err := s.db.QueryRowContext(t.Context(), `SELECT r.state,a.state,i.state FROM responder_runs r JOIN delivery_attempts a ON a.id=r.attempt_id JOIN inbox i ON i.id=r.message_id WHERE r.run_id=?`, r.RunID).Scan(&run, &attempt, &inbox); err != nil || run != "failed" || attempt != "responder_failed" || inbox != "responder_failed" {
				t.Fatalf("incomplete recovery: run=%s attempt=%s inbox=%s err=%v", run, attempt, inbox, err)
			}
			before, err := s.Receipt(t.Context(), r.MessageID)
			if err != nil || before.Stage != "responder_failed" || before.AttemptID != r.AttemptID {
				t.Fatalf("recovery terminal receipt: %+v %v", before, err)
			}
			if err = s.RecoverAskRuns(t.Context(), testNow+2); err != nil {
				t.Fatal(err)
			}
			if after, readErr := s.Receipt(t.Context(), r.MessageID); readErr != nil || before != after {
				t.Fatalf("repeated recovery changed evidence: %+v %v", after, readErr)
			}
			got, err := s.ReserveAsk(t.Context(), r)
			if err != nil || got.Start || got.State != "failed" {
				t.Fatalf("recovered attempt replayed: %+v %v", got, err)
			}
			if err = s.AuthorizeAsk(t.Context(), r.RunID, testNow+2); !fault.IsCode(err, fault.Conflict) {
				t.Fatalf("recovered attempt authorized: %v", err)
			}
			var replies int
			if err = s.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM outbox WHERE lane='data'`).Scan(&replies); err != nil || replies != 0 {
				t.Fatalf("recovery invented an answer: %d %v", replies, err)
			}
			if _, err = s.db.ExecContext(t.Context(), `DELETE FROM outbox WHERE grant_id='grant_reverse'`); err != nil {
				t.Fatal(err)
			}
			reserveAsk(t, s, nextAsk(t, s, f, r))
		})
	}
}

func TestResponderUncertaintyDoesNotFenceLiveAdmission(t *testing.T) {
	s, _, r := askFixture(t)
	reserveAsk(t, s, r)
	if err := s.AdvanceAsk(t.Context(), r.RunID, askEvidence(r, "in_doubt"), nil); err != nil {
		t.Fatal(err)
	}
	if closed, err := s.UncertainDeliveries(t.Context()); err != nil || closed {
		t.Fatalf("responder uncertainty fenced live: %v %v", closed, err)
	}
	if _, err := s.db.ExecContext(t.Context(), `UPDATE inbox SET route=? WHERE id=?`, envelope.Live, r.MessageID); err != nil {
		t.Fatal(err)
	}
	if closed, err := s.UncertainDeliveries(t.Context()); err != nil || !closed {
		t.Fatalf("live uncertainty lost its fence: %v %v", closed, err)
	}
}

func TestAskReservationNeedsReverseControlHeadroom(t *testing.T) {
	for _, remaining := range []int{0, 5, 6} {
		t.Run(fmt.Sprint(remaining), func(t *testing.T) {
			s, f, r := askFixture(t)
			installReturnGrant(t, s, f)
			fillAskControls(t, s, MaxControlMessages-remaining, 1)
			got, err := s.ReserveAsk(t.Context(), r)
			if remaining >= 6 {
				if err != nil || !got.Start {
					t.Fatalf("six controls should admit: %+v %v", got, err)
				}
				return
			}
			if !fault.IsCode(err, Backpressure) {
				t.Fatalf("insufficient reverse controls still spent a turn: %+v %v", got, err)
			}
			var runs, turns int
			if err = s.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM responder_runs`).Scan(&runs); err != nil || runs != 0 {
				t.Fatalf("rejected reservation persisted: %d %v", runs, err)
			}
			if err = s.db.QueryRowContext(t.Context(), `SELECT turns FROM sessions WHERE id=?`, bindingKey(r.Parent)).Scan(&turns); err != nil || turns != 0 {
				t.Fatalf("rejected reservation charged: %d %v", turns, err)
			}
		})
	}
}

func TestAskTerminalSettlementSurvivesNotificationFailures(t *testing.T) {
	for _, cause := range []string{"revoked_reverse", "invalid_packet", "second_transaction"} {
		t.Run(cause, func(t *testing.T) {
			s, f, r := askFixture(t)
			reserveAsk(t, s, r)
			receipt := askEvidence(r, "responder_failed")
			notice := nativeNoticeFixture(t, s, f, receipt)
			switch cause {
			case "revoked_reverse":
				if _, err := s.db.ExecContext(t.Context(), `UPDATE grants SET revoked=1 WHERE id='grant_reverse'`); err != nil {
					t.Fatal(err)
				}
			case "invalid_packet":
				notice.Packet = []byte("invalid")
			case "second_transaction":
				commits := 0
				s.abort = func(step string) error {
					if step == "before_commit" {
						commits++
						if commits == 2 {
							return errors.New("notification durability unavailable")
						}
					}
					return nil
				}
			}
			if err := s.AdvanceAsk(t.Context(), r.RunID, receipt, nil, notice); err == nil {
				t.Fatal("notification failure was not reported")
			}
			s.abort = nil
			var state string
			if err := s.db.QueryRowContext(t.Context(), `SELECT state FROM responder_runs WHERE run_id=?`, r.RunID).Scan(&state); err != nil || state != "failed" {
				t.Fatalf("notification failure rolled back settlement: %s %v", state, err)
			}
			if got, err := s.Receipt(t.Context(), r.MessageID); err != nil || got != receipt {
				t.Fatalf("notification failure rolled back evidence: %+v %v", got, err)
			}
		})
	}
}
