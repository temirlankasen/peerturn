package store

import (
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func pendingAsk(t *testing.T) (*Store, *testFixture, AskAdmission) {
	t.Helper()
	s := testStore(t)
	f := fixture(t, s)
	installReturnGrant(t, s, f)
	f.message.Kind = protocol.Ask
	commitOne(t, s, f, 1)
	return s, f, AskAdmission{MessageID: f.message.MessageID, NotificationGrantID: "grant_reverse",
		Parent: f.recipient.cert.Binding, ParentContext: []byte("verified-native-context"), Now: testNow}
}

func reservationFromAdmission(a AskAdmission) AskReservation {
	return AskReservation{RunID: strings.Repeat("1", 32), MessageID: a.MessageID, AttemptID: strings.Repeat("2", 32),
		NotificationGrantID: a.NotificationGrantID, Parent: a.Parent, ParentContext: a.ParentContext,
		Snapshot: []byte("private-snapshot"), Model: "provider/model", Effort: "xhigh", ProfileHash: "profile_one",
		BudgetID: "reservation_one", Now: a.Now}
}

func askAdmissionState(t *testing.T, s *Store) string {
	t.Helper()
	var result string
	err := s.db.QueryRowContext(t.Context(), `SELECT (SELECT COUNT(*) FROM responder_runs)||':'||(SELECT COUNT(*) FROM delivery_attempts)||':'||(SELECT COUNT(*) FROM audit)||':'||(SELECT SUM(turns) FROM sessions)||':'||(SELECT SUM(messages+burst+control_messages+control_used) FROM grants)||':'||(SELECT COUNT(*) FROM outbox)||':'||(SELECT state FROM inbox LIMIT 1)`).Scan(&result)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestAskAdmissionIsDryBeforeDeliveryIntent(t *testing.T) {
	s, _, a := pendingAsk(t)
	before := askAdmissionState(t, s)
	for range 3 {
		if err := s.CheckAskAdmission(t.Context(), a); err != nil {
			t.Fatal(err)
		}
	}
	if after := askAdmissionState(t, s); before != after {
		t.Fatalf("dry check mutated state: %s -> %s", before, after)
	}
	r := reservationFromAdmission(a)
	if _, err := s.ReserveAsk(t.Context(), r); err == nil {
		t.Fatal("reservation accepted without exact delivery intent")
	}
	if _, err := s.DeliveryIntent(t.Context(), a.MessageID, r.AttemptID, a.Now); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckAskAdmission(t.Context(), a); err == nil {
		t.Fatal("pending check accepted already attempted input")
	}
	a.AttemptID = r.AttemptID
	if err := s.CheckAskAdmission(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	reserveAsk(t, s, r)
}

func replaceAskGrantScope(t *testing.T, s *Store, f *testFixture, reverse bool, scope grant.Scope) {
	t.Helper()
	issuer, subject, id, key := f.recipient.cert, f.sender.cert, "grant_one", f.recipient.key
	if reverse {
		issuer, subject, id, key = f.sender.cert, f.recipient.cert, "grant_reverse", f.sender.key
	}
	g := grant.Grant{Version: 1, GrantID: id, Issuer: issuer.Binding, Subject: subject.Binding,
		Scope: scope, GrantEpoch: 1, NotBefore: testNow - 5, ExpiresAt: testNow + 600,
		Limits: grant.DefaultLimits(), ReplyRoute: &subject.Binding}
	if scope == grant.Inform {
		g.ReplyRoute = nil
	}
	signed, err := grant.Issue(g, key)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := s.seal("grants", id, []byte(signed))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(t.Context(), `UPDATE grants SET signed=?,hash=? WHERE id=?`, sealed, grant.Hash(signed), id); err != nil {
		t.Fatal(err)
	}
}

func TestAskAdmissionRejectsCheapFailuresWithoutMutation(t *testing.T) {
	for _, change := range []string{"scope", "reverse_scope", "reverse_missing", "reverse_revoked", "wrong_reverse", "expired", "parent", "context", "binding", "revoked", "epoch", "route", "turn_budget", "busy", "control_slots", "control_bytes", "retained_slots", "retained_bytes"} {
		t.Run(change, func(t *testing.T) {
			s, f, a := pendingAsk(t)
			var err error
			switch change {
			case "scope", "reverse_scope":
				replaceAskGrantScope(t, s, f, change == "reverse_scope", grant.Inform)
			case "reverse_missing":
				_, err = s.db.ExecContext(t.Context(), `DELETE FROM grants WHERE id='grant_reverse'`)
			case "reverse_revoked":
				_, err = s.db.ExecContext(t.Context(), `UPDATE grants SET revoked=1 WHERE id='grant_reverse'`)
			case "wrong_reverse":
				a.NotificationGrantID = "grant_one"
			case "expired":
				a.Now += 300
			case "parent":
				a.Parent = f.sender.cert.Binding
			case "context":
				a.ParentContext = []byte("different-native-context")
			case "binding":
				_, err = s.db.ExecContext(t.Context(), `UPDATE bindings SET state='paused_owner'`)
			case "revoked":
				_, err = s.db.ExecContext(t.Context(), `UPDATE grants SET revoked=1 WHERE id='grant_one'`)
			case "epoch":
				_, err = s.db.ExecContext(t.Context(), `UPDATE grants SET epoch=epoch+1 WHERE id='grant_one'`)
			case "route":
				_, err = s.db.ExecContext(t.Context(), `UPDATE inbox SET route='live'`)
			case "turn_budget":
				_, err = s.db.ExecContext(t.Context(), `UPDATE sessions SET turn_window=?,turns=20`, a.Now)
			case "busy":
				r := reservationFromAdmission(a)
				if _, err = s.DeliveryIntent(t.Context(), r.MessageID, r.AttemptID, r.Now); err != nil {
					t.Fatal(err)
				}
				reserveAsk(t, s, r)
				commitOne(t, s, f, 2)
				a.MessageID = strings.Repeat("0", 31) + "2"
			case "control_slots":
				fillAskControls(t, s, MaxControlMessages-5, 1)
			case "control_bytes":
				fillAskControls(t, s, 1, MaxControlBytes-6*protocol.MaxPacket+1)
			case "retained_slots":
				fillAskControls(t, s, MaxControlMessages*64-5, 1)
				_, err = s.db.ExecContext(t.Context(), `UPDATE outbox SET stage='relay_stored' WHERE grant_id='grant_reverse'`)
			case "retained_bytes":
				fillAskControls(t, s, 1, MaxControlBytes*8-6*protocol.MaxPacket+1)
				_, err = s.db.ExecContext(t.Context(), `UPDATE outbox SET stage='relay_stored' WHERE grant_id='grant_reverse'`)
			}
			if err != nil {
				t.Fatal(err)
			}
			before := askAdmissionState(t, s)
			if err = s.CheckAskAdmission(t.Context(), a); err == nil {
				t.Fatal("cheap failure accepted")
			}
			if after := askAdmissionState(t, s); before != after {
				t.Fatalf("failed dry check mutated state: %s -> %s", before, after)
			}
		})
	}
}

func TestAskReservationRechecksDryAdmission(t *testing.T) {
	for _, change := range []string{"revoked", "control_capacity", "turn_budget"} {
		t.Run(change, func(t *testing.T) {
			s, _, a := pendingAsk(t)
			if err := s.CheckAskAdmission(t.Context(), a); err != nil {
				t.Fatal(err)
			}
			r := reservationFromAdmission(a)
			if _, err := s.DeliveryIntent(t.Context(), r.MessageID, r.AttemptID, r.Now); err != nil {
				t.Fatal(err)
			}
			var err error
			switch change {
			case "revoked":
				_, err = s.db.ExecContext(t.Context(), `UPDATE grants SET revoked=1 WHERE id='grant_one'`)
			case "control_capacity":
				fillAskControls(t, s, MaxControlMessages-5, 1)
			case "turn_budget":
				_, err = s.db.ExecContext(t.Context(), `UPDATE sessions SET turn_window=?,turns=20`, a.Now)
			}
			if err != nil {
				t.Fatal(err)
			}
			before := askAdmissionState(t, s)
			if _, err = s.ReserveAsk(t.Context(), r); err == nil {
				t.Fatal("reservation trusted stale dry check")
			}
			if after := askAdmissionState(t, s); before != after {
				t.Fatalf("failed reservation partially committed: %s -> %s", before, after)
			}
		})
	}
}

func TestAskAdmissionChecksExactReciprocalControlLane(t *testing.T) {
	s, f, a := pendingAsk(t)
	g := grant.Grant{Version: 1, GrantID: "another_reverse", Issuer: f.sender.cert.Binding, Subject: f.recipient.cert.Binding,
		Scope: grant.Coauthor, GrantEpoch: 1, NotBefore: testNow - 5, ExpiresAt: testNow + 600,
		Limits: grant.DefaultLimits(), ReplyRoute: &f.recipient.cert.Binding}
	var err error
	f.reverse, err = grant.Issue(g, f.sender.key)
	if err != nil {
		t.Fatal(err)
	}
	installReturnGrant(t, s, f)
	a.NotificationGrantID = ""
	if err = s.CheckAskAdmission(t.Context(), a); !fault.IsCode(err, fault.ScopeDenied) {
		t.Fatalf("ambiguous reciprocal grant selected automatically: %v", err)
	}
	fillAskControls(t, s, MaxControlMessages, 1)
	a.NotificationGrantID = "grant_reverse"
	if err = s.CheckAskAdmission(t.Context(), a); !fault.IsCode(err, Backpressure) {
		t.Fatalf("other reciprocal capacity masked the exact full lane: %v", err)
	}
	a.NotificationGrantID = "another_reverse"
	if err = s.CheckAskAdmission(t.Context(), a); err != nil {
		t.Fatalf("exact healthy reciprocal lane refused: %v", err)
	}
}
