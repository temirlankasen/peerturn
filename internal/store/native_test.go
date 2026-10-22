package store

import (
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"testing"
)

func TestNativePendingUsesSenderFIFOAndRetainsSignedPacket(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	commitOne(t, s, f, 1)
	commitOne(t, s, f, 2)
	items, err := s.NativePending(t.Context(), 10)
	if err != nil || len(items) != 2 {
		t.Fatalf("pending: %v %v", items, err)
	}
	p, err := protocol.DecodePacket(items[0].Packet)
	if err != nil || p.Header.MessageID != f.message.MessageID {
		t.Fatalf("retained packet: %+v %v", p, err)
	}
	if err = s.GrantRevokeLocal(t.Context(), "grant_one", 1, testNow); err != nil {
		t.Fatal(err)
	}
	items, err = s.NativePending(t.Context(), 10)
	if err != nil || len(items) != 0 {
		t.Fatalf("revoked exposed: %v %v", items, err)
	}
}

func TestNoWakeOfferCannotConsumeLiveInput(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	commitOne(t, s, f, 1)
	r := protocol.ReceiptPayload{MessageID: f.message.MessageID, AttemptID: f.message.MessageID, Stage: "offered", Outcome: "offered", EvidenceKind: "hook_notice", At: testNow}
	if err := s.InformResults(t.Context(), []InformResult{{Receipt: r}}); !fault.IsCode(err, fault.ScopeDenied) {
		t.Fatalf("live offer: %v", err)
	}
}
