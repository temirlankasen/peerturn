package daemon

import (
	"bytes"
	"testing"

	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/identity"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/store"
)

func assertSelfServiceE2EGrants(t *testing.T, local, remote *Daemon) {
	t.Helper()
	var grants []store.GrantInfo
	selfServiceE2ECall(t, local, localipc.Request{Method: "grants"}, &grants)
	if len(grants) != 2 {
		t.Fatal("handshake did not install exactly two directional grants")
	}
	p := local.state.Peers[peerID(remote.state.Chain)]
	r := remote.state.Peers[peerID(local.state.Chain)]
	if p == nil || r == nil || p.Incoming != r.Outgoing || p.Outgoing != r.Incoming {
		t.Fatal("directional grants disagree between synthetic peers")
	}
	incoming, outgoing := 0, 0
	for _, g := range grants {
		if g.Scope != grant.Coauthor || g.Epoch != 1 || g.Revoked || g.EffectiveRevoked || g.ExpiresAt <= unixNow() {
			t.Fatal("handshake installed unexpected grant policy")
		}
		if identity.EqualBinding(g.Issuer, local.cert.Binding) && identity.EqualBinding(g.Subject, remote.cert.Binding) && g.Hash == grant.Hash(p.Incoming) {
			incoming++
		}
		if identity.EqualBinding(g.Issuer, remote.cert.Binding) && identity.EqualBinding(g.Subject, local.cert.Binding) && g.Hash == grant.Hash(p.Outgoing) {
			outgoing++
		}
	}
	if incoming != 1 || outgoing != 1 {
		t.Fatal("grants do not bind both exact recipient-issued session/epoch directions")
	}
}

func selfServiceE2ECustody(t *testing.T, f *selfServiceE2ERelay, from, to *Daemon, body string) {
	t.Helper()
	var receipt protocol.ReceiptPayload
	selfServiceE2ECall(t, from, localipc.Request{Method: "send", Peer: peerID(to.state.Chain), Kind: "coauthor", Body: body}, &receipt)
	if receipt.Stage != "relay_stored" {
		t.Fatal("online send did not establish relay custody before recipient fetch")
	}
	id := receipt.MessageID
	var raw []byte
	err := f.db.QueryRowContext(t.Context(), "SELECT packet FROM messages WHERE mailbox_id=? AND message_id=?", to.mailbox(), id).Scan(&raw)
	if err != nil || len(raw) == 0 || bytes.Contains(raw, []byte(body)) {
		t.Fatal("real relay did not retain ciphertext without the private plaintext")
	}
	packet, err := protocol.DecodePacket(raw)
	if err != nil || packet.Header.MessageID != id || packet.Header.DstMailbox != to.mailbox() || packet.Header.RoutePermitID != routePermit(from.routingPublic()) || len(packet.Ciphertext) == 0 {
		t.Fatal("retained relay packet has invalid encrypted routing custody")
	}
	retained, err := from.db.OutboxExists(t.Context(), id)
	if err != nil || !retained {
		t.Fatal("relay storage ACK lost the durable sender message identity")
	}
	var inbox InboxPage
	selfServiceE2ECall(t, to, localipc.Request{Method: "inbox"}, &inbox)
	for _, item := range inbox.Messages {
		if item.MessageID == id {
			t.Fatal("recipient claimed custody before its scheduled fetch")
		}
	}
	selfServiceE2ESyncUntil(t, from, to, func() bool {
		selfServiceE2ECall(t, from, localipc.Request{Method: "receipt", MessageID: id}, &receipt)
		return receipt.Stage == "receiver_stored"
	})
	selfServiceE2ECall(t, to, localipc.Request{Method: "inbox"}, &inbox)
	count := 0
	for _, item := range inbox.Messages {
		if item.MessageID == id {
			count++
			if item.BodyUTF8 != body || item.Peer != peerID(from.state.Chain) || item.Stage != "receiver_stored" {
				t.Fatal("recipient custody changed literal text, peer or receipt stage")
			}
		}
	}
	if count != 1 {
		t.Fatalf("recipient custody admissions: got %d, want 1", count)
	}
	var acked bool
	err = f.db.QueryRowContext(t.Context(), "SELECT packet IS NULL FROM messages WHERE mailbox_id=? AND message_id=?", to.mailbox(), id).Scan(&acked)
	if err != nil || !acked {
		t.Fatal("receiver ACK did not release real relay ciphertext custody")
	}
}

func selfServiceE2ERevoke(t *testing.T, f *selfServiceE2ERelay, a, b *Daemon) {
	t.Helper()
	p := b.state.Peers[peerID(a.state.Chain)]
	g, err := b.incomingGrant(p)
	if err != nil {
		t.Fatal("missing recipient-issued grant to revoke:", safeCode(err))
	}
	selfServiceE2ECall(t, b, localipc.Request{Method: "revoke", GrantID: g.GrantID}, nil)
	if !p.IncomingRevoked || p.RevokeRoutePending {
		t.Fatal("local revoke or real relay route revoke is uncommitted")
	}
	var revoked bool
	var epoch int64
	err = f.db.QueryRowContext(t.Context(), "SELECT revoked,epoch FROM permits WHERE mailbox_id=? AND id=?", b.mailbox(), routePermit(a.routingPublic())).Scan(&revoked, &epoch)
	if err != nil || !revoked || epoch != 2 {
		t.Fatal("grant revoke did not fence the actual relay permit")
	}
	selfServiceE2ESyncUntil(t, a, b, func() bool {
		ap, bp := a.peers(), b.peers()
		return len(ap) == 1 && len(bp) == 1 && ap[0].State == "revoked" && bp[0].State == "revoked"
	})
	for _, d := range []*Daemon{a, b} {
		var grants []store.GrantInfo
		selfServiceE2ECall(t, d, localipc.Request{Method: "grants"}, &grants)
		observed := false
		for _, installed := range grants {
			if installed.GrantID == g.GrantID && installed.EffectiveRevoked {
				observed = true
			}
		}
		if !observed {
			t.Fatal("recipient revoke did not reach both authenticated grant catalogs")
		}
	}
	var before, after int
	if err = f.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM messages").Scan(&before); err != nil {
		t.Fatal("read pre-refusal relay custody count:", err)
	}
	for _, d := range []*Daemon{a, b} {
		peer := d.peers()[0].ID
		sequence, pending := d.state.Sequences[peer], len(d.state.Pending)
		_, err = d.Dispatch(t.Context(), localipc.Request{Method: "send", Session: d.state.Label, Peer: peer, Kind: "coauthor", Body: "synthetic-post-revoke-must-refuse"})
		if safeCode(err) != "revoked" {
			t.Fatal("post-revoke send was not refused:", safeCode(err))
		}
		if d.state.Reserved != nil || d.state.Sequences[peer] != sequence || len(d.state.Pending) != pending {
			t.Fatal("refused send reserved new local custody")
		}
	}
	if err = f.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM messages").Scan(&after); err != nil || before != after {
		t.Fatal("refused send created relay message custody")
	}
}
