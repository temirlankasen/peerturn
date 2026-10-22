package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func TestFarFutureRejectionDoesNotHoldSharedFrontier(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	r := InboxRequest{Packet: f.packet(t, 66), Mailbox: "mailbox_b", MailboxSeq: 1, GrantID: "grant_one", Open: f.options, Receipt: f.receipt}
	got, err := s.InboxCommit(t.Context(), r)
	if err != nil || got.Stage != "rejected" || got.Frontier != 1 {
		t.Fatalf("far future must be terminal custody: %+v %v", got, err)
	}
	status, err := s.Rejections(t.Context())
	if err != nil || len(status) != 1 || status[0].GrantID != "grant_one" || status[0].Reason != fault.SequenceWindowExceeded || status[0].Count != 1 {
		t.Fatalf("missing grant attribution: %+v %v", status, err)
	}
	r.Packet, r.MailboxSeq = f.packet(t, 1), 2
	got, err = s.InboxCommit(t.Context(), r)
	if err != nil || got.Stage != "receiver_stored" || got.Frontier != 2 {
		t.Fatalf("far future consumed sender authority: %+v %v", got, err)
	}
}

func TestRejectedCustodyRetryHasSameDecision(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	f.message.Kind = protocol.KeyUpdate
	r := InboxRequest{Packet: f.packet(t, 1), Mailbox: "mailbox_b", MailboxSeq: 1, GrantID: "grant_one", Open: f.options, Receipt: f.receipt}
	for n := range 2 {
		got, err := s.InboxCommit(t.Context(), r)
		if err != nil || got.Stage != "rejected" || got.Reason != fault.ScopeDenied || got.Frontier != 1 || got.Duplicate != (n == 1) {
			t.Fatalf("rejected retry %d: %+v %v", n, got, err)
		}
	}
}

func TestCompactedRejectedCustodyRetryStillAcknowledges(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	f.message.Kind = protocol.KeyUpdate
	r := InboxRequest{Packet: f.packet(t, 1), Mailbox: "mailbox_b", MailboxSeq: 1, GrantID: "grant_one", Open: f.options, Receipt: f.receipt}
	if _, err := s.InboxCommit(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	if err := s.Expire(t.Context(), testNow+1); err != nil {
		t.Fatal(err)
	}
	got, err := s.InboxCommit(t.Context(), r)
	if err != nil || !got.Duplicate || got.Frontier != 1 {
		t.Fatalf("compaction lost custody: %+v %v", got, err)
	}
}

func TestAuthenticatedScopeRejectionConsumesSequence(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	f.message.Kind = protocol.KeyUpdate
	r := InboxRequest{Packet: f.packet(t, 1), Mailbox: "mailbox_b", MailboxSeq: 1, GrantID: "grant_one", Open: f.options, Receipt: f.receipt}
	got, err := s.InboxCommit(t.Context(), r)
	if err != nil || got.Stage != "rejected" || got.Frontier != 1 {
		t.Fatalf("scope rejection custody: %+v %v", got, err)
	}
	f.message.Kind = protocol.Coauthor
	r.Packet, r.MailboxSeq = f.packet(t, 2), 2
	got, err = s.InboxCommit(t.Context(), r)
	if err != nil || got.Stage != "receiver_stored" || got.Frontier != 2 {
		t.Fatalf("scope rejection left a sender gap: %+v %v", got, err)
	}
}

func TestPersistentBackpressureBecomesCustodyRejection(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	if err := s.write(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<128) INSERT INTO outbox(id,grant_id,digest,packet,lane,stage,expires_at,bytes,created_at) SELECT printf('f%031d',i),'grant_one','',X'','control','sender_stored',?,1,? FROM n`, testNow+300, testNow)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	r := InboxRequest{Packet: f.packet(t, 1), Mailbox: "mailbox_b", MailboxSeq: 1, GrantID: "grant_one", Open: f.options, Receipt: f.receipt}
	got, err := s.InboxCommit(t.Context(), r)
	if err != nil || got.Stage != "rejected" || got.Frontier != 1 {
		t.Fatalf("permanent queue refusal held mailbox: %+v %v", got, err)
	}
	if _, err = s.Inbox(t.Context(), f.message.MessageID); !fault.IsCode(err, NotFound) {
		t.Fatalf("rejected admission retained body: %v", err)
	}
}

func TestExpiredFloodHasBoundedAdmissions(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	f.options.Now = testNow + 301
	accepted := 0
	for seq := uint64(1); seq <= 40; seq++ {
		got, err := s.InboxCommit(t.Context(), InboxRequest{Packet: f.packet(t, seq), Mailbox: "mailbox_b", MailboxSeq: seq, GrantID: "grant_one", Open: f.options, Receipt: f.receipt})
		if err != nil || got.Frontier != seq {
			t.Fatalf("expired slot %d: %+v %v", seq, got, err)
		}
		if got.Stage == "expired" {
			accepted++
		}
	}
	if accepted > 5 {
		t.Fatalf("unmetered expired admissions: %d", accepted)
	}
}

func TestReceiptFloodHasBoundedAdmissions(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	if _, err := s.OutboxEnqueue(t.Context(), outgoingRequest(t, f, 1)); err != nil {
		t.Fatal(err)
	}
	o := installReturnGrant(t, s, f)
	body, err := json.Marshal(protocol.ReceiptPayload{MessageID: f.message.MessageID, AttemptID: f.message.MessageID, Stage: "receiver_stored", EvidenceKind: "peer_commit", At: testNow})
	if err != nil {
		t.Fatal(err)
	}
	accepted := 0
	for seq := uint64(1); seq <= 40; seq++ {
		r := returnReply(t, f, o, func(m *protocol.MessagePayload) {
			m.MessageID, m.SenderSeq = fmt.Sprintf("%032x", seq+5000), seq
			m.Kind, m.ParentID, m.ReplyTo, m.Hop, m.BodyUTF8 = protocol.Receipt, "", "", 0, string(body)
		})
		r.MailboxSeq = seq
		got, commitErr := s.InboxCommit(t.Context(), r)
		if commitErr != nil || got.Frontier != seq {
			t.Fatalf("control slot %d: %+v %v", seq, got, commitErr)
		}
		if got.Stage != "rejected" {
			accepted++
		}
	}
	if accepted != 5 {
		t.Fatalf("unmetered control admissions: %d", accepted)
	}
}
