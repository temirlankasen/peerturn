package store

import (
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func TestNativeResultIdenticalRetryAndConflict(t *testing.T) {
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
	for range 2 {
		if err := s.DeliveryResult(t.Context(), attempt, r, []byte("queue-one")); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeliveryResult(t.Context(), attempt, r, []byte("queue-two")); !fault.IsCode(err, fault.Conflict) {
		t.Fatalf("conflicting native result: %v", err)
	}
	var events int
	if err := s.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM audit WHERE action='delivery_result' AND message_id=? AND digest!='' AND reason='adapter_accepted'`, r.MessageID).Scan(&events); err != nil || events != 1 {
		t.Fatalf("result audit duplicated/missing: %d %v", events, err)
	}
}

func TestMailboxLookaheadRollsBackCustody(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	request := InboxRequest{Packet: f.packet(t, 1), Mailbox: "mailbox_b", MailboxSeq: MaxMailboxLookahead + 1, GrantID: "grant_one", Open: f.options, Receipt: f.receipt}
	if _, err := s.InboxCommit(t.Context(), request); !fault.IsCode(err, Backpressure) {
		t.Fatalf("mailbox gap unbounded: %v", err)
	}
	if _, err := s.Inbox(t.Context(), f.message.MessageID); !fault.IsCode(err, NotFound) {
		t.Fatalf("lookahead partial custody: %v", err)
	}
}
