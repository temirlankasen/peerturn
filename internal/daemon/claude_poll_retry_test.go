package daemon

import (
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestClaudeLostPollResponseReoffersOnlyBeforeAuthorization(t *testing.T) {
	parallelDaemonTest(t)
	admin := routingKey(t)
	f := newRelayFixture(t, admin)
	a, b := openRecoveryDaemon(t, f, admin), openRecoveryDaemon(t, f, routingKey(t))
	attachPair(t, a, b)
	instance := bindClaude(t, a)
	p := incomingData(t, b, a)
	if err := a.receive(t.Context(), p.Packet, 1); err != nil {
		t.Fatal(err)
	}
	r := localipc.Request{Method: "adapter.poll", Instance: instance}
	v, err := a.Dispatch(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	first, ok := v.(adapterEvent)
	if !ok || first.Type != "live" {
		t.Fatal(v)
	}
	v, err = a.Dispatch(t.Context(), r)
	retry, ok := v.(adapterEvent)
	if err != nil || !ok || retry != first {
		t.Fatalf("lost poll response wedged intent: %v %v", v, err)
	}
	if _, err = a.Dispatch(t.Context(), localipc.Request{Method: "adapter.authorize", Instance: instance, MessageID: first.MessageID, AttemptID: first.AttemptID}); err != nil {
		t.Fatal(err)
	}
	v, err = a.Dispatch(t.Context(), r)
	after, ok := v.(adapterEvent)
	if err != nil || !ok || after.Type != "status" {
		t.Fatalf("authorized frame replayed: %v %v", v, err)
	}
}

func TestClaudeOversizedEscapedFrameIsRejectedBeforeIntent(t *testing.T) {
	parallelDaemonTest(t)
	admin := routingKey(t)
	f := newRelayFixture(t, admin)
	a, b, c := openRecoveryDaemon(t, f, admin), openRecoveryDaemon(t, f, routingKey(t)), openRecoveryDaemon(t, f, routingKey(t))
	attachPair(t, a, b)
	attachPair(t, a, c)
	instance := bindClaude(t, a)
	bad, err := b.makePacket(peerID(a.state.Chain), b.state.Peers[peerID(a.state.Chain)], "coauthor", strings.Repeat("\u0080", 22500), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.receive(t.Context(), bad.Packet, 1); err != nil {
		t.Fatal(err)
	}
	good := incomingData(t, c, a)
	if err = a.receive(t.Context(), good.Packet, 2); err != nil {
		t.Fatal(err)
	}
	v, err := a.Dispatch(t.Context(), localipc.Request{Method: "adapter.poll", Instance: instance})
	event, ok := v.(adapterEvent)
	if err != nil || !ok || event.Type != "live" || event.MessageID != good.Message.MessageID {
		t.Fatalf("oversized frame blocked valid peer: type=%T err=%v", v, err)
	}
	r, err := a.db.Receipt(t.Context(), bad.Message.MessageID)
	if err != nil || r.Stage != "rejected" || r.ReasonCode != "too_large" {
		t.Fatalf("oversized item not terminal: %+v %v", r, err)
	}
}

func TestClaudeUnconfirmedBoundaryPermitsBootstrap(t *testing.T) {
	parallelDaemonTest(t)
	admin := routingKey(t)
	d := openRecoveryDaemon(t, newRelayFixture(t, admin), admin)
	r := localipc.Request{Method: "adapter.open", NativeSession: "11111111-1111-4111-8111-111111111111", NativeSocket: "/tmp/unconfirmed.sock", RuntimeVersion: "2.1.306", Provider: "gateway"}
	if _, err := d.Dispatch(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	r.Method, r.Instance = "adapter.boundary", d.claudeBinding.Instance
	v, err := d.Dispatch(t.Context(), r)
	notice, ok := v.(map[string]string)
	if err != nil || !ok || notice["context"] != "" {
		t.Fatalf("first owner prompt killed unconfirmed helper: %v %v", v, err)
	}
	if _, err = d.Dispatch(t.Context(), localipc.Request{Method: "adapter.challenge", NativeSocket: r.NativeSocket}); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeOwnerRevokePendingDoesNotStarveOtherPeers(t *testing.T) {
	parallelDaemonTest(t)
	admin := routingKey(t)
	f := newRelayFixture(t, admin)
	a, b, c := openRecoveryDaemon(t, f, admin), openRecoveryDaemon(t, f, routingKey(t)), openRecoveryDaemon(t, f, routingKey(t))
	attachPair(t, a, b)
	attachPair(t, a, c)
	instance := bindClaude(t, a)
	p := incomingData(t, b, a)
	if err := a.receive(t.Context(), p.Packet, 1); err != nil {
		t.Fatal(err)
	}
	v, err := a.Dispatch(t.Context(), localipc.Request{Method: "adapter.poll", Instance: instance})
	if err != nil {
		t.Fatal(err)
	}
	event, ok := v.(adapterEvent)
	if !ok || event.Type != "live" {
		t.Fatal(v)
	}
	g, err := a.incomingGrant(a.state.Peers[peerID(b.state.Chain)])
	if err != nil {
		t.Fatal(err)
	}
	if err = a.db.GrantRevokeLocal(t.Context(), g.GrantID, g.GrantEpoch, unixNow()); err != nil {
		t.Fatal(err)
	}
	a.state.Peers[peerID(b.state.Chain)].IncomingRevoked = true
	v, err = a.Dispatch(t.Context(), localipc.Request{Method: "adapter.authorize", Instance: instance, MessageID: event.MessageID, AttemptID: event.AttemptID})
	refused, ok := v.(map[string]bool)
	if err != nil || !ok || refused["authorized"] {
		t.Fatalf("terminal revoke became adapter failure: %v %v", v, err)
	}
	good := incomingData(t, c, a)
	if err = a.receive(t.Context(), good.Packet, 2); err != nil {
		t.Fatal(err)
	}
	v, err = a.Dispatch(t.Context(), localipc.Request{Method: "adapter.poll", Instance: instance})
	event, ok = v.(adapterEvent)
	if err != nil || !ok || event.Type != "live" || event.MessageID != good.Message.MessageID {
		t.Fatalf("revoke starved other peer: %v %v", v, err)
	}
}
