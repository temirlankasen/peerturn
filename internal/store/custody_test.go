package store

import (
	"bytes"
	"sync"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func TestConcurrentInboxCustodyAndFrontier(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	packet := f.packet(t, 1)
	request := InboxRequest{Packet: packet, Mailbox: "mailbox_b", MailboxSeq: 2, GrantID: "grant_one", Open: f.options, Receipt: f.receipt}
	var wg sync.WaitGroup
	for range 64 {
		wg.Go(func() {
			got, err := s.InboxCommit(t.Context(), request)
			if err != nil || got.Frontier != 0 || got.Stage != "receiver_stored" {
				t.Errorf("custody: %+v %v", got, err)
			}
		})
	}
	wg.Wait()
	request.MailboxSeq = 1
	got, err := s.InboxCommit(t.Context(), request)
	if err != nil || got.Frontier != 2 || !got.Duplicate {
		t.Fatalf("gap ACK: %+v %v", got, err)
	}
	item, err := s.Inbox(t.Context(), f.message.MessageID)
	if err != nil || item.Routing != "live" || !bytes.Contains(item.Body, []byte(f.message.BodyUTF8)) {
		t.Fatalf("inbox: %+v %v", item, err)
	}
	if _, err = s.DeliveryIntent(t.Context(), f.message.MessageID, "11111111111111111111111111111111", testNow); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DeliveryIntent(t.Context(), f.message.MessageID, "22222222222222222222222222222222", testNow); !fault.IsCode(err, fault.Conflict) {
		t.Fatalf("second intent: %v", err)
	}
}

func TestSenderCustodyConflict(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	packet := f.packet(t, 1)
	r := OutboxRequest{Packet: packet, Message: f.message, GrantID: "grant_one", Now: testNow}
	for range 2 {
		got, err := s.OutboxEnqueue(t.Context(), r)
		if err != nil || got.Stage != "sender_stored" {
			t.Fatalf("outbox: %+v %v", got, err)
		}
	}
	// A freshly sealed packet carries the same ID but different authenticated ciphertext.
	r.Packet = f.packet(t, 1)
	if _, err := s.OutboxEnqueue(t.Context(), r); !fault.IsCode(err, fault.Conflict) {
		t.Fatalf("ID conflict: %v", err)
	}
}

func TestReceiptTerminalNeverRegresses(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	if _, err := s.OutboxEnqueue(t.Context(), outgoingRequest(t, f, 1)); err != nil {
		t.Fatal(err)
	}
	r := protocol.ReceiptPayload{MessageID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", AttemptID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Stage: "revoked", EvidenceKind: "local_commit", At: testNow}
	if err := s.RecordReceipt(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	r.Stage = "adapter_accepted"
	r.At++
	if err := s.RecordReceipt(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	got, err := s.Receipt(t.Context(), r.MessageID)
	if err != nil || got.Stage != "revoked" {
		t.Fatalf("regressed: %+v %v", got, err)
	}
}

func TestMonotonicReceiptStages(t *testing.T) {
	for _, terminal := range []string{"turn_completed", "reply", "in_doubt", "expired", "rejected", "revoked", "cancelled", "responder_failed"} {
		t.Run(terminal, func(t *testing.T) {
			s := testStore(t)
			f := fixture(t, s)
			if _, err := s.OutboxEnqueue(t.Context(), outgoingRequest(t, f, 1)); err != nil {
				t.Fatal(err)
			}
			r := protocol.ReceiptPayload{MessageID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", AttemptID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Stage: "sender_stored", EvidenceKind: "local_commit", At: testNow}
			for _, stage := range []string{"sender_stored", "relay_stored", "receiver_stored", terminal, "adapter_accepted", "sender_stored"} {
				r.Stage = stage
				if err := s.RecordReceipt(t.Context(), r); err != nil {
					t.Fatal(err)
				}
			}
			got, err := s.Receipt(t.Context(), r.MessageID)
			if err != nil || got.Stage != terminal {
				t.Fatalf("regression: %+v %v", got, err)
			}
		})
	}
}
