package store

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func askFixture(t *testing.T) (*Store, *testFixture, AskReservation) {
	t.Helper()
	s := testStore(t)
	f := fixture(t, s)
	installReturnGrant(t, s, f)
	f.message.Kind = protocol.Ask
	commitOne(t, s, f, 1)
	r := AskReservation{RunID: strings.Repeat("1", 32), MessageID: f.message.MessageID,
		AttemptID: strings.Repeat("2", 32), Parent: f.recipient.cert.Binding,
		ParentContext: []byte("verified-native-context"), Snapshot: []byte("private-snapshot-canary"),
		Model: "provider/model", Effort: "xhigh", ProfileHash: "profile_one", BudgetID: "reservation_one", Now: testNow}
	if _, err := s.DeliveryIntent(t.Context(), r.MessageID, r.AttemptID, r.Now); err != nil {
		t.Fatal(err)
	}
	return s, f, r
}

func reserveAsk(t *testing.T, s *Store, r AskReservation) {
	t.Helper()
	got, err := s.ReserveAsk(t.Context(), r)
	if err != nil || !got.Start || got.State != "reserved" {
		t.Fatalf("reserve: %+v %v", got, err)
	}
}

func TestAskReservationIsDurableSealedAndIdempotent(t *testing.T) {
	s, _, r := askFixture(t)
	var before int
	if err := s.db.QueryRowContext(t.Context(), `SELECT turns FROM sessions WHERE id=?`, bindingKey(r.Parent)).Scan(&before); err != nil {
		t.Fatal(err)
	}
	reserveAsk(t, s, r)
	got, err := s.ReserveAsk(t.Context(), r)
	if err != nil || got.Start || got.State != "reserved" {
		t.Fatalf("duplicate reservation: %+v %v", got, err)
	}
	var after int
	var sealed []byte
	if err = s.db.QueryRowContext(t.Context(), `SELECT turns FROM sessions WHERE id=?`, bindingKey(r.Parent)).Scan(&after); err != nil || after != before+1 {
		t.Fatalf("reservation must charge exactly one turn: %d %d %v", before, after, err)
	}
	if err = s.db.QueryRowContext(t.Context(), `SELECT snapshot_handle FROM responder_runs WHERE run_id=?`, r.RunID).Scan(&sealed); err != nil || bytes.Contains(sealed, r.Snapshot) {
		t.Fatalf("snapshot not sealed: %v", err)
	}
	r.Snapshot = []byte("changed-snapshot")
	if _, err = s.ReserveAsk(t.Context(), r); !fault.IsCode(err, fault.Conflict) {
		t.Fatalf("reservation metadata changed: %v", err)
	}
}

func TestAskReservationRechecksParentAndSessionTurnLimit(t *testing.T) {
	for _, tc := range []string{"parent_context", "binding_state", "turn_limit", "expired"} {
		t.Run(tc, func(t *testing.T) {
			s, _, r := askFixture(t)
			want := InvalidState
			switch tc {
			case "parent_context":
				r.ParentContext = []byte("replacement-native-instance")
				want = fault.Conflict
			case "binding_state":
				if _, err := s.db.ExecContext(t.Context(), `UPDATE bindings SET state='paused_owner'`); err != nil {
					t.Fatal(err)
				}
			case "turn_limit":
				if _, err := s.db.ExecContext(t.Context(), `UPDATE sessions SET turn_window=?,turns=20`, testNow); err != nil {
					t.Fatal(err)
				}
				want = fault.BudgetExceeded
			case "expired":
				r.Now = testNow + 300
				want = fault.Expired
			}
			if _, err := s.ReserveAsk(t.Context(), r); !fault.IsCode(err, want) {
				t.Fatalf("reservation accepted: %v", err)
			}
			var count int
			if err := s.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM responder_runs`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("partial reservation: %d %v", count, err)
			}
		})
	}
}

func TestAskReservationBlocksConcurrentParentAndRecoveryResubmit(t *testing.T) {
	s, f, r := askFixture(t)
	reserveAsk(t, s, r)
	commitOne(t, s, f, 2)
	other := r
	other.RunID, other.MessageID, other.AttemptID = strings.Repeat("3", 32), strings.Repeat("0", 31)+"2", strings.Repeat("4", 32)
	if _, err := s.DeliveryIntent(t.Context(), other.MessageID, other.AttemptID, testNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReserveAsk(t.Context(), other); !fault.IsCode(err, fault.Conflict) {
		t.Fatalf("parallel parent accepted: %v", err)
	}
	if err := s.RecoverAskRuns(t.Context(), testNow+1); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReserveAsk(t.Context(), r)
	if err != nil || got.Start || got.State != "failed" {
		t.Fatalf("recovery restarted: %+v %v", got, err)
	}
	reserveAsk(t, s, other)
}

func TestAskReservationRollbackDoesNotChargeModelTurn(t *testing.T) {
	s, _, r := askFixture(t)
	injected := errors.New("reservation durability unavailable")
	s.abort = func(step string) error {
		if step == "before_commit" {
			return injected
		}
		return nil
	}
	if _, err := s.ReserveAsk(t.Context(), r); !errors.Is(err, injected) {
		t.Fatalf("reservation: %v", err)
	}
	s.abort = nil
	var turns, runs int
	if err := s.db.QueryRowContext(t.Context(), `SELECT turns FROM sessions WHERE id=?`, bindingKey(r.Parent)).Scan(&turns); err != nil || turns != 0 {
		t.Fatalf("partial turn charge: %d %v", turns, err)
	}
	if err := s.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM responder_runs`).Scan(&runs); err != nil || runs != 0 {
		t.Fatalf("partial run: %d %v", runs, err)
	}
	reserveAsk(t, s, r)
}
