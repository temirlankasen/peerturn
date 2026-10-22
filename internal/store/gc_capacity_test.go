package store

import (
	"testing"

	"github.com/temirlankasen/peerturn/internal/protocol"
)

func TestGCRestoresReceiptOutboxCapacity(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	// Completed receipt traffic previously exhausted the control lane forever.
	if _, err := s.db.ExecContext(t.Context(), `WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<8192)
 INSERT INTO outbox(id,grant_id,digest,packet,lane,stage,expires_at,bytes,created_at)
 SELECT printf('a%031d',i),'grant_one','',X'','control','relay_stored',0,0,0 FROM n`); err != nil {
		t.Fatal(err)
	}
	if err := s.Expire(t.Context(), testNow+301); err != nil {
		t.Fatal(err)
	}
	got := commitOne(t, s, f, 1)
	if got.Stage != "receiver_stored" || got.Frontier != 1 {
		t.Fatalf("receipt retention still blocks data custody: %+v", got)
	}
}

func TestGCRestoresDeliveredInboxBytes(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	// A full historical byte allowance must not become a lifetime traffic cap.
	if _, err := s.db.ExecContext(t.Context(), `WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<1024)
 INSERT INTO inbox(id,channel,grant_id,epoch,sender_seq,digest,packet,body,route,state,expires_at,bytes,created_at,lane)
 SELECT printf('d%031d',i),'fill_channel','grant_one',1,i,'',X'',X'','live','adapter_accepted',0,65536,0,'data' FROM n`); err != nil {
		t.Fatal(err)
	}
	if err := s.Expire(t.Context(), testNow+301); err != nil {
		t.Fatal(err)
	}
	var used int
	if err := s.db.QueryRowContext(t.Context(), `SELECT COALESCE(SUM(bytes),0) FROM inbox`).Scan(&used); err != nil || used != 0 {
		t.Fatalf("historical bytes retained: %d %v", used, err)
	}
	if got := commitOne(t, s, f, 1); got.Stage != "receiver_stored" {
		t.Fatalf("delivered history still blocks data custody: %+v", got)
	}
}

func TestGCOutboxExpiryKeepsTerminalReceipt(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	r := outgoingRequest(t, f, 1)
	if _, err := s.OutboxEnqueue(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	if err := s.Expire(t.Context(), testNow+301); err != nil {
		t.Fatal(err)
	}
	late := protocol.ReceiptPayload{MessageID: r.Message.MessageID, AttemptID: r.Message.MessageID, Stage: "relay_stored", EvidenceKind: "relay_commit", At: testNow + 302}
	if err := s.RecordReceipt(t.Context(), late); err != nil {
		t.Fatal(err)
	}
	evidence, err := s.Receipt(t.Context(), r.Message.MessageID)
	if err != nil || evidence.Stage != "expired" {
		t.Fatalf("terminal expiry evidence regressed: %+v %v", evidence, err)
	}
	var stage string
	var used int
	if err = s.db.QueryRowContext(t.Context(), `SELECT stage,bytes FROM outbox WHERE id=?`, r.Message.MessageID).Scan(&stage, &used); err != nil || stage != "expired" || used != 0 {
		t.Fatalf("terminal outbox regressed: %q %d %v", stage, used, err)
	}
}

func TestGCKeepsAmbiguousOutboxMetadata(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	r := outgoingRequest(t, f, 1)
	if _, err := s.OutboxEnqueue(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	ambiguous := protocol.ReceiptPayload{MessageID: r.Message.MessageID, AttemptID: r.Message.MessageID, Stage: "in_doubt", EvidenceKind: "native_queue", At: testNow + 1}
	if err := s.RecordReceipt(t.Context(), ambiguous); err != nil {
		t.Fatal(err)
	}
	if err := s.Expire(t.Context(), retainUntil(r.Message.ExpiresAt, r.Now)+1); err != nil {
		t.Fatal(err)
	}
	evidence, err := s.Receipt(t.Context(), r.Message.MessageID)
	if err != nil || evidence.Stage != "in_doubt" {
		t.Fatalf("GC lost ambiguous outbox evidence: %+v %v", evidence, err)
	}
	var stage string
	var used int
	if err = s.db.QueryRowContext(t.Context(), `SELECT stage,bytes FROM outbox WHERE id=?`, r.Message.MessageID).Scan(&stage, &used); err != nil || stage != "in_doubt" || used != 0 {
		t.Fatalf("ambiguous outbox metadata/body: %q %d %v", stage, used, err)
	}
}
