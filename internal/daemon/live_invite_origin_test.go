package daemon

import (
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func TestClaudeLivePeerLinkCannotAuthorizeJoin(t *testing.T) {
	parallelDaemonTest(t)
	a, b := pairedStores(t)
	instance := bindClaude(t, a)
	link := syntheticJoinLink("foreign.invalid", "A")
	p, err := b.makePacket(peerID(a.state.Chain), b.state.Peers[peerID(a.state.Chain)], protocol.Coauthor, link, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.receive(t.Context(), p.Packet, 1); err != nil {
		t.Fatal(err)
	}
	result, err := a.Dispatch(t.Context(), localipc.Request{Method: "adapter.poll", Instance: instance})
	if err != nil {
		t.Fatal(err)
	}
	live, ok := result.(adapterEvent)
	if !ok || live.Type != "live" {
		t.Fatal("fixture did not deliver the coauthor frame")
	}
	if _, err = a.join(t.Context(), link); localipc.PublicError(err).Code != "peer_invitation_denied" {
		t.Fatalf("live coauthor link acquired authority: %v", err)
	}
}

func TestCodexQueuedPeerLinkCannotAuthorizeJoin(t *testing.T) {
	parallelDaemonTest(t)
	a, b, runtime := nativePair(t)
	link := syntheticJoinLink("foreign.invalid", "A")
	nativeReceipt(t, a, localipc.Request{Method: "send", Peer: a.peers()[0].ID, Kind: "coauthor", Body: link})
	if err := b.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(runtime.frames) != 1 {
		t.Fatal("fixture did not queue the coauthor frame")
	}
	if _, err := b.join(t.Context(), link); localipc.PublicError(err).Code != "peer_invitation_denied" {
		t.Fatalf("queued coauthor link acquired authority: %v", err)
	}
}
