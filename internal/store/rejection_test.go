package store

import (
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
)

func TestRejectedMailboxTombstoneDoesNotConsumeSenderSequence(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	for range 2 {
		got, err := s.RejectMailbox(t.Context(), "mailbox_b", 1, []byte("malformed transport packet"), fault.InvalidEncoding, testNow)
		if err != nil || got.Stage != "rejected" || got.Frontier != 1 {
			t.Fatalf("rejected custody: %+v %v", got, err)
		}
	}
	r := InboxRequest{Packet: f.packet(t, 1), Mailbox: "mailbox_b", MailboxSeq: 2, GrantID: "grant_one", Open: f.options, Receipt: f.receipt}
	got, err := s.InboxCommit(t.Context(), r)
	if err != nil || got.Stage != "receiver_stored" || got.Frontier != 2 {
		t.Fatalf("untrusted sender sequence: %+v %v", got, err)
	}
	if _, err = s.RejectMailbox(t.Context(), "mailbox_b", 1, []byte("different packet"), fault.InvalidEncoding, testNow); !fault.IsCode(err, fault.Conflict) {
		t.Fatalf("rejected slot conflict: %v", err)
	}
}
