package store

import (
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func TestExpiredFirstCustodyAdvancesBothFrontiers(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	original := f.packet(t, 1)
	f.options.Now = testNow + 301
	r := InboxRequest{Packet: original, Mailbox: "mailbox_b", MailboxSeq: 1, GrantID: "grant_one", Open: f.options, Receipt: f.receipt}
	got, err := s.InboxCommit(t.Context(), r)
	if err != nil || got.Stage != "expired" || got.Frontier != 1 {
		t.Fatalf("first expired custody: %+v %v", got, err)
	}
	f.header.ExpiresAt = testNow + 500
	f.message.ExpiresAt = f.header.ExpiresAt
	f.message.CreatedAt = testNow + 301
	r.Packet = f.packet(t, 2)
	r.MailboxSeq = 2
	got, err = s.InboxCommit(t.Context(), r)
	if err != nil || got.Stage != "receiver_stored" || got.Frontier != 2 {
		t.Fatalf("expired sender gap: %+v %v", got, err)
	}
	item, err := s.Inbox(t.Context(), f.message.MessageID)
	if err != nil || item.State != "expired" || len(item.Body) != 0 {
		t.Fatalf("expired plaintext returned: %+v %v", item, err)
	}
}

func TestParkedControlNeverRepliesAndKeepsReserve(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	if err := s.write(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `UPDATE grants SET burst=5,burst_start=?,messages=10,minute_start=?`, testNow, testNow)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	f.message.Kind = protocol.Receipt
	body, err := json.Marshal(protocol.ReceiptPayload{MessageID: f.message.MessageID, AttemptID: f.message.MessageID, Stage: "receiver_stored", EvidenceKind: "peer_commit", At: testNow})
	if err != nil {
		t.Fatal(err)
	}
	f.message.BodyUTF8 = string(body)
	got, err := s.InboxCommit(t.Context(), InboxRequest{Packet: f.packet(t, 2), Mailbox: "mailbox_b", MailboxSeq: 2, GrantID: "grant_one", Open: f.options, Receipt: func(protocol.ReceiptPayload) ([]byte, error) { t.Error("response to parked receipt"); return nil, nil }})
	if err != nil || got.Stage != "sequence_gap" {
		t.Fatalf("control reserve: %+v %v", got, err)
	}
	item, err := s.Inbox(t.Context(), "00000000000000000000000000000002")
	if err != nil || item.Routing != "" || item.State != "parked" {
		t.Fatalf("parked control dispatched: %+v %v", item, err)
	}
	var count int
	if err = s.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM outbox`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("receipt storm: %d %v", count, err)
	}
}

func TestMailboxCustodyCompactionRestoresCapacity(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	packet := f.packet(t, 1)
	request := InboxRequest{Packet: packet, Mailbox: "mailbox_b", MailboxSeq: 1, GrantID: "grant_one", Open: f.options, Receipt: f.receipt}
	if _, err := s.InboxCommit(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if err := s.write(t.Context(), func(tx *sql.Tx) error {
		// Use the mailbox bound, rather than the unrelated 65K outbox ceiling.
		// Every retained slot is still present when the real collector runs.
		_, err := tx.ExecContext(t.Context(), `WITH RECURSIVE n(i) AS (VALUES(2) UNION ALL SELECT i+1 FROM n WHERE i<?) INSERT INTO dedup(key,message_id,digest,mailbox,mailbox_seq,state,retain_until) SELECT 'fill/'||i,'','', 'mailbox_b',i,'custody',? FROM n`, MaxMailboxRecords, testNow+1209600)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(t.Context(), `UPDATE dedup SET sender_seq=? WHERE key='frontier/mailbox_b'`, MaxMailboxRecords)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var count uint64
	if err := s.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM dedup WHERE mailbox='mailbox_b'`).Scan(&count); err != nil || count != MaxMailboxRecords {
		t.Fatalf("mailbox fixture did not reach its full retained bound: %d %v", count, err)
	}
	if err := s.Expire(t.Context(), retainUntil(f.header.ExpiresAt, testNow)+1); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM dedup WHERE mailbox='mailbox_b'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("contiguous custody was not compacted: %d %v", count, err)
	}
	got, err := s.RejectMailbox(t.Context(), "mailbox_b", MaxMailboxRecords+1, []byte("invalid-wire"), fault.InvalidEncoding, testNow+301)
	if err != nil || got.Frontier != MaxMailboxRecords+1 {
		t.Fatalf("mailbox capacity did not recover: %+v %v", got, err)
	}
}
