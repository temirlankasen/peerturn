package daemon

import (
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestCodexSessionUnbindRefusesWithoutChangingAuthority(t *testing.T) {
	parallelDaemonTest(t)
	a, b, runtime := nativePair(t)
	r := nativeReceipt(t, a, localipc.Request{Method: "send", Peer: a.peers()[0].ID, Kind: "coauthor", Body: "queued while owner busy"})
	if err := b.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	before, err := b.db.Receipt(t.Context(), r.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []localipc.Request{
		{Method: "session.unbind"},
		{Method: "session.unbind", Session: b.state.Label, NativeThread: runtime.thread},
	} {
		if _, err = b.Dispatch(t.Context(), request); safeCode(err) != "unsupported" {
			t.Fatalf("Codex unbind must refuse: %v", err)
		}
	}
	after, err := b.db.Receipt(t.Context(), r.MessageID)
	if err != nil || after != before || b.state.Codex == nil || len(runtime.deleted) != 0 {
		t.Fatalf("unsupported unbind mutated native custody: %+v %v", after, err)
	}
	for _, p := range b.state.Peers {
		if p.Revoked || p.IncomingRevoked {
			t.Fatal("unsupported unbind revoked authority")
		}
	}
	// The supported Codex path must still delete the exact queued native item.
	revokeNativePeer(t, b, a)
	if len(runtime.deleted) != 1 || runtime.deleted[0] != "queue-"+r.MessageID {
		t.Fatalf("grants revoke lost native dequeue: %v", runtime.deleted)
	}
}

func revokeNativePeer(t *testing.T, receiver, sender *Daemon) {
	t.Helper()
	g, err := receiver.incomingGrant(receiver.state.Peers[peerID(sender.state.Chain)])
	if err != nil {
		t.Fatal(err)
	}
	nativeCall(t, receiver, localipc.Request{Method: "revoke", GrantID: g.GrantID})
}

func TestMutualRevokeCannotStarveThirdNativePeer(t *testing.T) {
	parallelDaemonTest(t)
	b, a, _ := nativePair(t)
	runtime, ok := b.native.(*nativeFixture)
	if !ok {
		t.Fatal("native fixture type")
	}
	c := thirdNativePeer(t, b)
	nativeReceipt(t, a, localipc.Request{Method: "send", Peer: a.peers()[0].ID, Kind: "coauthor", Body: "from a"})
	if err := b.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Data queued under the independent outgoing grant survives local incoming
	// revoke. It must neither be sent nor mistaken for a terminal control.
	relay := b.relay
	recoveryRelayOffline(t, b)
	stuck := nativeReceipt(t, b, localipc.Request{Method: "send", Peer: peerID(a.state.Chain), Kind: "coauthor", Body: "outgoing before revoke"})
	b.relay = relay
	revokeNativePeer(t, a, b)
	revokeNativePeer(t, b, a)
	closed := b.state.Peers[peerID(a.state.Chain)]
	if len(b.state.Pending) == 0 {
		t.Fatal("fixture needs terminal receipts on the now-closed route")
	}
	for i := range 3 {
		r := nativeReceipt(t, c, localipc.Request{Method: "send", Peer: c.peers()[0].ID, Kind: "coauthor", Body: "from c"})
		if err := b.Sync(t.Context()); err != nil {
			t.Fatalf("sync #%d blocked by closed peer: %v", i, err)
		}
		if len(runtime.ids) != i+2 || runtime.ids[i+1] != r.MessageID {
			t.Fatalf("third peer starved: %v", runtime.ids)
		}
		if err := c.Sync(t.Context()); err != nil {
			t.Fatal(err)
		}
		receipt, err := b.db.Receipt(t.Context(), r.MessageID)
		if err != nil || receipt.Stage != "adapter_accepted" {
			t.Fatalf("third peer native evidence lost: %+v %v", receipt, err)
		}
		for _, order := range b.state.Pending {
			if order.PeerID == peerID(c.state.Chain) {
				t.Fatal("second flush did not send third peer receipts")
			}
		}
	}
	if !closed.NotificationsUnavailable || closed.SequenceBlocked {
		t.Fatal("closed receipt route was not terminalized independently")
	}
	for id, order := range b.state.Pending {
		if order.PeerID == peerID(a.state.Chain) && id != stuck.MessageID {
			t.Fatal("denied closed-route receipt still retries")
		}
	}
	// The run loop records successful Sync this way; old sequence fences on a
	// closed route must not degrade the remaining live peers' aggregate status.
	b.verified, b.lastError = unixNow(), ""
	closed.Revoked, closed.SequenceBlocked = true, true
	status := nativeValue[Status](t, b, localipc.Request{Method: "status"})
	if status.State != "active" {
		t.Fatalf("closed peer poisoned status: %+v", status)
	}
}
