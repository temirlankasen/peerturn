package store

import (
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func commitOne(t *testing.T, s *Store, f *testFixture, seq uint64) Custody {
	t.Helper()
	got, err := s.InboxCommit(t.Context(), InboxRequest{Packet: f.packet(t, seq), Mailbox: "mailbox_b", MailboxSeq: seq, GrantID: "grant_one", Open: f.options, Receipt: f.receipt})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestBoundedGapExpiryAndReplay(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	if got := commitOne(t, s, f, 2); got.Stage != "sequence_gap" || got.Frontier != 0 {
		t.Fatalf("future: %+v", got)
	}
	item, err := s.Inbox(t.Context(), "00000000000000000000000000000002")
	if err != nil || len(item.Body) != 0 || item.Routing != "" {
		t.Fatalf("parked dispatch: %+v %v", item, err)
	}
	request := InboxRequest{Packet: f.packet(t, 66), Mailbox: "mailbox_b", MailboxSeq: 66, GrantID: "grant_one", Open: f.options, Receipt: f.receipt}
	if got, checkErr := s.InboxCommit(t.Context(), request); checkErr != nil || got.Stage != "rejected" || got.Reason != fault.SequenceWindowExceeded {
		t.Fatalf("unbounded future: %+v %v", got, checkErr)
	}
	if got := commitOne(t, s, f, 1); got.Frontier != 2 {
		t.Fatalf("custody frontier: %+v", got)
	}
	if err = s.Expire(t.Context(), testNow+301); err != nil {
		t.Fatal(err)
	}
	f.options.Now = testNow + 301
	got, err := s.InboxCommit(t.Context(), InboxRequest{Packet: f.packet(t, 2), Mailbox: "mailbox_b", MailboxSeq: 2, GrantID: "grant_one", Open: f.options, Receipt: f.receipt})
	if !fault.IsCode(err, fault.Conflict) {
		t.Fatalf("fresh ciphertext under expired ID: %+v %v", got, err)
	}
	item, err = s.Inbox(t.Context(), "00000000000000000000000000000002")
	if err != nil || item.State != "expired" || len(item.Body) != 0 {
		t.Fatalf("expiry: %+v %v", item, err)
	}
}

func TestCrashAfterAuthorizationBecomesInDoubt(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	commitOne(t, s, f, 1)
	id := f.message.MessageID
	attempt := "11111111111111111111111111111111"
	first, err := s.DeliveryIntent(t.Context(), id, attempt, testNow)
	if err != nil || !first.Start {
		t.Fatalf("intent: %+v %v", first, err)
	}
	if err = s.AuthorizeDelivery(t.Context(), id, attempt, testNow); err != nil {
		t.Fatal(err)
	}
	if err = s.RecoverInDoubt(t.Context(), testNow+1); err != nil {
		t.Fatal(err)
	}
	retry, err := s.DeliveryIntent(t.Context(), id, attempt, testNow+1)
	if err != nil || retry.Start || retry.State != "in_doubt" {
		t.Fatalf("blind retry: %+v %v", retry, err)
	}
	r, err := s.Receipt(t.Context(), id)
	if err != nil || r.Stage != "in_doubt" {
		t.Fatalf("evidence: %+v %v", r, err)
	}
}

func TestCrashAfterIntentBecomesInDoubt(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	commitOne(t, s, f, 1)
	id, attempt := f.message.MessageID, "11111111111111111111111111111111"
	first, err := s.DeliveryIntent(t.Context(), id, attempt, testNow)
	if err != nil || !first.Start {
		t.Fatalf("intent: %+v %v", first, err)
	}
	// Legacy M4 intents did not retain proof that authorization never happened.
	if _, err = s.db.ExecContext(t.Context(), `DELETE FROM audit WHERE action='delivery_intent' AND message_id=?`, id); err != nil {
		t.Fatal(err)
	}
	if err = s.RecoverInDoubt(t.Context(), testNow+1); err != nil {
		t.Fatal(err)
	}
	retry, err := s.DeliveryIntent(t.Context(), id, attempt, testNow+1)
	if err != nil || retry.Start || retry.State != "in_doubt" {
		t.Fatalf("blind legacy retry: %+v %v", retry, err)
	}
	r, err := s.Receipt(t.Context(), id)
	if err != nil || r.Stage != "in_doubt" {
		t.Fatalf("legacy evidence: %+v %v", r, err)
	}
}

func TestRevokeAtomicAndIdempotent(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	commitOne(t, s, f, 1)
	if err := s.GrantRevoke(t.Context(), "grant_one", 1, testNow+1, f.receipt); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantRevoke(t.Context(), "grant_one", 1, testNow+2); err != nil {
		t.Fatal(err)
	}
	item, err := s.Inbox(t.Context(), f.message.MessageID)
	if err != nil || item.State != "quarantined" {
		t.Fatalf("quarantine: %+v %v", item, err)
	}
	if _, err = s.DeliveryIntent(t.Context(), f.message.MessageID, "11111111111111111111111111111111", testNow+2); !fault.IsCode(err, fault.Revoked) {
		t.Fatalf("post-revoke admission: %v", err)
	}
	r := protocol.ReceiptPayload{MessageID: f.message.MessageID, AttemptID: "11111111111111111111111111111111", Stage: "adapter_accepted", EvidenceKind: "native_queue", At: testNow + 2}
	if err = s.DeliveryResult(t.Context(), r.AttemptID, r, []byte("late queue result")); !fault.IsCode(err, NotFound) {
		t.Fatalf("unknown delivery result: %v", err)
	}
}

func TestRevokeQuarantinesNativeQueueBeforeModelInput(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	commitOne(t, s, f, 1)
	attempt := "11111111111111111111111111111111"
	if _, err := s.DeliveryIntent(t.Context(), f.message.MessageID, attempt, testNow); err != nil {
		t.Fatal(err)
	}
	r := protocol.ReceiptPayload{MessageID: f.message.MessageID, AttemptID: attempt, Stage: "adapter_accepted", EvidenceKind: "native_queue", At: testNow}
	if err := s.AuthorizeDelivery(t.Context(), r.MessageID, attempt, testNow); err != nil {
		t.Fatal(err)
	}
	if err := s.DeliveryResult(t.Context(), attempt, r, []byte("queue-id")); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantRevoke(t.Context(), "grant_one", 1, testNow+1, f.receipt); err != nil {
		t.Fatal(err)
	}
	item, err := s.Inbox(t.Context(), f.message.MessageID)
	if err != nil || item.State != "quarantined" {
		t.Fatalf("queued coauthor was not fenced: %+v %v", item, err)
	}
}
