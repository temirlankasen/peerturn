package daemon

import (
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/store"
)

func TestRevokeFlowKeepsOtherPeerMailboxFlowing(t *testing.T) {
	admin := routingKey(t)
	relay := newRelayFixture(t, admin)
	a := newDaemonFixture(t, relay, "a", admin)
	b := newDaemonFixture(t, relay, "b", routingKey(t))
	c := newDaemonFixture(t, relay, "c", routingKey(t))
	for _, joiner := range []*daemonFixture{b, c} {
		var invitation struct{ URL string }
		a.call(t, localipc.Request{Method: "invite", Scope: "coauthor"}, &invitation)
		joiner.call(t, localipc.Request{Method: "join", Scope: "coauthor", Link: invitation.URL}, nil)
	}
	var ap, bp, cp []Peer
	eventually(t, func() bool {
		a.call(t, localipc.Request{Method: "peers"}, &ap)
		b.call(t, localipc.Request{Method: "peers"}, &bp)
		c.call(t, localipc.Request{Method: "peers"}, &cp)
		return len(ap) == 2 && ap[0].State == "connected" && ap[1].State == "connected" && len(bp) == 1 && bp[0].State == "connected" && len(cp) == 1 && cp[0].State == "connected"
	})
	var bStatus Status
	b.call(t, localipc.Request{Method: "status"}, &bStatus)
	var toB string
	for _, peer := range ap {
		if peer.Fingerprint == bStatus.Fingerprint {
			toB = peer.ID
		}
	}
	var before protocol.ReceiptPayload
	a.call(t, localipc.Request{Method: "send", Peer: toB, Kind: "coauthor", Body: "revoke target"}, &before)
	eventually(t, func() bool {
		a.call(t, localipc.Request{Method: "receipt", MessageID: before.MessageID}, &before)
		return before.Stage == "receiver_stored"
	})
	var grants []store.GrantInfo
	b.call(t, localipc.Request{Method: "grants"}, &grants)
	for _, grant := range grants {
		if grant.Issuer.IdentityID == bStatus.Fingerprint {
			b.call(t, localipc.Request{Method: "revoke", GrantID: grant.GrantID}, nil)
		}
	}
	eventually(t, func() bool {
		a.call(t, localipc.Request{Method: "peers"}, &ap)
		for _, peer := range ap {
			if peer.ID == toB {
				return peer.State == "revoked"
			}
		}
		return false
	})
	for _, sender := range []*daemonFixture{a, b} {
		peer := toB
		if sender == b {
			peer = bp[0].ID
		}
		_, err := sender.client.Call(t.Context(), localipc.Request{Method: "send", Session: sender.label, Peer: peer, Kind: "coauthor", Body: "post-revoke denied"})
		if safeCode(err) != "revoked" {
			t.Fatalf("post-revoke error: %v", err)
		}
	}
	var after protocol.ReceiptPayload
	c.call(t, localipc.Request{Method: "send", Peer: cp[0].ID, Kind: "coauthor", Body: "healthy peer after revoke"}, &after)
	eventually(t, func() bool {
		c.call(t, localipc.Request{Method: "receipt", MessageID: after.MessageID}, &after)
		return after.Stage == "receiver_stored"
	})
	var inbox InboxPage
	a.call(t, localipc.Request{Method: "inbox"}, &inbox)
	if len(inbox.Messages) != 1 || inbox.Messages[0].MessageID != after.MessageID {
		t.Fatalf("revoke wedged mailbox or admitted denied data: %+v", inbox)
	}
}
