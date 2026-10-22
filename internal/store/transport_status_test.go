package store

import (
	"database/sql"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func TestTransportRelayStorageAdvancesOnlyStorageEvidence(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	r := outgoingRequest(t, f, 1)
	if exists, err := s.OutboxExists(t.Context(), r.Message.MessageID); err != nil || exists {
		t.Fatal("missing packet reported retained", err)
	}
	if _, err := s.OutboxEnqueue(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkRelayed(t.Context(), r.Message.MessageID, testNow); err != nil {
		t.Fatal(err)
	}
	if exists, err := s.OutboxExists(t.Context(), r.Message.MessageID); err != nil || !exists {
		t.Fatal("relayed original lost retry identity", err)
	}
	if items, err := s.PendingOutbox(t.Context(), 10); err != nil || len(items) != 0 {
		t.Fatal("relayed data still queued", err)
	}
	receipt, err := s.Receipt(t.Context(), r.Message.MessageID)
	if err != nil || receipt.Stage != "relay_stored" || receipt.EvidenceKind != "relay_commit" {
		t.Fatalf("relay evidence: %+v %v", receipt, err)
	}
	receipt.Stage, receipt.EvidenceKind = "expired", "local_expiry"
	if err = s.RecordReceipt(t.Context(), receipt); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkRelayed(t.Context(), r.Message.MessageID, testNow+1); err != nil {
		t.Fatal(err)
	}
	receipt, err = s.Receipt(t.Context(), r.Message.MessageID)
	if err != nil || receipt.Stage != "expired" {
		t.Fatal("late relay response overwrote terminal decision", err)
	}
}

func TestTransportRelayedControlsNeedNoGrantReceiptChannel(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	control, err := f.receipt(protocol.ReceiptPayload{MessageID: f.message.MessageID, AttemptID: f.message.MessageID, Stage: "receiver_stored", EvidenceKind: "peer_commit", At: testNow})
	if err != nil {
		t.Fatal(err)
	}
	packet, err := protocol.DecodePacket(control)
	if err != nil {
		t.Fatal(err)
	}
	id := packet.Header.MessageID
	if err = s.write(t.Context(), func(tx *sql.Tx) error {
		_, putErr := s.putOutbox(t.Context(), tx, id, "grant_one", control, "control", testNow+100, testNow)
		return putErr
	}); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkRelayed(t.Context(), id, testNow); err != nil {
		t.Fatal(err)
	}
	if items, readErr := s.PendingOutbox(t.Context(), 10); readErr != nil || len(items) != 0 {
		t.Fatal("relayed control remained queued", readErr)
	}
	if exists, readErr := s.OutboxExists(t.Context(), id); readErr != nil || !exists {
		t.Fatal("control retry identity lost after relay storage", readErr)
	}
	if _, readErr := s.Receipt(t.Context(), id); !fault.IsCode(readErr, NotFound) {
		t.Fatal("relay control created data receipt evidence", readErr)
	}
}

func TestTransportRevokePendingUsesExistingAffectedMessages(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	commitOne(t, s, f, 1)
	commitOne(t, s, f, 3)
	ids, err := s.RevokePending(t.Context(), "grant_one")
	if err != nil || len(ids) != 2 {
		t.Fatalf("pending and parked revoke set: %v %v", ids, err)
	}
	if err = s.GrantRevoke(t.Context(), "grant_one", 1, testNow, f.receipt); err != nil {
		t.Fatal(err)
	}
	ids, err = s.RevokePending(t.Context(), "grant_one")
	if err != nil || len(ids) != 0 {
		t.Fatalf("quarantined messages reappeared as pending: %v %v", ids, err)
	}
	if _, err = s.RevokePending(t.Context(), "missing"); !fault.IsCode(err, NotFound) {
		t.Fatal("unknown grant reported empty revoke set", err)
	}
}
