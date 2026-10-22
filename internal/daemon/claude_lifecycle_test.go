package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/claude"
	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestClaudeLeaseRenewsBeforeSlowOperationLock(t *testing.T) {
	admin := routingKey(t)
	d := openRecoveryDaemon(t, newRelayFixture(t, admin), admin)
	old := time.Now().Add(-15 * time.Second)
	b, err := claude.NewBinding("11111111-1111-4111-8111-111111111111", "/tmp/lease.sock", "2.1.306", "gateway", old)
	if err != nil {
		t.Fatal(err)
	}
	nonce, err := b.Challenge(b.Socket, old)
	if err != nil {
		t.Fatal(err)
	}
	if err = b.Confirm(nonce, b.NativeSessionID, b.Instance, old); err != nil {
		t.Fatal(err)
	}
	d.claudeBinding = b
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	d.opMu.Lock()
	go func() {
		_, callErr := d.Dispatch(ctx, localipc.Request{Method: "adapter.poll", Instance: b.Instance})
		done <- callErr
	}()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	renewed := false
	for !renewed {
		d.mu.Lock()
		renewed = b.Ready(time.Now().Add(10 * time.Second))
		d.mu.Unlock()
		if renewed {
			break
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			cancel()
			d.opMu.Unlock()
			if callErr := <-done; callErr == nil {
				t.Error("timed-out probe unexpectedly completed")
			}
			t.Fatal("slow transport lock expired healthy helper lease")
		}
	}
	cancel()
	d.opMu.Unlock()
	if err = <-done; err == nil {
		t.Fatal("canceled poll mutated state after lock wait")
	}
}

func TestClaudeBoundaryRequiresCurrentInstance(t *testing.T) {
	admin := routingKey(t)
	d := openRecoveryDaemon(t, newRelayFixture(t, admin), admin)
	instance := bindClaude(t, d)
	r := boundaryRequest(d, instance)
	r.Instance = "replaced-helper"
	if _, err := d.Dispatch(t.Context(), r); localipc.PublicError(err).Code != "needs_rebind" {
		t.Fatalf("old boundary accepted: %v", err)
	}
	r.Instance = instance
	if _, err := d.Dispatch(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Dispatch(t.Context(), localipc.Request{Method: "adapter.open", NativeSession: d.state.NativeSessionID, NativeSocket: d.claudeBinding.Socket, RuntimeVersion: "2.1.306", Provider: "gateway"}); localipc.PublicError(err).Code != "already_running" {
		t.Fatalf("parallel helper accepted: %v", err)
	}
}

func TestClaudeUnconfirmedHelperReplacementAndResolve(t *testing.T) {
	admin := routingKey(t)
	d := openRecoveryDaemon(t, newRelayFixture(t, admin), admin)
	r := localipc.Request{Method: "adapter.open", NativeSession: "11111111-1111-4111-8111-111111111111", NativeSocket: "/tmp/unconfirmed.sock", RuntimeVersion: "2.1.306", Provider: "gateway"}
	if _, err := d.Dispatch(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	old := d.claudeBinding.Instance
	instance := bindClaude(t, d)
	if instance == old {
		t.Fatal("unconfirmed helper retained")
	}
	r = localipc.Request{Method: "adapter.resolve", NativeSocket: d.claudeBinding.Socket, Nonce: d.claudeBinding.ConfirmedNonce}
	v, err := d.Dispatch(t.Context(), r)
	resolved, ok := v.(map[string]string)
	if err != nil || !ok || resolved["session"] != d.state.Label || resolved["binding"] != "native_claude" {
		t.Fatalf("resolve failed: %v %v", v, err)
	}
	r.Nonce = strings.Repeat("0", 64)
	if _, err = d.Dispatch(t.Context(), r); err == nil {
		t.Fatal("unconfirmed nonce resolved native session")
	}
}

func TestClaudeUnbindInvalidatesAuthorityBeforeChangingSession(t *testing.T) {
	a, b := pairedStores(t)
	attachPair(t, a, b)
	bindClaude(t, a)
	if _, err := a.Dispatch(t.Context(), localipc.Request{Method: "session.unbind"}); err != nil {
		t.Fatal(err)
	}
	if a.claudeBinding != nil || a.state.NativeSessionID != "" {
		t.Fatal("binding survived unbind")
	}
	grants, err := a.db.ListGrants(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range grants {
		if !g.Revoked {
			t.Fatal("new native session could inherit old grant")
		}
	}
	p := a.state.Peers[peerID(b.state.Chain)]
	if _, _, err = a.outgoing(p); localipc.PublicError(err).Code != "revoked" {
		t.Fatalf("old outbound authority retained: %v", err)
	}
	v, err := a.Dispatch(t.Context(), localipc.Request{Method: "adapter.open", NativeSession: "22222222-2222-4222-8222-222222222222", NativeSocket: "/tmp/new.sock", RuntimeVersion: "2.1.306", Provider: "gateway"})
	if err != nil {
		t.Fatalf("unbind did not allow a fresh session: %v %v", v, err)
	}
}

func TestClaudeResolveIsOwnerOnlyAndReopensAdmission(t *testing.T) {
	for _, outcome := range []string{"sent", "not_sent"} {
		t.Run(outcome, func(t *testing.T) {
			admin := routingKey(t)
			f := newRelayFixture(t, admin)
			a, b := openRecoveryDaemon(t, f, admin), openRecoveryDaemon(t, f, routingKey(t))
			attachPair(t, a, b)
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
			if !ok {
				t.Fatal(v)
			}
			r := localipc.Request{Method: "adapter.authorize", Instance: instance, MessageID: event.MessageID, AttemptID: event.AttemptID}
			if _, err = a.Dispatch(t.Context(), r); err != nil {
				t.Fatal(err)
			}
			r.Method, r.Outcome = "adapter.result", "in_doubt"
			if _, err = a.Dispatch(t.Context(), r); err != nil {
				t.Fatal(err)
			}
			r = localipc.Request{Method: "delivery.resolve", MessageID: event.MessageID, Outcome: outcome, Session: a.state.Label, Capability: strings.Repeat("a", 64)}
			if _, err = a.Dispatch(t.Context(), r); err == nil {
				t.Fatal("MCP capability resolved owner decision")
			}
			r.Capability = ""
			if _, err = a.Dispatch(t.Context(), r); err != nil {
				t.Fatal(err)
			}
			late := localipc.Request{Method: "adapter.result", Instance: instance, MessageID: event.MessageID, AttemptID: event.AttemptID, Outcome: "in_doubt"}
			if _, err = a.Dispatch(t.Context(), late); err != nil {
				t.Fatalf("lost result ACK blocked reconciled session: %v", err)
			}
			uncertain, err := a.db.UncertainDeliveries(t.Context())
			if err != nil || uncertain {
				t.Fatalf("resolution retained fence: %v %v", uncertain, err)
			}
			v, err = a.Dispatch(t.Context(), localipc.Request{Method: "adapter.poll", Instance: instance})
			if err != nil {
				t.Fatal(err)
			}
			event, ok = v.(adapterEvent)
			if !ok || (outcome == "not_sent" && event.Type != "live") || (outcome == "sent" && event.Type == "live") {
				t.Fatalf("resolution eligibility wrong: %v", v)
			}
		})
	}
}
