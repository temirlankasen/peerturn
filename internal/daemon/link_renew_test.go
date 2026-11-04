package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/relayclient"
	"github.com/temirlankasen/peerturn/internal/store"
)

// shortenGrant replaces b's grant to a by one with less than half its
// lifetime left, as if the link had been idle for half a month.
func shortenGrant(t *testing.T, a, b *Daemon) string {
	t.Helper()
	aid, bid := peerID(b.state.Chain), peerID(a.state.Chain)
	current, err := b.incomingGrant(b.state.Peers[bid])
	if err != nil {
		t.Fatal(err)
	}
	now := unixNow()
	current.GrantID, current.NotBefore, current.ExpiresAt = randomID(), now, now+grant.DefaultLifetime/2-60
	signed, err := grant.Issue(current, b.signingKey())
	if err != nil {
		t.Fatal(err)
	}
	if err = b.installGrant(t.Context(), signed, b.state.Chain, a.state.Chain); err != nil {
		t.Fatal(err)
	}
	if err = a.installGrant(t.Context(), signed, b.state.Chain, a.state.Chain); err != nil {
		t.Fatal(err)
	}
	b.state.Peers[bid].Incoming, a.state.Peers[aid].Outgoing = signed, signed
	return signed
}

func commitData(t *testing.T, from, to *Daemon, body string) reservedPacket {
	t.Helper()
	id := peerID(to.state.Chain)
	r, err := from.makePacket(id, from.state.Peers[id], protocol.Coauthor, body, nil)
	if err != nil {
		t.Fatal(err)
	}
	from.state.Reserved = &r
	from.state.Sequences[id]++
	if err = from.save(); err != nil {
		t.Fatal(err)
	}
	if err = from.recoverReserved(t.Context()); err != nil {
		t.Fatal(err)
	}
	return r
}

