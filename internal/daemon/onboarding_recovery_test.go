package daemon

import (
	"crypto/ed25519"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/invite"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/relayclient"
	"github.com/temirlankasen/peerturn/internal/store"
)

func recoveryJoin(t *testing.T, local, remote *Daemon) *joiningInvite {
	t.Helper()
	now := unixNow()
	descriptor := invite.Descriptor{Version: 1, Inviter: remote.state.Chain, ForwardScope: grant.Coauthor, ReturnScope: grant.Coauthor, RelayOrigin: local.state.RelayURL, NotBefore: now, ExpiresAt: now + 300, MaxUses: 1, PublisherManifestHash: previewManifest, EnrollmentVoucher: randomID() + randomID(), RoutingKey: remote.routingPublic(), MailboxKey: remote.routingPublic()}
	link, _, err := invite.Create(descriptor, remote.signingKey(), now)
	if err != nil {
		t.Fatal(err)
	}
	descriptor.InviteID = link.ID()
	returned, err := local.issueGrant(remote.cert)
	if err != nil {
		t.Fatal(err)
	}
	// Grant issuance samples the clock independently and can cross a second.
	now = unixNow()
	claim := invite.Claim{Version: 1, InviteID: link.ID(), ClaimID: base64.RawURLEncoding.EncodeToString(make([]byte, 16)), Claimant: local.state.Chain, Audience: remote.cert.Binding, RequestedScope: grant.Coauthor, ReturnGrant: returned, RoutingKey: local.routingPublic(), MailboxKey: local.routingPublic(), NotBefore: now, ExpiresAt: descriptor.ExpiresAt}
	packet, err := invite.CreateClaim(link, descriptor, claim, local.signingKey(), 1, now)
	if err != nil {
		t.Fatal(err)
	}
	j := &joiningInvite{URL: link.SecretURL(), Descriptor: descriptor, Claim: claim, Packet: packet}
	local.state.Joining[link.ID()] = j
	p := local.state.Peers[peerID(remote.state.Chain)]
	p.Incoming = returned
	return j
}

