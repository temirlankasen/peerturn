package store

import (
	"bytes"
	"errors"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
)

func terminalAskFork(t *testing.T, stage string) (*Store, *testFixture, AskReservation, []byte) {
	t.Helper()
	if stage == "reply" {
		s, f, r := redactedAsk(t)
		if _, err := s.CommitAskReply(t.Context(), r.RunID, askReplyRequest(t, s, f), askEvidence(r, "reply")); err != nil {
			t.Fatal(err)
		}
		return s, f, r, []byte("private-child-native-id")
	}
	s, f, r := askFixture(t)
	reserveAsk(t, s, r)
	native := []byte("unresolved-native-child")
	if err := s.RememberAskFork(t.Context(), r.RunID, native, r.Now); err != nil {
		t.Fatal(err)
	}
	if stage == "recovered" {
		if err := s.RecoverAskRuns(t.Context(), r.Now+1); err != nil {
			t.Fatal(err)
		}
	} else if err := s.AdvanceAsk(t.Context(), r.RunID, askEvidence(r, stage), nil); err != nil {
		t.Fatal(err)
	}
	return s, f, r, native
}

func TestGCRetainsUnresolvedAskForkCleanupMetadata(t *testing.T) {
	for _, stage := range []string{"responder_failed", "cancelled", "reply", "recovered"} {
		t.Run(stage, func(t *testing.T) {
			s, f, r, native := terminalAskFork(t, stage)
			if err := s.Expire(t.Context(), retainUntil(f.header.ExpiresAt, testNow)+1); err != nil {
				t.Fatal(err)
			}
			forks, err := s.ListAskForks(t.Context())
			if err != nil || len(forks) != 1 || forks[0].RunID != r.RunID || !bytes.Equal(forks[0].NativeID, native) || !bytes.Equal(forks[0].Snapshot, r.Snapshot) {
				t.Fatalf("GC lost unresolved cleanup metadata: %+v %v", forks, err)
			}
			var runs, attempts, inbox int
			err = s.db.QueryRowContext(t.Context(), `SELECT (SELECT COUNT(*) FROM responder_runs WHERE run_id=?),(SELECT COUNT(*) FROM delivery_attempts WHERE id=?),(SELECT COUNT(*) FROM inbox WHERE id=?)`, r.RunID, r.AttemptID, r.MessageID).Scan(&runs, &attempts, &inbox)
			if err != nil || runs != 1 || attempts != 1 || inbox != 1 {
				t.Fatalf("GC removed cleanup dependencies: runs=%d attempts=%d inbox=%d err=%v", runs, attempts, inbox, err)
			}
			item, err := s.Inbox(t.Context(), r.MessageID)
			if err != nil || len(item.Body) != 0 {
				t.Fatalf("cleanup retention kept the expired peer body: %+v %v", item, err)
			}
		})
	}
}

func TestGCCollectsAskForkAfterExactCleanupConfirmation(t *testing.T) {
	for _, stage := range []string{"responder_failed", "cancelled", "reply", "recovered"} {
		t.Run(stage, func(t *testing.T) {
			s, f, r, native := terminalAskFork(t, stage)
			horizon := retainUntil(f.header.ExpiresAt, testNow) + 1
			if err := s.Expire(t.Context(), horizon); err != nil {
				t.Fatal(err)
			}
			if err := s.ForgetAskFork(t.Context(), r.RunID, []byte("another-child")); !fault.IsCode(err, fault.Conflict) {
				t.Fatalf("wrong child cleanup confirmed: %v", err)
			}
			if err := s.ForgetAskFork(t.Context(), r.RunID, native); err != nil {
				t.Fatal(err)
			}
			if err := s.Expire(t.Context(), horizon+1); err != nil {
				t.Fatal(err)
			}
			var runs, attempts, inbox int
			err := s.db.QueryRowContext(t.Context(), `SELECT (SELECT COUNT(*) FROM responder_runs WHERE run_id=?),(SELECT COUNT(*) FROM delivery_attempts WHERE id=?),(SELECT COUNT(*) FROM inbox WHERE id=?)`, r.RunID, r.AttemptID, r.MessageID).Scan(&runs, &attempts, &inbox)
			if err != nil || runs != 0 || attempts != 0 || inbox != 0 {
				t.Fatalf("confirmed cleanup prevented normal GC: runs=%d attempts=%d inbox=%d err=%v", runs, attempts, inbox, err)
			}
			if forks, err := s.ListAskForks(t.Context()); err != nil || len(forks) != 0 {
				t.Fatalf("cleaned fork remains pending: %+v %v", forks, err)
			}
		})
	}
}

func TestAskForkCleanupConfirmationPreservesProgressAndDurability(t *testing.T) {
	for _, state := range []string{"reserved", "running", "failed"} {
		t.Run(state, func(t *testing.T) {
			s, _, r := askFixture(t)
			reserveAsk(t, s, r)
			native := []byte("exact-cleaned-child")
			if err := s.RememberAskFork(t.Context(), r.RunID, native, r.Now); err != nil {
				t.Fatal(err)
			}
			switch state {
			case "running":
				if err := s.AuthorizeAsk(t.Context(), r.RunID, r.Now); err != nil {
					t.Fatal(err)
				}
				if err := s.AdvanceAsk(t.Context(), r.RunID, askEvidence(r, "responder_started"), native); err != nil {
					t.Fatal(err)
				}
			case "failed":
				if err := s.AdvanceAsk(t.Context(), r.RunID, askEvidence(r, "responder_failed"), nil); err != nil {
					t.Fatal(err)
				}
			}
			before, err := s.Receipt(t.Context(), r.MessageID)
			if err != nil {
				t.Fatal(err)
			}
			injected := errors.New("cleanup confirmation durability unavailable")
			s.abort = func(step string) error {
				if step == "before_commit" {
					return injected
				}
				return nil
			}
			if err = s.ForgetAskFork(t.Context(), r.RunID, native); !errors.Is(err, injected) {
				t.Fatalf("cleanup confirmation: %v", err)
			}
			s.abort = nil
			if forks, readErr := s.ListAskForks(t.Context()); readErr != nil || len(forks) != 1 || !bytes.Equal(forks[0].NativeID, native) {
				t.Fatalf("uncommitted cleanup discarded identity: %+v %v", forks, readErr)
			}
			// Authority loss must not prevent finishing already-proven cleanup.
			if _, err = s.db.ExecContext(t.Context(), `UPDATE grants SET revoked=1`); err != nil {
				t.Fatal(err)
			}
			if err = s.ForgetAskFork(t.Context(), r.RunID, native); err != nil {
				t.Fatal(err)
			}
			if after, readErr := s.Receipt(t.Context(), r.MessageID); readErr != nil || after != before {
				t.Fatalf("cleanup confirmation changed delivery evidence: %+v %v", after, readErr)
			}
			var runState string
			var forgotten bool
			if err = s.db.QueryRowContext(t.Context(), `SELECT state,fork_native_id IS NULL FROM responder_runs WHERE run_id=?`, r.RunID).Scan(&runState, &forgotten); err != nil || runState != state || !forgotten {
				t.Fatalf("cleanup changed run progress or retained identity: %s %v %v", runState, forgotten, err)
			}
			if err = s.ForgetAskFork(t.Context(), r.RunID, native); !fault.IsCode(err, fault.Conflict) {
				t.Fatalf("forgotten identity treated as an exact match: %v", err)
			}
		})
	}
}
