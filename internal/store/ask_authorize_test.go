package store

import (
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
)

func TestAskAuthorizationRechecksBindingImmediatelyBeforeSpawn(t *testing.T) {
	for _, change := range []string{"context", "state", "grant", "lease", "ttl"} {
		t.Run(change, func(t *testing.T) {
			s, _, r := askFixture(t)
			reserveAsk(t, s, r)
			now := testNow
			var err error
			switch change {
			case "context":
				var sealed []byte
				sealed, err = s.seal("bindings", bindingKey(r.Parent), []byte("different-instance"))
				if err == nil {
					_, err = s.db.ExecContext(t.Context(), `UPDATE bindings SET context=? WHERE session_id=?`, sealed, bindingKey(r.Parent))
				}
			case "state":
				_, err = s.db.ExecContext(t.Context(), `UPDATE bindings SET state='paused_owner'`)
			case "grant":
				_, err = s.db.ExecContext(t.Context(), `UPDATE grants SET revoked=1 WHERE id='grant_one'`)
			case "lease":
				_, err = s.db.ExecContext(t.Context(), `UPDATE grants SET lease_generation=lease_generation+1 WHERE id='grant_one'`)
			case "ttl":
				now += 300
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = s.AuthorizeAsk(t.Context(), r.RunID, now); err == nil {
				t.Fatal("changed authority authorized spawn")
			}
			var state string
			if err = s.db.QueryRowContext(t.Context(), `SELECT state FROM delivery_attempts WHERE id=?`, r.AttemptID).Scan(&state); err != nil || state != "intent" {
				t.Fatalf("partial authorization: %s %v", state, err)
			}
		})
	}
}

func TestAskRecoveryRepeatedReceiptIsStable(t *testing.T) {
	s, _, r := askFixture(t)
	reserveAsk(t, s, r)
	if err := s.AuthorizeAsk(t.Context(), r.RunID, testNow); err != nil {
		t.Fatal(err)
	}
	if err := s.AdvanceAsk(t.Context(), r.RunID, askEvidence(r, "responder_started"), []byte("child-id")); err != nil {
		t.Fatal(err)
	}
	if err := s.RecoverAskRuns(t.Context(), testNow+1); err != nil {
		t.Fatal(err)
	}
	before, err := s.Receipt(t.Context(), r.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RecoverAskRuns(t.Context(), testNow+2); err != nil {
		t.Fatal(err)
	}
	if err = s.RecoverInDoubt(t.Context(), testNow+2); err != nil {
		t.Fatal(err)
	}
	after, err := s.Receipt(t.Context(), r.MessageID)
	if err != nil || before != after || after.Stage != "responder_failed" || after.AttemptID != r.AttemptID {
		t.Fatalf("repeated recovery changed custody: %+v %+v %v", before, after, err)
	}
	if err = s.AuthorizeAsk(t.Context(), r.RunID, testNow+3); !fault.IsCode(err, fault.Conflict) {
		t.Fatalf("recovery reauthorized child: %v", err)
	}
}
