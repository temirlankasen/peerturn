package store

import (
	"bytes"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
)

func TestAskForkIsRememberedBeforeStartAndSurvivesRecovery(t *testing.T) {
	s, _, r := askFixture(t)
	reserveAsk(t, s, r)
	native := []byte("preselected-native-child")
	before, err := s.Receipt(t.Context(), r.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RememberAskFork(t.Context(), r.RunID, native, r.Now); err != nil {
		t.Fatal(err)
	}
	var sealed []byte
	var runState, attemptState string
	if err = s.db.QueryRowContext(t.Context(), `SELECT r.fork_native_id,r.state,a.state FROM responder_runs r JOIN delivery_attempts a ON a.id=r.attempt_id WHERE r.run_id=?`, r.RunID).Scan(&sealed, &runState, &attemptState); err != nil || bytes.Contains(sealed, native) || runState != "reserved" || attemptState != "intent" {
		t.Fatalf("pre-spawn identity changed progress or remained plaintext: %s %s %v", runState, attemptState, err)
	}
	if after, readErr := s.Receipt(t.Context(), r.MessageID); readErr != nil || before != after {
		t.Fatalf("remembered identity claimed native acceptance: %+v %v", after, readErr)
	}
	if err = s.AuthorizeAsk(t.Context(), r.RunID, r.Now); err != nil {
		t.Fatal(err)
	}
	if err = s.RememberAskFork(t.Context(), r.RunID, native, r.Now); err != nil {
		t.Fatal(err)
	}
	if err = s.RememberAskFork(t.Context(), r.RunID, []byte("replacement-child"), r.Now); !fault.IsCode(err, fault.Conflict) {
		t.Fatalf("preselected fork identity changed: %v", err)
	}
	if err = s.RecoverAskRuns(t.Context(), r.Now+1); err != nil {
		t.Fatal(err)
	}
	forks, err := s.ListAskForks(t.Context())
	if err != nil || len(forks) != 1 || forks[0].RunID != r.RunID || !bytes.Equal(forks[0].NativeID, native) || !bytes.Equal(forks[0].Snapshot, r.Snapshot) {
		t.Fatalf("recovery lost exact cleanup metadata: %+v %v", forks, err)
	}
	if err = s.RememberAskFork(t.Context(), r.RunID, native, r.Now+2); !fault.IsCode(err, fault.Conflict) {
		t.Fatalf("terminal child identity admitted again: %v", err)
	}
}

func TestAskForkRememberingRechecksAuthorityAndRecordedIdentity(t *testing.T) {
	for _, change := range []string{"grant", "binding", "lease", "expired", "running"} {
		t.Run(change, func(t *testing.T) {
			s, _, r := askFixture(t)
			reserveAsk(t, s, r)
			now := r.Now
			var err error
			switch change {
			case "grant":
				_, err = s.db.ExecContext(t.Context(), `UPDATE grants SET revoked=1 WHERE id='grant_one'`)
			case "binding":
				_, err = s.db.ExecContext(t.Context(), `UPDATE bindings SET state='paused_owner'`)
			case "lease":
				_, err = s.db.ExecContext(t.Context(), `UPDATE grants SET lease_generation=lease_generation+1 WHERE id='grant_one'`)
			case "expired":
				now += 300
			case "running":
				if err = s.AuthorizeAsk(t.Context(), r.RunID, r.Now); err == nil {
					err = s.AdvanceAsk(t.Context(), r.RunID, askEvidence(r, "responder_started"), []byte("original-child"))
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = s.RememberAskFork(t.Context(), r.RunID, []byte("late-child"), now); err == nil {
				t.Fatal("changed authority permitted a new child identity")
			}
			forks, err := s.ListAskForks(t.Context())
			if err != nil || (change != "running" && len(forks) != 0) || (change == "running" && (len(forks) != 1 || string(forks[0].NativeID) != "original-child")) {
				t.Fatalf("failed remember changed cleanup metadata: %+v %v", forks, err)
			}
		})
	}
}
