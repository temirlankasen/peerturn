package daemon

import (
	"testing"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func TestParkedRevokeAndStaleJournalRestorePeerRevocation(t *testing.T) {
	parallelDaemonTest(t)
	a, b := pairedStores(t)
	aid, bid := peerID(b.state.Chain), peerID(a.state.Chain)
	outbound := incomingData(t, a, b)
	a.state.Reserved = &outbound
	a.state.Sequences[aid]++
	if err := a.save(); err != nil {
		t.Fatal(err)
	}
	if err := a.recoverReserved(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := b.receive(t.Context(), outbound.Packet, 1); err != nil {
		t.Fatal(err)
	}
	var first protocol.EncryptedPacket
	for id := range b.state.Pending {
		raw, err := b.db.OutboxPacket(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		first, err = protocol.DecodePacket(raw)
		if err != nil {
			t.Fatal(err)
		}
	}
	body, err := codec.Canonical(protocol.ReceiptPayload{MessageID: outbound.Message.MessageID, AttemptID: outbound.Message.MessageID, Stage: "revoked", EvidenceKind: "local_revoke", ReasonCode: "revoked", At: unixNow()})
	if err != nil {
		t.Fatal(err)
	}
	revoked, err := b.makePacket(bid, b.state.Peers[bid], protocol.Receipt, string(body), nil)
	if err != nil || revoked.Message.SenderSeq != 2 {
		t.Fatal("fixture did not allocate receipt sequence2", err)
	}
	if err = a.receive(t.Context(), revoked.Packet, 1); err != nil {
		t.Fatal(err)
	}
	if a.state.Peers[aid].Revoked {
		t.Fatal("future receipt was not parked")
	}
	assertDaemonCatalogRevoked(t, a, outbound.Message.GrantID, false)
	if err = a.receive(t.Context(), first, 2); err != nil {
		t.Fatal(err)
	}
	receipt, err := a.db.Receipt(t.Context(), outbound.Message.MessageID)
	if err != nil || receipt.Stage != "revoked" || !a.state.Peers[aid].Revoked {
		t.Fatalf("promoted revoke left peer active: %+v %v", receipt, err)
	}
	assertDaemonCatalogRevoked(t, a, outbound.Message.GrantID, true)
	// Model a crash after receipt commit but before its peer flag was journaled.
	a.state.Peers[aid].Revoked = false
	if err = a.save(); err != nil {
		t.Fatal(err)
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), a.options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := reopened.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	if !reopened.state.Peers[aid].Revoked {
		t.Fatal("stale journal lost durable peer revocation")
	}
	assertDaemonCatalogRevoked(t, reopened, outbound.Message.GrantID, true)
}
