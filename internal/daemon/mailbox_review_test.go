package daemon

import (
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func TestUnavailableReturnChannelDoesNotWedgeMailbox(t *testing.T) {
	for _, reason := range []string{"revoked", "expired", "invalid"} {
		t.Run(reason, func(t *testing.T) {
			a, b := pairedStores(t)
			_, c := pairedStores(t)
			attachPair(t, a, c)
			ap := a.state.Peers[peerID(b.state.Chain)]
			switch reason {
			case "revoked":
				ap.Revoked = true
			case "expired":
				now := unixNow()
				expires := now + 2
				signed, err := grant.Issue(grant.Grant{Version: 1, GrantID: randomID(), Issuer: b.cert.Binding, Subject: a.cert.Binding, Scope: grant.Coauthor, GrantEpoch: 1, NotBefore: now, ExpiresAt: expires, Limits: grant.DefaultLimits(), ReplyRoute: &a.cert.Binding}, b.signingKey())
				if err != nil {
					t.Fatal(err)
				}
				ap.Outgoing = signed
				if _, _, err = a.outgoing(ap); err != nil {
					t.Fatal("fixture grant was never valid", err)
				}
				timer := time.NewTimer(time.Until(time.Unix(expires, 0)))
				defer timer.Stop()
				<-timer.C
			case "invalid":
				ap.Outgoing = "invalid"
			}
			first := incomingData(t, b, a)
			for range 3 {
				if err := a.receive(t.Context(), first.Packet, 1); err != nil {
					t.Fatalf("return channel blocked transport custody: %v", err)
				}
			}
			second := incomingData(t, c, a)
			if err := a.receive(t.Context(), second.Packet, 2); err != nil {
				t.Fatal(err)
			}
			frontier, err := a.db.AckFrontier(t.Context(), a.mailbox())
			if err != nil || frontier != 2 {
				t.Fatalf("mailbox wedged: %d %v", frontier, err)
			}
			items, err := a.db.ListInbox(t.Context(), 10)
			if err != nil || len(items) != 1 || items[0].MessageID != second.Message.MessageID {
				t.Fatalf("healthy peer lost behind failed receipt: %+v %v", items, err)
			}
			rejections, err := a.db.Rejections(t.Context())
			if err != nil || len(rejections) != 1 || rejections[0].Count != 1 {
				t.Fatalf("missing bounded rejection: %+v %v", rejections, err)
			}
			if a.state.Reserved != nil || a.state.Sequences[peerID(b.state.Chain)] != 0 {
				t.Fatal("unavailable receipt spent a sequence")
			}
		})
	}
}

func TestRejectedAdmissionDoesNotBuildReturnReceipt(t *testing.T) {
	a, b := pairedStores(t)
	id := peerID(b.state.Chain)
	ap := a.state.Peers[id]
	g, err := a.incomingGrant(ap)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.db.GrantRevoke(t.Context(), g.GrantID, 1, unixNow()); err != nil {
		t.Fatal(err)
	}
	ap.Outgoing = "invalid"
	packet := incomingData(t, b, a)
	if err = a.receive(t.Context(), packet.Packet, 1); err != nil {
		t.Fatalf("receipt construction preceded admission: %v", err)
	}
	rejections, err := a.db.Rejections(t.Context())
	if err != nil || len(rejections) != 1 || rejections[0].Reason != fault.Revoked {
		t.Fatalf("wrong admission reason: %+v %v", rejections, err)
	}
}

func attachPair(t *testing.T, a, b *Daemon) {
	t.Helper()
	ag, err := a.issueGrant(b.cert)
	if err != nil {
		t.Fatal(err)
	}
	bg, err := b.issueGrant(a.cert)
	if err != nil {
		t.Fatal(err)
	}
	a.state.Peers[peerID(b.state.Chain)] = &peerState{Chain: b.state.Chain, RoutingKey: b.routingPublic(), Incoming: ag, Outgoing: bg, HelloReceived: true, HelloSent: true}
	b.state.Peers[peerID(a.state.Chain)] = &peerState{Chain: a.state.Chain, RoutingKey: a.routingPublic(), Incoming: bg, Outgoing: ag, HelloReceived: true, HelloSent: true}
	for _, local := range []*Daemon{a, b} {
		if err = local.installGrant(t.Context(), ag, a.state.Chain, b.state.Chain); err != nil {
			t.Fatal(err)
		}
		if err = local.installGrant(t.Context(), bg, b.state.Chain, a.state.Chain); err != nil {
			t.Fatal(err)
		}
	}
}

func incomingData(t *testing.T, from, to *Daemon) reservedPacket {
	t.Helper()
	id := peerID(to.state.Chain)
	r, err := from.makePacket(id, from.state.Peers[id], protocol.Coauthor, "literal data", nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
