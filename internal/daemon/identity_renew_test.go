package daemon

import (
	"crypto/ed25519"
	"testing"

	"github.com/temirlankasen/peerturn/internal/identity"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// rewriteState edits the sealed journal of a stopped fixture daemon.
func rewriteState(t *testing.T, d *daemonFixture, edit func(*clientState)) {
	t.Helper()
	authority, err := openAuthorityKey(t.Context(), d.dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	state, err := loadState(d.dir, authority.sealer)
	if err != nil {
		t.Fatal(err)
	}
	edit(&state)
	if err = saveState(d.dir, authority.sealer, state, false); err != nil {
		t.Fatal(err)
	}
}

func ownCertificate(t *testing.T, d *daemonFixture) identity.SessionCertificate {
	t.Helper()
	var cert identity.SessionCertificate
	rewriteState(t, d, func(s *clientState) {
		var err error
		if cert, _, err = historicalBinding(s.Chain); err != nil {
			t.Fatal(err)
		}
	})
	return cert
}

// A profile used to stop inviting, joining and even starting a day after
// init. An expired certificate must renew on open with the same session and
// keys under the next epoch.
func TestExpiredProfileRenewsOnOpenAndInvites(t *testing.T) {
	parallelDaemonTest(t)
	admin := routingKey(t)
	relay := newRelayFixture(t, admin)
	a := newDaemonFixture(t, relay, "a", admin)
	a.stop(t)
	before := ownCertificate(t, a)
	rewriteState(t, a, func(s *clientState) {
		if err := reissueSession(s, unixNow()-identity.SessionMaxLifetime-60); err != nil {
			t.Fatal(err)
		}
	})
	a.start(t)
	var invitation struct{ URL string }
	a.call(t, localipc.Request{Method: "invite", Scope: "coauthor"}, &invitation)
	if invitation.URL == "" {
		t.Fatal("renewed profile did not invite")
	}
	a.stop(t)
	after := ownCertificate(t, a)
	a.start(t)
	if after.SessionID != before.SessionID || after.SessionEpoch != before.SessionEpoch+2 || after.ExpiresAt <= unixNow()+renewalMargin {
		t.Fatalf("renewal kept %+v from %+v", after.Binding, before.Binding)
	}
	if !equalBytes(after.SigningKey, before.SigningKey) || !equalBytes(after.EncryptionKey, before.EncryptionKey) {
		t.Fatal("renewal replaced session keys")
	}
}

// After either side renews, the peer holds a record for the previous epoch.
// A new invitation in either direction must replace it and revoke the old
// link's grants, so only the current link's messages stay in the inbox.
func TestRenewedPeerRelinksInBothDirections(t *testing.T) {
	parallelDaemonTest(t)
	admin := routingKey(t)
	relay := newRelayFixture(t, admin)
	a := newDaemonFixture(t, relay, "a", admin)
	bKey := routingKey(t)
	relay.cfg.AdminKeys = append(relay.cfg.AdminKeys, publicKey(bKey[32:]))
	relay.restart(t)
	b := newDaemonFixture(t, relay, "b", bKey)
	link := func(inviter, joiner *daemonFixture) {
		var invitation struct{ URL string }
		inviter.call(t, localipc.Request{Method: "invite", Scope: "coauthor"}, &invitation)
		joiner.call(t, localipc.Request{Method: "join", Scope: "coauthor", Link: invitation.URL}, nil)
		eventually(t, func() bool {
			var ip, jp []Peer
			inviter.call(t, localipc.Request{Method: "peers"}, &ip)
			joiner.call(t, localipc.Request{Method: "peers"}, &jp)
			return len(ip) == 1 && len(jp) == 1 && ip[0].State == "connected" && jp[0].State == "connected"
		})
	}
	exchange := func(body string) {
		for _, pair := range [][2]*daemonFixture{{a, b}, {b, a}} {
			var peers []Peer
			pair[0].call(t, localipc.Request{Method: "peers"}, &peers)
			var receipt protocol.ReceiptPayload
			pair[0].call(t, localipc.Request{Method: "send", Peer: peers[0].ID, Kind: "coauthor", Body: body}, &receipt)
			eventually(t, func() bool {
				pair[0].call(t, localipc.Request{Method: "receipt", MessageID: receipt.MessageID}, &receipt)
				return receipt.Stage == "receiver_stored"
			})
		}
	}
	renew := func(d *daemonFixture) {
		d.stop(t)
		rewriteState(t, d, func(s *clientState) {
			if err := reissueSession(s, unixNow()); err != nil {
				t.Fatal(err)
			}
		})
		d.start(t)
	}
	link(a, b)
	exchange("first link")
	renew(b)
	link(a, b)
	exchange("after joiner renewal")
	renew(a)
	link(a, b)
	exchange("after inviter renewal")
	for _, d := range []*daemonFixture{a, b} {
		var peers []Peer
		d.call(t, localipc.Request{Method: "peers"}, &peers)
		var inbox InboxPage
		d.call(t, localipc.Request{Method: "inbox"}, &inbox)
		if len(inbox.Messages) != 1 || inbox.Messages[0].BodyUTF8 != "after inviter renewal" || inbox.Messages[0].Peer != peers[0].ID {
			t.Fatalf("inbox after renewals: %+v", inbox.Messages)
		}
		assertTwoLiveGrants(t, d)
	}
}

// Renewing before expiry changes the epoch while grants and packets of the old
// certificate are still valid, so only an explicit margin renews early.
func TestRenewChainWaitsForExpiryWithoutMargin(t *testing.T) {
	parallelDaemonTest(t)
	admin := routingKey(t)
	a := newDaemonFixture(t, newRelayFixture(t, admin), "a", admin)
	a.stop(t)
	rewriteState(t, a, func(s *clientState) {
		cert, _, err := historicalBinding(s.Chain)
		if err != nil {
			t.Fatal(err)
		}
		// Reissue so that the certificate has five minutes left now.
		if err = reissueSession(s, unixNow()-identity.SessionMaxLifetime+300); err != nil {
			t.Fatal(err)
		}
		probe := *s
		if renewed, renewErr := renewChain(&probe, unixNow(), 0); renewErr != nil || renewed {
			t.Fatalf("renewed a live certificate: %v", renewErr)
		}
		if renewed, renewErr := renewChain(&probe, unixNow(), renewalMargin); renewErr != nil || !renewed {
			t.Fatalf("margin did not renew: %v", renewErr)
		}
		after, _, err := historicalBinding(probe.Chain)
		if err != nil || after.SessionEpoch != cert.SessionEpoch+2 {
			t.Fatalf("renewed epoch %d from %d: %v", after.SessionEpoch, cert.SessionEpoch, err)
		}
	})
}

// A profile from before 0.1.9 holds a day-long certificate. The owner's next
// invitation renews it to the full lifetime, so the new link outlives the day.
func TestLegacyDayCertificateRenewsOnInvite(t *testing.T) {
	parallelDaemonTest(t)
	admin := routingKey(t)
	a := newDaemonFixture(t, newRelayFixture(t, admin), "a", admin)
	a.stop(t)
	rewriteState(t, a, func(s *clientState) {
		cert, _, err := historicalBinding(s.Chain)
		if err != nil {
			t.Fatal(err)
		}
		cert.ExpiresAt = cert.NotBefore + 24*60*60 + 5*60
		if s.Chain.Session, err = identity.IssueSession(cert, ed25519.NewKeyFromSeed(s.DeviceSeed)); err != nil {
			t.Fatal(err)
		}
	})
	before := ownCertificate(t, a)
	a.start(t)
	var invitation struct{ URL string }
	a.call(t, localipc.Request{Method: "invite", Scope: "coauthor"}, &invitation)
	a.stop(t)
	after := ownCertificate(t, a)
	a.start(t)
	if after.SessionEpoch != before.SessionEpoch+1 || after.ExpiresAt-after.NotBefore != identity.SessionMaxLifetime {
		t.Fatalf("legacy certificate kept: %+v", after)
	}
}
