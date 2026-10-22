package store

import (
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
)

func TestInboundCollisionCannotChangeOutboxEvidence(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "live", true: "expired"}[expired], func(t *testing.T) {
			s := testStore(t)
			f := fixture(t, s)
			outgoing := outgoingRequest(t, f, 1)
			if _, err := s.OutboxEnqueue(t.Context(), outgoing); err != nil {
				t.Fatal(err)
			}
			if expired {
				f.options.Now = testNow + 301
			}
			_, err := s.InboxCommit(t.Context(), InboxRequest{Packet: f.packet(t, 1), Mailbox: "mailbox_b", MailboxSeq: 1, GrantID: "grant_one", Open: f.options, Receipt: f.receipt})
			if !fault.IsCode(err, fault.Conflict) {
				t.Fatalf("inbound/outbound ID collision: %v", err)
			}
			r, err := s.Receipt(t.Context(), outgoing.Message.MessageID)
			if err != nil || r.Stage != "sender_stored" {
				t.Fatalf("inbound rewrote outbound evidence: %+v %v", r, err)
			}
			if _, err = s.OutboxPacket(t.Context(), outgoing.Message.MessageID); err != nil {
				t.Fatalf("collision suppressed retries: %v", err)
			}
		})
	}
}

func TestOutboundCollisionCannotReplaceInboundEvidence(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	commitOne(t, s, f, 1)
	if _, err := s.OutboxEnqueue(t.Context(), outgoingRequest(t, f, 1)); !fault.IsCode(err, fault.Conflict) {
		t.Fatalf("outbound/inbound ID collision: %v", err)
	}
	r, err := s.Receipt(t.Context(), f.message.MessageID)
	if err != nil || r.Stage != "receiver_stored" {
		t.Fatalf("outbound rewrote inbound evidence: %+v %v", r, err)
	}
}
