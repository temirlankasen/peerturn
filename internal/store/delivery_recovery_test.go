package store

import (
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func TestRecoveryUnsentIntentCanReceiveFreshAttempt(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	commitOne(t, s, f, 1)
	id, attempt := f.message.MessageID, "11111111111111111111111111111111"
	if _, err := s.DeliveryIntent(t.Context(), id, attempt, testNow); err != nil {
		t.Fatal(err)
	}
	if err := s.RecoverInDoubt(t.Context(), testNow+1); err != nil {
		t.Fatal(err)
	}
	if uncertain, err := s.UncertainDeliveries(t.Context()); err != nil || uncertain {
		t.Fatalf("unsent frame fenced adapter: %v %v", uncertain, err)
	}
	if item, err := s.Inbox(t.Context(), id); err != nil || item.State != "pending" {
		t.Fatalf("unsent frame not reeligible: %+v %v", item, err)
	}
	if r, err := s.Receipt(t.Context(), id); err != nil || r.Stage != "receiver_stored" || r.Outcome != "not_sent" || r.AttemptID != attempt {
		t.Fatalf("unsent evidence: %+v %v", r, err)
	}
	if old, err := s.DeliveryIntent(t.Context(), id, attempt, testNow+2); err != nil || old.Start || old.State != "not_sent" {
		t.Fatalf("old attempt restarted: %+v %v", old, err)
	}
	fresh, err := s.DeliveryIntent(t.Context(), id, "22222222222222222222222222222222", testNow+2)
	if err != nil || !fresh.Start || fresh.State != "intent" {
		t.Fatalf("fresh attempt refused: %+v %v", fresh, err)
	}
	if err = s.AuthorizeDelivery(t.Context(), id, fresh.ID, testNow+2); err != nil {
		t.Fatal(err)
	}
	r := protocol.ReceiptPayload{MessageID: id, AttemptID: fresh.ID, Stage: "adapter_accepted", EvidenceKind: "native_queue", At: testNow + 2}
	if err = s.DeliveryResult(t.Context(), fresh.ID, r, []byte("native-queue")); err != nil {
		t.Fatal(err)
	}
	if got, receiptErr := s.Receipt(t.Context(), id); receiptErr != nil || got.Stage != "adapter_accepted" || got.AttemptID != fresh.ID {
		t.Fatalf("fresh attempt evidence lost: %+v %v", got, receiptErr)
	}
	var retired int
	if err = s.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM audit WHERE action='delivery_attempt_retired' AND message_id=? AND reason=?`, id, attempt+":not_sent").Scan(&retired); err != nil || retired != 1 {
		t.Fatalf("old attempt history lost: %d %v", retired, err)
	}
}

func TestAuthorizeDeliveryPersistsBeforeNativeCall(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	commitOne(t, s, f, 1)
	id, attempt := f.message.MessageID, "11111111111111111111111111111111"
	if _, err := s.DeliveryIntent(t.Context(), id, attempt, testNow); err != nil {
		t.Fatal(err)
	}
	if err := s.AuthorizeDelivery(t.Context(), id, attempt, testNow); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := s.db.QueryRowContext(t.Context(), `SELECT state FROM delivery_attempts WHERE id=?`, attempt).Scan(&state); err != nil || state != "authorized" {
		t.Fatalf("authorization not durable: %q %v", state, err)
	}
}

func TestRevokeNeverAuthorizedIntentIsNotAmbiguous(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	commitOne(t, s, f, 1)
	id, attempt := f.message.MessageID, "11111111111111111111111111111111"
	if _, err := s.DeliveryIntent(t.Context(), id, attempt, testNow); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantRevoke(t.Context(), "grant_one", 1, testNow+1, f.receipt); err != nil {
		t.Fatal(err)
	}
	if uncertain, err := s.UncertainDeliveries(t.Context()); err != nil || uncertain {
		t.Fatalf("revoke fenced unsent frame: %v %v", uncertain, err)
	}
	if r, err := s.Receipt(t.Context(), id); err != nil || r.Stage != "revoked" || r.Outcome != "not_sent" {
		t.Fatalf("false ambiguous revoke: %+v %v", r, err)
	}
	if err := s.AuthorizeDelivery(t.Context(), id, attempt, testNow+2); !fault.IsCode(err, fault.Revoked) {
		t.Fatalf("revoked frame authorized: %v", err)
	}
}

func TestLegacyIntentWithoutAuthorizationMarkerRemainsAmbiguous(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	commitOne(t, s, f, 1)
	id, attempt := f.message.MessageID, "11111111111111111111111111111111"
	if _, err := s.DeliveryIntent(t.Context(), id, attempt, testNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), `UPDATE audit SET reason='' WHERE action='delivery_intent' AND message_id=?`, id); err != nil {
		t.Fatal(err)
	}
	if err := s.RecoverInDoubt(t.Context(), testNow+1); err != nil {
		t.Fatal(err)
	}
	if uncertain, err := s.UncertainDeliveries(t.Context()); err != nil || !uncertain {
		t.Fatalf("legacy outcome lost fence: %v %v", uncertain, err)
	}
	if item, err := s.Inbox(t.Context(), id); err != nil || item.State != "in_doubt" {
		t.Fatalf("legacy intent reoffered: %+v %v", item, err)
	}
}

func TestLegacyIntentRevokePreservesAmbiguity(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	commitOne(t, s, f, 1)
	id, attempt := f.message.MessageID, "11111111111111111111111111111111"
	if _, err := s.DeliveryIntent(t.Context(), id, attempt, testNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), `UPDATE audit SET reason='' WHERE action='delivery_intent' AND message_id=?`, id); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantRevoke(t.Context(), "grant_one", 1, testNow+1, f.receipt); err != nil {
		t.Fatal(err)
	}
	if uncertain, err := s.UncertainDeliveries(t.Context()); err != nil || !uncertain {
		t.Fatalf("legacy revoke lost fence: %v %v", uncertain, err)
	}
	if r, err := s.Receipt(t.Context(), id); err != nil || r.Stage != "in_doubt" || r.Outcome != "submit_unknown" {
		t.Fatalf("false unsent legacy revoke: %+v %v", r, err)
	}
}

func TestNotSentAttemptCannotReuseHistoricalIdentifier(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	commitOne(t, s, f, 1)
	id := f.message.MessageID
	const first = "11111111111111111111111111111111"
	for _, attempt := range []string{first, "22222222222222222222222222222222"} {
		if _, err := s.DeliveryIntent(t.Context(), id, attempt, testNow); err != nil {
			t.Fatal(err)
		}
		if err := s.RecoverInDoubt(t.Context(), testNow+1); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DeliveryIntent(t.Context(), id, first, testNow+2); !fault.IsCode(err, fault.Conflict) {
		t.Fatalf("historical attempt ID reused: %v", err)
	}
}
