package daemon

import (
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/store"
)

// A colleague who moves to a new Claude session keeps the same identity. The
// owner's next pasted invitation must rebuild that pair instead of failing on
// the old record, and both directions must flow again under the new grants.
func TestNewInvitationRelinksPreviouslyConnectedPeer(t *testing.T) {
	parallelDaemonTest(t)
	admin := routingKey(t)
	relay := newRelayFixture(t, admin)
	a := newDaemonFixture(t, relay, "a", admin)
	// Both sides invite in turn, so the relay admits both mailbox keys.
	bKey := routingKey(t)
	relay.cfg.AdminKeys = append(relay.cfg.AdminKeys, publicKey(bKey[32:]))
	relay.restart(t)
	b := newDaemonFixture(t, relay, "b", bKey)
	link := func(inviter, joiner *daemonFixture) (string, string) {
		var invitation struct{ URL string }
		inviter.call(t, localipc.Request{Method: "invite", Scope: "coauthor"}, &invitation)
		joiner.call(t, localipc.Request{Method: "join", Scope: "coauthor", Link: invitation.URL}, nil)
		var ip, jp []Peer
		eventually(t, func() bool {
			inviter.call(t, localipc.Request{Method: "peers"}, &ip)
			joiner.call(t, localipc.Request{Method: "peers"}, &jp)
			return len(ip) == 1 && len(jp) == 1 && ip[0].State == "connected" && jp[0].State == "connected"
		})
		return ip[0].ID, jp[0].ID
	}
	send := func(from, to *daemonFixture, peer, body string) {
		var receipt protocol.ReceiptPayload
		from.call(t, localipc.Request{Method: "send", Peer: peer, Kind: "coauthor", Body: body}, &receipt)
		eventually(t, func() bool {
			from.call(t, localipc.Request{Method: "receipt", MessageID: receipt.MessageID}, &receipt)
			return receipt.Stage == "receiver_stored"
		})
		var inbox InboxPage
		to.call(t, localipc.Request{Method: "inbox"}, &inbox)
		for _, item := range inbox.Messages {
			if item.MessageID == receipt.MessageID {
				return
			}
		}
		t.Fatalf("%q was not admitted", body)
	}
	toB, toA := link(a, b)
	send(a, b, toB, "first a->b")
	send(a, b, toB, "second a->b")
	send(b, a, toA, "first b->a")
	// The recipient's revoke also revokes its relay route, like a lost session.
	var grants []store.GrantInfo
	b.call(t, localipc.Request{Method: "grants"}, &grants)
	var status Status
	b.call(t, localipc.Request{Method: "status"}, &status)
	for _, g := range grants {
		if g.Issuer.IdentityID == status.Fingerprint {
			b.call(t, localipc.Request{Method: "revoke", GrantID: g.GrantID}, nil)
		}
	}
	eventually(t, func() bool {
		var ap []Peer
		a.call(t, localipc.Request{Method: "peers"}, &ap)
		return len(ap) == 1 && ap[0].State == "revoked"
	})
	for _, pair := range [][2]*daemonFixture{{a, b}, {b, a}} {
		toB, toA = link(pair[0], pair[1])
		if pair[0] == b {
			toA, toB = toB, toA
		}
		send(a, b, toB, "relinked a->b")
		send(b, a, toA, "relinked b->a")
		send(a, b, toB, "relinked again a->b")
		assertTwoLiveGrants(t, a)
		assertTwoLiveGrants(t, b)
	}
}

// A replaced link keeps no live grant: a later revoke of the new link finds
// only the current grants, so an old one would keep authorizing delivery.
func assertTwoLiveGrants(t *testing.T, d *daemonFixture) {
	t.Helper()
	var grants []store.GrantInfo
	d.call(t, localipc.Request{Method: "grants"}, &grants)
	live := 0
	for _, g := range grants {
		if !g.Revoked {
			live++
		}
	}
	if live != 2 {
		t.Fatalf("%d live grants after relink, want the current pair", live)
	}
}

// A packet queued under the superseded link is dropped by the relink. The
// receiver must skip that sequence instead of parking all later messages.
func TestRelinkSkipsSequenceLostWithOldLink(t *testing.T) {
	parallelDaemonTest(t)
	admin := routingKey(t)
	relay := newRelayFixture(t, admin)
	a := newDaemonFixture(t, relay, "a", admin)
	b := newDaemonFixture(t, relay, "b", routingKey(t))
	connect := func() (string, string) {
		var invitation struct{ URL string }
		a.call(t, localipc.Request{Method: "invite", Scope: "coauthor"}, &invitation)
		b.call(t, localipc.Request{Method: "join", Scope: "coauthor", Link: invitation.URL}, nil)
		var ap, bp []Peer
		eventually(t, func() bool {
			a.call(t, localipc.Request{Method: "peers"}, &ap)
			b.call(t, localipc.Request{Method: "peers"}, &bp)
			return len(ap) == 1 && len(bp) == 1 && ap[0].State == "connected" && bp[0].State == "connected"
		})
		return ap[0].ID, bp[0].ID
	}
	stored := func(from *daemonFixture, peer, body string) string {
		var receipt protocol.ReceiptPayload
		from.call(t, localipc.Request{Method: "send", Peer: peer, Kind: "coauthor", Body: body}, &receipt)
		eventually(t, func() bool {
			from.call(t, localipc.Request{Method: "receipt", MessageID: receipt.MessageID}, &receipt)
			return receipt.Stage == "receiver_stored"
		})
		return receipt.MessageID
	}
	_, toA := connect()
	stored(b, toA, "before")
	relay.holdMessages.Store(true)
	var lost protocol.ReceiptPayload
	b.call(t, localipc.Request{Method: "send", Peer: toA, Kind: "coauthor", Body: "lost with the old link"}, &lost)
	if lost.Stage != "sender_stored" {
		t.Fatalf("held packet reached %s", lost.Stage)
	}
	var invitation struct{ URL string }
	a.call(t, localipc.Request{Method: "invite", Scope: "coauthor"}, &invitation)
	b.call(t, localipc.Request{Method: "join", Scope: "coauthor", Link: invitation.URL}, nil)
	relay.holdMessages.Store(false)
	var bp []Peer
	eventually(t, func() bool {
		b.call(t, localipc.Request{Method: "peers"}, &bp)
		return len(bp) == 1 && bp[0].State == "connected"
	})
	after := stored(b, bp[0].ID, "after relink")
	var inbox InboxPage
	a.call(t, localipc.Request{Method: "inbox"}, &inbox)
	seen := map[string]bool{}
	for _, item := range inbox.Messages {
		seen[item.BodyUTF8] = item.MessageID == after || item.BodyUTF8 != "after relink"
	}
	if !seen["after relink"] || seen["before"] || seen["lost with the old link"] {
		t.Fatalf("relinked inbox: %+v", inbox.Messages)
	}
}
