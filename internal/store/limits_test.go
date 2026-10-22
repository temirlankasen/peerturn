package store

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func TestDataRateDoesNotStarveControlOrResetByConversation(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	for seq := uint64(1); seq <= 5; seq++ {
		commitOne(t, s, f, seq)
	}
	request := InboxRequest{Packet: f.packet(t, 6), Mailbox: "mailbox_b", MailboxSeq: 6, GrantID: "grant_one", Open: f.options, Receipt: f.receipt}
	if got, err := s.InboxCommit(t.Context(), request); err != nil || got.Stage != "rejected" || got.Reason != fault.BudgetExceeded || got.Frontier != 6 {
		t.Fatalf("burst reset by conversation: %+v %v", got, err)
	}
	// Control packets must remain durable without waking or generating a receipt.
	f.message.Kind = protocol.Receipt
	body, bodyErr := json.Marshal(protocol.ReceiptPayload{MessageID: f.message.MessageID, AttemptID: f.message.MessageID, Stage: "receiver_stored", EvidenceKind: "peer_commit", At: testNow})
	if bodyErr != nil {
		t.Fatal(bodyErr)
	}
	f.message.BodyUTF8 = string(body)
	request.Packet, request.MailboxSeq = f.packet(t, 7), 7
	if got, err := s.InboxCommit(t.Context(), request); err != nil || got.Frontier != 7 {
		t.Fatalf("control starved: %v", err)
	}
	// The unknown referenced outbox is a terminal control rejection, with no wake.
	if _, err := s.Inbox(t.Context(), fmt.Sprintf("%032x", 7)); !fault.IsCode(err, NotFound) {
		t.Fatalf("unbound receipt admitted: %v", err)
	}
	var count int
	if err := s.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM outbox WHERE lane='control'`).Scan(&count); err != nil || count != 5 {
		t.Fatalf("response to receipt: %d %v", count, err)
	}
}

func TestQueueBackpressureKeepsControlReserve(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	for seq := uint64(1); seq <= MaxQueuedMessages; seq++ {
		if _, err := s.OutboxEnqueue(t.Context(), outgoingRequest(t, f, seq)); err != nil {
			t.Fatalf("enqueue %d: %v", seq, err)
		}
	}
	if _, err := s.OutboxEnqueue(t.Context(), outgoingRequest(t, f, MaxQueuedMessages+1)); !fault.IsCode(err, Backpressure) {
		t.Fatalf("unbounded backlog: %v", err)
	}
	f.header.MessageID, f.message.MessageID = "cccccccccccccccccccccccccccccccc", "cccccccccccccccccccccccccccccccc"
	f.message.ConversationID = "dddddddddddddddddddddddddddddddd"
	commitOne(t, s, f, 1)
}

func TestConversationResolvedInsideCustody(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	commitOne(t, s, f, 1)
	h, m := f.header, f.message
	h.MessageID = "cccccccccccccccccccccccccccccccc"
	m.MessageID = h.MessageID
	m.SenderSeq = 2
	m.ParentID = f.message.MessageID
	m.Hop = 1
	packet, err := envelope.Seal(h, m, f.sender.key, f.recipient.encryption.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	request := InboxRequest{Packet: raw, Mailbox: "mailbox_b", MailboxSeq: 2, GrantID: "grant_one", Open: f.options, Receipt: f.receipt}
	// A forged caller snapshot cannot replace the committed chain.
	request.Open.Conversation = &envelope.Conversation{ID: "forged"}
	if _, err = s.InboxCommit(t.Context(), request); err != nil {
		t.Fatalf("durable chain lookup: %v", err)
	}
}