func recoveryRelayOffline(t *testing.T, d *Daemon) {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	t.Cleanup(server.Close)
	client, err := relayclient.New(relayclient.Options{URL: server.URL, Key: routePrivate(d.state.RoutingSeed), HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	d.relay = client
}

func TestJoiningRecoveryRestoresReturnGrantBeforeClaimRetry(t *testing.T) {
	a, b := pairedStores(t)
	j := recoveryJoin(t, a, b)
	returned, err := grant.Verify(j.Claim.ReturnGrant, a.cert, b.cert, 1, unixNow())
	if err != nil {
		t.Fatal(err)
	}
	if err = a.save(); err != nil {
		t.Fatal(err)
	}
	// The journal survived a crash before its ReturnGrant reached the store.
	recoveryRelayOffline(t, a)
	if err = a.syncJoins(t.Context()); err == nil {
		t.Fatal("offline claim unexpectedly succeeded")
	}
	grants, err := a.db.ListGrants(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range grants {
		if g.GrantID == returned.GrantID {
			return
		}
	}
	t.Fatal("sealed join recovery retried network before restoring its return grant")
}

func TestJoiningRecoveryRestoresCompletedGrantsAndPreservesRevocation(t *testing.T) {
	a, b := pairedStores(t)
	j := recoveryJoin(t, a, b)
	j.Complete, j.Claimed, j.Enrolled = true, true, true
	p := a.state.Peers[peerID(b.state.Chain)]
	var err error
	p.Outgoing, err = b.issueGrant(a.cert)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.syncJoins(t.Context()); err != nil {
		t.Fatal(err)
	}
	items, err := a.db.ListGrants(t.Context())
	if err != nil || len(items) != 4 {
		t.Fatal("completed join failed to restore both missing grants", len(items), err)
	}
	g, err := grant.Verify(j.Claim.ReturnGrant, a.cert, b.cert, 1, unixNow())
	if err != nil {
		t.Fatal(err)
	}
	if err = a.db.GrantRevoke(t.Context(), g.GrantID, 1, unixNow()); err != nil {
		t.Fatal(err)
	}
	if err = a.syncJoins(t.Context()); err != nil {
		t.Fatal(err)
	}
	items, err = a.db.ListGrants(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.GrantID == g.GrantID && (!item.Revoked || item.Epoch != 2) {
			t.Fatal("completed recovery revived a revoked grant")
		}
	}
}

func TestOnboardingExpiredEntryDoesNotHideAnotherJoinOrStorageFailure(t *testing.T) {
	a, b := pairedStores(t)
	good := recoveryJoin(t, a, b)
	a.state.Joining["000-expired"] = &joiningInvite{Descriptor: invite.Descriptor{ExpiresAt: unixNow() - 1}}
	recoveryRelayOffline(t, a)
	if err := a.syncJoins(t.Context()); err == nil {
		t.Fatal("offline onboarding unexpectedly succeeded")
	}
	bad := a.state.Joining["000-expired"]
	if !bad.Terminal || bad.Failure != string(fault.Expired) || good.Terminal || good.Failure == "" {
		t.Fatalf("entry isolation lost terminal/transient distinction: bad=%+v good=%+v", bad, good)
	}
	state, err := loadState(a.options.StateDir, a.sealer)
	if err != nil || !state.Joining["000-expired"].Terminal || state.Joining[good.Claim.InviteID].Failure == "" {
		t.Fatal("entry failure state was not durable", err)
	}
	a.sealer = failedSealer{a.sealer}
	good.Failure = ""
	if err = a.syncJoins(t.Context()); !fault.IsCode(err, store.SealingFailed) {
		t.Fatal("onboarding transport error masked journal failure", err)
	}
}

func openRecoveryDaemon(t *testing.T, relay *relayFixture, key ed25519.PrivateKey) *Daemon {
	t.Helper()
	dir := stateDir(t)
	if _, err := Initialize(t.Context(), InitOptions{StateDir: dir, RelayURL: relay.server.URL, SessionLabel: "recovery", FilePermissions: true, RoutingKey: key}); err != nil {
		t.Fatal(err)
	}
	d, err := Open(t.Context(), Options{StateDir: dir, HTTPClient: relay.server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	return d
}

func TestIssuedClaimWaitsForJoiningMailboxWithoutBecomingTerminal(t *testing.T) {
	key := routingKey(t)
	relay := newRelayFixture(t, key)
	a := openRecoveryDaemon(t, relay, key)
	b := openRecoveryDaemon(t, relay, routingKey(t))
	if _, err := a.createInvite(t.Context()); err != nil {
		t.Fatal(err)
	}
	var issued *issuedInvite
	for _, entry := range a.state.Issued {
		issued = entry
	}
	if _, err := b.join(t.Context(), issued.URL); err != nil {
		t.Fatal(err)
	}
	j := b.state.Joining[issued.Descriptor.InviteID]
	if _, err := b.relay.ClaimInvite(t.Context(), j.Claim.InviteID, j.Packet); err != nil {
		t.Fatal(err)
	}
	if err := a.syncInvites(t.Context()); err == nil {
		t.Fatal("unenrolled joining mailbox accepted response")
	}
	if issued.Claim == nil || issued.Terminal || issued.Failure == "" {
		t.Fatal("valid claim was terminalized while its mailbox was not enrolled")
	}
	if err := b.syncJoins(t.Context()); err != nil {
		t.Fatal(err)
	}
	// A retained claim may send its exact already consumed response after the
	// descriptor expires; it cannot make a fresh admission under that expiry.
	issued.Descriptor.ExpiresAt = unixNow() - 1
	if err := a.syncInvites(t.Context()); err != nil || !issued.Sent || issued.Terminal || issued.Failure != "" {
		t.Fatal("retained response failed delayed mailbox enrollment", err)
	}
}

func TestJoiningInvalidProofBecomesTerminalWithoutRepeatedSubmission(t *testing.T) {
	key := routingKey(t)
	relay := newRelayFixture(t, key)
	a := openRecoveryDaemon(t, relay, key)
	b := openRecoveryDaemon(t, relay, routingKey(t))
	if _, err := a.createInvite(t.Context()); err != nil {
		t.Fatal(err)
	}
	var issued *issuedInvite
	for _, entry := range a.state.Issued {
		issued = entry
	}
	if _, err := b.join(t.Context(), issued.URL); err != nil {
		t.Fatal(err)
	}
	j := b.state.Joining[issued.Descriptor.InviteID]
	j.Packet.Proof = "invalid-signature"
	if err := b.syncJoins(t.Context()); err == nil || !j.Terminal || j.Failure != "invalid_invite_proof" {
		t.Fatal("invalid invitation proof remained an active retry", err)
	}
	if err := b.syncJoins(t.Context()); err != nil || j.Claimed {
		t.Fatal("terminal invitation proof was submitted again", err)
	}
}

func TestExpiredOnboardingDoesNotBlockConnectedPeerCustody(t *testing.T) {
	key := routingKey(t)
	relay := newRelayFixture(t, key)
	a := newDaemonFixture(t, relay, "a-explicit", key)
	b := newDaemonFixture(t, relay, "b-explicit", routingKey(t))
	var invitation struct {
		URL string `json:"url"`
	}
	a.call(t, localipc.Request{Method: "invite", Scope: "coauthor"}, &invitation)
	b.call(t, localipc.Request{Method: "join", Scope: "coauthor", Link: invitation.URL}, nil)
	var peers []Peer
	eventually(t, func() bool {
		a.call(t, localipc.Request{Method: "peers"}, &peers)
		return len(peers) == 1 && peers[0].State == "connected"
	})
	a.stop(t)
	d, err := Open(t.Context(), Options{StateDir: a.dir, HTTPClient: relay.server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	d.state.Joining["expired-unrelated"] = &joiningInvite{Descriptor: invite.Descriptor{ExpiresAt: unixNow() - 1}}
	if err = d.save(); err != nil {
		t.Fatal(err)
	}
	if err = d.Close(); err != nil {
		t.Fatal(err)
	}
	a.start(t)
	var receipt protocol.ReceiptPayload
	a.call(t, localipc.Request{Method: "send", Peer: peers[0].ID, Kind: "coauthor", Body: "unrelated onboarding must not block custody"}, &receipt)
	eventually(t, func() bool {
		a.call(t, localipc.Request{Method: "receipt", MessageID: receipt.MessageID}, &receipt)
		return receipt.Stage == "receiver_stored"
	})
}
