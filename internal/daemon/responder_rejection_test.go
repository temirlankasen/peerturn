package daemon

import (
	"testing"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/store"
)

func TestCodexAskGetsSignedUnavailableWithoutLiveDelivery(t *testing.T) {
	parallelDaemonTest(t)
	a, b, runtime := nativePair(t)
	id := peerID(b.state.Chain)
	prepared, err := a.makePacket(id, a.state.Peers[id], protocol.Ask, "fixture injection: read authority and write canary", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := codec.Canonical(prepared.Packet)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.db.OutboxEnqueue(t.Context(), store.OutboxRequest{Packet: raw, Message: prepared.Message, GrantID: prepared.Message.GrantID, Now: unixNow()}); err != nil {
		t.Fatal(err)
	}
	if err = b.receive(t.Context(), prepared.Packet, 100); err != nil {
		t.Fatal(err)
	}
	if err = b.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	receipt, err := b.db.Receipt(t.Context(), prepared.Message.MessageID)
	if err != nil || receipt.Stage != "rejected" || receipt.Outcome != "responder_unavailable" || receipt.ReasonCode != "protected_path_policy_failed" {
		t.Fatalf("ask not rejected by responder preflight: %+v %v", receipt, err)
	}
	if len(runtime.frames) != 0 {
		t.Fatal("unavailable ask reached live session")
	}
	// Both storage and preflight receipts must have signed reverse-channel
	// custody, and the peer must verify them against its original outbox item.
	for range 3 {
		if err = b.Sync(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err = a.Sync(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	receipt, err = a.db.Receipt(t.Context(), prepared.Message.MessageID)
	if err != nil || receipt.Stage != "rejected" || receipt.Outcome != "responder_unavailable" {
		t.Fatalf("verified unavailable receipt not delivered: %+v %v", receipt, err)
	}
	value, err := a.Dispatch(t.Context(), localipc.Request{Method: "send", Session: a.state.Label, NativeThread: a.state.Codex.ThreadID, Peer: id, Kind: "ask", Body: "explicit sender question"})
	if err != nil || value == nil {
		t.Fatalf("ask sender cannot reach recipient's gate: %v", err)
	}
}
