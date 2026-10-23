package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// The sender revoked our outbound grant while its own inbound items remain pending.
func TestClaudePeerOutboundRevokeDoesNotWedgeOtherPeers(t *testing.T) {
	parallelDaemonTest(t)
	for _, kind := range []protocol.Kind{protocol.Coauthor, protocol.Inform} {
		t.Run(string(kind), func(t *testing.T) {
			admin := routingKey(t)
			f := newRelayFixture(t, admin)
			a, b, c := openRecoveryDaemon(t, f, admin), openRecoveryDaemon(t, f, routingKey(t)), openRecoveryDaemon(t, f, routingKey(t))
			attachPair(t, a, b)
			attachPair(t, a, c)
			instance := bindClaude(t, a)
			var ids []string
			for i, sender := range []*Daemon{b, c} {
				p, err := sender.makePacket(peerID(a.state.Chain), sender.state.Peers[peerID(a.state.Chain)], kind, "private-body", nil)
				if err != nil {
					t.Fatal(err)
				}
				if err = a.receive(t.Context(), p.Packet, uint64(i+1)); err != nil {
					t.Fatal(err)
				}
				ids = append(ids, p.Message.MessageID)
			}
			a.state.Peers[peerID(b.state.Chain)].Revoked = true
			for range 3 {
				r := localipc.Request{Method: "adapter.poll", Instance: instance}
				if kind == protocol.Inform {
					r = boundaryRequest(a, instance)
				}
				v, err := a.Dispatch(t.Context(), r)
				if err != nil {
					t.Fatalf("one peer wedged adapter: %v", err)
				}
				if kind == protocol.Coauthor {
					event, ok := v.(adapterEvent)
					if !ok || event.Type != "live" && a.claudeAttempt == nil {
						t.Fatalf("healthy peer starved: %v", v)
					}
					if event.Type == "live" && event.MessageID != ids[1] {
						t.Fatalf("bad peer admitted: %v", v)
					}
				} else if notice, ok := v.(map[string]string); !ok || !strings.Contains(notice["context"], "1 inform") || strings.Contains(notice["context"], "private-body") {
					t.Fatalf("healthy notice missing or body leaked: %v", v)
				}
			}
			receipt, err := a.db.Receipt(t.Context(), ids[0])
			if err != nil || receipt.Stage != "rejected" || receipt.ReasonCode != "revoked" {
				t.Fatalf("per-item terminal receipt: %+v %v", receipt, err)
			}
		})
	}
}

func TestClaudeExpiredOutgoingIsOnlyItsOwnFailure(t *testing.T) {
	parallelDaemonTest(t)
	admin := routingKey(t)
	f := newRelayFixture(t, admin)
	a, b, c := openRecoveryDaemon(t, f, admin), openRecoveryDaemon(t, f, routingKey(t)), openRecoveryDaemon(t, f, routingKey(t))
	attachPair(t, a, b)
	attachPair(t, a, c)
	instance := bindClaude(t, a)
	bad, good := incomingData(t, b, a), incomingData(t, c, a)
	if err := a.receive(t.Context(), bad.Packet, 1); err != nil {
		t.Fatal(err)
	}
	if err := a.receive(t.Context(), good.Packet, 2); err != nil {
		t.Fatal(err)
	}
	p := a.state.Peers[peerID(b.state.Chain)]
	g, _, err := a.outgoing(p)
	if err != nil {
		t.Fatal(err)
	}
	g.NotBefore, g.ExpiresAt = unixNow()-2, unixNow()-1
	p.Outgoing, err = grant.Issue(g, b.signingKey())
	if err != nil {
		t.Fatal(err)
	}
	v, err := a.Dispatch(t.Context(), localipc.Request{Method: "adapter.poll", Instance: instance})
	event, ok := v.(adapterEvent)
	if err != nil || !ok || event.Type != "live" || event.MessageID != good.Message.MessageID {
		t.Fatalf("expired peer blocked other item: %v %v", v, err)
	}
	r, err := a.db.Receipt(t.Context(), bad.Message.MessageID)
	if err != nil || r.Stage != "rejected" || r.ReasonCode != "expired" {
		t.Fatalf("wrong per-item terminal outcome: %+v %v", r, err)
	}
}

func boundaryRequest(d *Daemon, instance string) localipc.Request {
	return localipc.Request{Method: "adapter.boundary", Instance: instance, NativeSession: d.state.NativeSessionID, NativeSocket: d.claudeBinding.Socket}
}

func TestClaudeUnauthorizedPendingReopensWithoutAmbiguity(t *testing.T) {
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
	if _, err := a.Dispatch(t.Context(), localipc.Request{Method: "adapter.poll", Instance: instance}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Dispatch(t.Context(), localipc.Request{Method: "adapter.close", Instance: instance}); err != nil {
		t.Fatal(err)
	}
	instance = bindClaude(t, a)
	uncertain, err := a.db.UncertainDeliveries(t.Context())
	if err != nil || uncertain {
		t.Fatalf("never-authorized frame fenced: %v %v", uncertain, err)
	}
	v, err := a.Dispatch(t.Context(), localipc.Request{Method: "adapter.poll", Instance: instance})
	event, ok := v.(adapterEvent)
	if err != nil || !ok || event.Type != "live" || event.MessageID != p.Message.MessageID {
		t.Fatalf("not reeligible: %v %v", v, err)
	}
}

func TestClaudeAcceptedDigestAndMismatchFence(t *testing.T) {
	parallelDaemonTest(t)
	for _, match := range []bool{true, false} {
		t.Run(map[bool]string{true: "match", false: "mismatch"}[match], func(t *testing.T) {
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
			digest := sha256.Sum256([]byte(event.Text))
			r.Method, r.Outcome, r.Nonce = "adapter.result", "accepted", hex.EncodeToString(digest[:])
			if !match {
				r.Nonce = strings.Repeat("0", 64)
			}
			_, err = a.Dispatch(t.Context(), r)
			if match {
				receipt, receiptErr := a.db.Receipt(t.Context(), event.MessageID)
				if err != nil || receiptErr != nil || receipt.Stage != "adapter_accepted" {
					t.Fatalf("acceptance %+v %v %v", receipt, err, receiptErr)
				}
				r.Method = "adapter.authorize"
				settled, settledErr := a.Dispatch(t.Context(), r)
				refused, valid := settled.(map[string]bool)
				if settledErr != nil || !valid || refused["authorized"] {
					t.Fatalf("late poll frame not retired: %v %v", settled, settledErr)
				}
				r.Method = "adapter.release"
				if _, settledErr = a.Dispatch(t.Context(), r); settledErr != nil {
					t.Fatalf("lost refusal response unbound session: %v", settledErr)
				}
			} else if err == nil || a.claudeAttempt == nil {
				t.Fatalf("mismatch lost fence: %v", err)
			}
		})
	}
}
