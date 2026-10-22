package store

import (
	"bytes"
	"database/sql"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/identity"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func TestTransportPendingOutboxPreservesPacketsAndControlPriority(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	r := outgoingRequest(t, f, 1)
	if _, err := s.OutboxEnqueue(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	// The independent control lane must reach transport before data.
	control, err := f.receipt(protocol.ReceiptPayload{MessageID: r.Message.MessageID, AttemptID: r.Message.MessageID, Stage: "receiver_stored", EvidenceKind: "peer_commit", At: testNow})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := protocol.DecodePacket(control)
	if err != nil {
		t.Fatal(err)
	}
	controlID := decoded.Header.MessageID
	if err = s.write(t.Context(), func(tx *sql.Tx) error {
		_, putErr := s.putOutbox(t.Context(), tx, controlID, "grant_one", control, "control", testNow+100, testNow+1)
		return putErr
	}); err != nil {
		t.Fatal(err)
	}
	items, err := s.PendingOutbox(t.Context(), 1)
	if err != nil || len(items) != 1 || items[0].MessageID != controlID || !bytes.Equal(items[0].Packet, control) {
		t.Fatalf("control priority or original packet lost: %+v %v", items, err)
	}
	items, err = s.PendingOutbox(t.Context(), 10)
	if err != nil || len(items) != 2 || items[1].GrantID != "grant_one" || items[1].Stage != "sender_stored" {
		t.Fatalf("pending rows: %+v %v", items, err)
	}
	original, err := s.OutboxPacket(t.Context(), r.Message.MessageID)
	if err != nil || !bytes.Equal(items[1].Packet, original) {
		t.Fatal("pending list resealed packet", err)
	}
	items[1].Packet[0] ^= 1
	again, err := s.PendingOutbox(t.Context(), 10)
	if err != nil || !bytes.Equal(again[1].Packet, original) {
		t.Fatal("caller mutated durable packet", err)
	}
	s.clock = func() time.Time { return time.Unix(testNow+300, 0) }
	items, err = s.PendingOutbox(t.Context(), 10)
	if err != nil || len(items) != 0 {
		t.Fatalf("expired outbox exposed before maintenance: %+v %v", items, err)
	}
}

func TestTransportListInboxOnlyAdmittedPendingData(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	commitOne(t, s, f, 1)
	items, err := s.ListInbox(t.Context(), 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("pending inbox: %+v %v", items, err)
	}
	item := items[0]
	if item.MessageID != f.message.MessageID || item.GrantID != "grant_one" || string(item.Body) != f.message.BodyUTF8 || item.Routing != envelope.Live || item.State != "pending" || item.ConversationID != f.message.ConversationID || item.BudgetID != f.message.BudgetID || item.Hop != 0 {
		t.Fatalf("inbox metadata lost: %+v", item)
	}
	// Missing sender sequence 2 parks 3 without exposing its body.
	commitOne(t, s, f, 3)
	items, err = s.ListInbox(t.Context(), 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("parked future exposed: %+v %v", items, err)
	}
	s.clock = func() time.Time { return time.Unix(f.message.ExpiresAt, 0) }
	items, err = s.ListInbox(t.Context(), 10)
	if err != nil || len(items) != 0 {
		t.Fatalf("expired inbox exposed: %+v %v", items, err)
	}
}

func TestTransportListGrantsIncludesHistoricalAuthorityAndLiveEpoch(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	if err := s.GrantRevoke(t.Context(), "grant_one", 1, testNow); err != nil {
		t.Fatal(err)
	}
	s.clock = func() time.Time { return time.Unix(testNow+7200, 0) }
	items, err := s.ListGrants(t.Context())
	if err != nil || len(items) != 1 {
		t.Fatalf("historical grants: %+v %v", items, err)
	}
	g := items[0]
	if g.GrantID != "grant_one" || g.Epoch != 2 || !g.Revoked || g.ExpiresAt != testNow+600 || g.Scope != grant.Coauthor || !identity.EqualBinding(g.Issuer, f.recipient.cert.Binding) || !identity.EqualBinding(g.Subject, f.sender.cert.Binding) {
		t.Fatalf("grant authority or epoch lost: %+v", g)
	}
}

func TestTransportListsRejectInvalidLimitAndFailClosedOnSealedCorruption(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	for _, limit := range []int{0, -1, int(MaxMailboxLookahead) + 1} {
		if _, err := s.PendingOutbox(t.Context(), limit); !fault.IsCode(err, fault.InvalidEncoding) {
			t.Fatal("invalid outbox limit", err)
		}
		if _, err := s.ListInbox(t.Context(), limit); !fault.IsCode(err, fault.InvalidEncoding) {
			t.Fatal("invalid inbox limit", err)
		}
	}
	commitOne(t, s, f, 1)
	if _, err := s.db.ExecContext(t.Context(), `UPDATE inbox SET body=X'00'`); err != nil {
		t.Fatal(err)
	}
	if items, err := s.ListInbox(t.Context(), 10); !fault.IsCode(err, SealingFailed) || items != nil {
		t.Fatal("corrupt inbox returned partial plaintext", err)
	}
	if _, err := s.db.ExecContext(t.Context(), `UPDATE grants SET signed=X'00'`); err != nil {
		t.Fatal(err)
	}
	if items, err := s.ListGrants(t.Context()); !fault.IsCode(err, SealingFailed) || items != nil {
		t.Fatal("corrupt grant returned authority", err)
	}
}

func TestTransportCustodyAndTombstonesKeepContiguousFrontier(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	packet := f.packet(t, 1)
	got, err := s.CommitTransport(t.Context(), "mailbox_b", 2, packet, testNow)
	if err != nil || got.Frontier != 0 || got.Stage != "custody_committed" || got.MessageID != f.message.MessageID {
		t.Fatalf("sparse transport: %+v %v", got, err)
	}
	got, err = s.CommitTombstone(t.Context(), "mailbox_b", 1, testNow)
	if err != nil || got.Frontier != 2 || got.Stage != "expired" {
		t.Fatalf("contiguous expiry custody: %+v %v", got, err)
	}
	got, err = s.CommitTransport(t.Context(), "mailbox_b", 2, packet, testNow)
	if err != nil || got.Frontier != 2 {
		t.Fatalf("transport retry: %+v %v", got, err)
	}
	if _, err = s.CommitTombstone(t.Context(), "mailbox_b", 2, testNow); !fault.IsCode(err, fault.Conflict) {
		t.Fatal("tombstone replaced retained packet custody", err)
	}
	if _, err = s.CommitTombstone(t.Context(), "mailbox_b", 3+MaxMailboxLookahead, testNow); !fault.IsCode(err, Backpressure) {
		t.Fatal("far future tombstone bypassed lookahead", err)
	}
	for _, test := range []struct {
		mailbox string
		seq     uint64
	}{
		{"", 1}, {"mailbox_b", 0}, {"mailbox_b", protocol.MaxSafeInteger + 1},
	} {
		if _, err = s.CommitTombstone(t.Context(), test.mailbox, test.seq, testNow); !fault.IsCode(err, fault.InvalidEncoding) {
			t.Fatal("invalid tombstone routing accepted", err)
		}
	}
	if _, err = s.CommitTransport(t.Context(), "other", 1, packet, testNow); !fault.IsCode(err, fault.AudienceMismatch) {
		t.Fatal("transport mailbox mismatch accepted", err)
	}
	if _, err = s.CommitTransport(t.Context(), "mailbox_b", 3, []byte("not-packet"), testNow); err == nil {
		t.Fatal("malformed transport packet accepted")
	}
}