// A used link renews the recipient's grant before it lapses. Packets sealed
// under the old grant stay admissible, the sender switches after the update,
// and revoking either grant ends the link.
func TestUsedLinkRenewsGrantWithoutLosingMessages(t *testing.T) {
	parallelDaemonTest(t)
	a, b := pairedStores(t)
	aid, bid := peerID(b.state.Chain), peerID(a.state.Chain)
	old := shortenGrant(t, a, b)
	first := commitData(t, a, b, "before renewal")
	inFlight := commitData(t, a, b, "sealed under the old grant")
	if err := b.receive(t.Context(), first.Packet, 1); err != nil {
		t.Fatal(err)
	}
	bp := b.state.Peers[bid]
	if bp.Incoming == old || bp.PriorIncoming != old || bp.Update == nil {
		t.Fatal("use did not renew the grant")
	}
	renewed, err := b.incomingGrant(bp)
	if err != nil || renewed.ExpiresAt-unixNow() < grant.DefaultLifetime-60 {
		t.Fatalf("renewed grant %+v: %v", renewed, err)
	}
	if err = a.receive(t.Context(), *bp.Update, 1); err != nil {
		t.Fatal(err)
	}
	ap := a.state.Peers[aid]
	if ap.Outgoing != bp.Incoming || ap.PriorOutgoing != old {
		t.Fatal("sender did not install the renewed grant")
	}
	after := commitData(t, a, b, "after renewal")
	if after.Message.GrantHash != grant.Hash(bp.Incoming) {
		t.Fatal("sender still seals under the old grant")
	}
	for i, r := range []reservedPacket{inFlight, after} {
		if err = b.receive(t.Context(), r.Packet, uint64(i+2)); err != nil {
			t.Fatal(err)
		}
	}
	if got := inboxBodies(t, b); len(got) != 3 || !slices.Contains(got, "sealed under the old grant") || !slices.Contains(got, "after renewal") {
		t.Fatalf("inbox across renewal: %v", got)
	}
	page, err := b.inbox(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	inbox, ok := page.(InboxPage)
	if !ok {
		t.Fatalf("inbox page %T", page)
	}
	for _, m := range inbox.Messages {
		if m.Peer != bid {
			t.Fatalf("message under the prior grant lost its peer: %+v", m)
		}
	}
	oldID, err := b.installedGrantID(t.Context(), old)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.revoke(t.Context(), oldID); err != nil {
		t.Fatal(err)
	}
	grants, err := b.db.ListGrants(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range grants {
		if bp.hasIncoming(g.Hash) && !g.Revoked {
			t.Fatalf("grant %s survived revoking its renewed pair", g.GrantID)
		}
	}
	if got := inboxBodies(t, b); len(got) != 0 {
		t.Fatalf("revoked link still lists messages: %v", got)
	}
	if _, err = b.send(t.Context(), localipc.Request{Method: "send", Peer: bid, Kind: "coauthor", Body: "x"}); safeCode(err) != "revoked" {
		t.Fatalf("revoked link still sends: %v", err)
	}
}

// A renewal may only move the window: a peer cannot widen its grant to this
// daemon through an update.
func TestLinkUpdateRejectsChangedGrantTerms(t *testing.T) {
	parallelDaemonTest(t)
	a, b := pairedStores(t)
	aid, bid := peerID(b.state.Chain), peerID(a.state.Chain)
	shortenGrant(t, a, b)
	bp := b.state.Peers[bid]
	current, err := b.incomingGrant(bp)
	if err != nil {
		t.Fatal(err)
	}
	now := unixNow()
	current.GrantID, current.NotBefore, current.ExpiresAt = randomID(), now, now+grant.DefaultLifetime
	current.Limits.MaxHops--
	if bp.Incoming, err = grant.Issue(current, b.signingKey()); err != nil {
		t.Fatal(err)
	}
	if err = b.scheduleUpdate(bp); err != nil {
		t.Fatal(err)
	}
	before := a.state.Peers[aid].Outgoing
	// The slot is rejected rather than retried; either way nothing changes.
	if err = a.receive(t.Context(), *bp.Update, 1); err != nil {
		t.Logf("update rejected: %v", err)
	}
	if a.state.Peers[aid].Outgoing != before {
		t.Fatal("update widened the grant")
	}
}

// The update reaches the peer through the relay: a relay outage keeps it for
// the next sync, and relay custody clears it.
func TestLinkUpdateWaitsForRelayCustody(t *testing.T) {
	parallelDaemonTest(t)
	a, b := pairedStores(t)
	aid, bid := peerID(b.state.Chain), peerID(a.state.Chain)
	shortenGrant(t, a, b)
	if err := b.receive(t.Context(), commitData(t, a, b, "use").Packet, 1); err != nil {
		t.Fatal(err)
	}
	var posted []protocol.EncryptedPacket
	var available atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !available.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		var packet protocol.EncryptedPacket
		if err := json.NewDecoder(r.Body).Decode(&packet); err != nil {
			t.Error(err)
		}
		posted = append(posted, packet)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(relayclient.Receipt{Stage: "relay_stored", Mailbox: packet.Header.DstMailbox, MessageID: packet.Header.MessageID, Seq: 1}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	client, err := relayclient.New(relayclient.Options{URL: server.URL, Key: routePrivate(b.state.RoutingSeed), HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	b.relay = client
	if err = b.syncLinkUpdates(t.Context()); err != nil || b.state.Peers[bid].Update == nil {
		t.Fatalf("outage dropped the update: %v", err)
	}
	available.Store(true)
	if err = b.syncLinkUpdates(t.Context()); err != nil || b.state.Peers[bid].Update != nil || len(posted) != 1 {
		t.Fatalf("update not handed to the relay: %v posted=%d", err, len(posted))
	}
	if err = a.receive(t.Context(), posted[0], 1); err != nil {
		t.Fatal(err)
	}
	if a.state.Peers[aid].Outgoing != b.state.Peers[bid].Incoming {
		t.Fatal("relayed update not installed")
	}
}

// Packets parked under both grants of a renewal are all released once the
// missing sequence arrives, whichever grant releases first.
func TestGapAcrossRenewalReleasesBothGrants(t *testing.T) {
	parallelDaemonTest(t)
	a, b := pairedStores(t)
	aid, bid := peerID(b.state.Chain), peerID(a.state.Chain)
	shortenGrant(t, a, b)
	delayed := commitData(t, a, b, "delayed")
	second := commitData(t, a, b, "parked under the old grant")
	if err := b.receive(t.Context(), second.Packet, 1); err != nil {
		t.Fatal(err)
	}
	if err := a.receive(t.Context(), *b.state.Peers[bid].Update, 1); err != nil {
		t.Fatal(err)
	}
	if a.state.Peers[aid].PriorOutgoing == "" {
		t.Fatal("a parked packet did not renew the grant")
	}
	third := commitData(t, a, b, "parked under the renewed grant")
	for i, r := range []reservedPacket{third, delayed} {
		if err := b.receive(t.Context(), r.Packet, uint64(i+2)); err != nil {
			t.Fatal(err)
		}
	}
	if got := inboxBodies(t, b); len(got) != 3 {
		t.Fatalf("packets left parked across the renewal: %v", got)
	}
}

// A stored message admitted under the prior grant still names its peer for
// native delivery after the renewal.
func TestPriorGrantMessageKeepsItsSender(t *testing.T) {
	parallelDaemonTest(t)
	a, b := pairedStores(t)
	bid := peerID(a.state.Chain)
	old := shortenGrant(t, a, b)
	if err := b.receive(t.Context(), commitData(t, a, b, "use").Packet, 1); err != nil {
		t.Fatal(err)
	}
	if b.state.Peers[bid].PriorIncoming != old {
		t.Fatal("grant not renewed")
	}
	oldID, err := b.installedGrantID(t.Context(), old)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.claudeSender(store.InboxEntry{GrantID: oldID}); err != nil {
		t.Fatalf("message under the prior grant lost its sender: %v", err)
	}
}
