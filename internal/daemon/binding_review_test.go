package daemon

import (
	"crypto/ed25519"
	"encoding/base64"
	"testing"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/invite"
	"github.com/temirlankasen/peerturn/internal/relayclient"
)

func TestDescriptorRoutingAndIdentityCollisionsHaveNoEffects(t *testing.T) {
	parallelDaemonTest(t)
	for _, collision := range []string{"peer_route", "own_route", "peer_identity", "own_identity", "own_signing_route", "peer_signing_route"} {
		t.Run(collision, func(t *testing.T) {
			admin := routingKey(t)
			f := newRelayFixture(t, admin)
			a := openRecoveryDaemon(t, f, admin)
			b := openRecoveryDaemon(t, f, routingKey(t))
			c := openRecoveryDaemon(t, f, routingKey(t))
			attachPair(t, a, b)
			if err := a.enroll(t.Context(), ""); err != nil {
				t.Fatal(err)
			}
			issuer, route := c, c.routingPublic()
			switch collision {
			case "peer_route":
				route = b.routingPublic()
			case "own_route":
				route = a.routingPublic()
			case "peer_identity":
				issuer = b
			case "own_identity":
				issuer = a
			case "own_signing_route":
				route = a.cert.SigningKey
			case "peer_signing_route":
				route = b.cert.SigningKey
			}
			link, entry := collisionInvite(t, a, issuer, route)
			if err := a.publishInvite(t.Context(), entry); err != nil {
				t.Fatal(err)
			}
			before, err := codec.Canonical(a.state)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = a.join(t.Context(), link.SecretURL()); !fault.IsCode(err, fault.Conflict) {
				t.Fatalf("collision accepted: %v", err)
			}
			after, err := codec.Canonical(a.state)
			if err != nil || string(before) != string(after) {
				t.Fatal("rejected descriptor changed state", err)
			}
			packet := incomingData(t, b, a)
			if err = a.receive(t.Context(), packet.Packet, 1); err != nil {
				t.Fatal(err)
			}
			items, err := a.db.ListInbox(t.Context(), 10)
			if err != nil || len(items) != 1 || items[0].MessageID != packet.Message.MessageID {
				t.Fatal("honest peer no longer bound", err)
			}
		})
	}
}

func TestClaimRoutingAndIdentityCollisionsHaveNoEffects(t *testing.T) {
	parallelDaemonTest(t)
	for _, collision := range []string{"peer_route", "own_route", "peer_identity", "own_identity"} {
		t.Run(collision, func(t *testing.T) {
			admin := routingKey(t)
			f := newRelayFixture(t, admin)
			a := openRecoveryDaemon(t, f, admin)
			b := openRecoveryDaemon(t, f, routingKey(t))
			c := openRecoveryDaemon(t, f, routingKey(t))
			attachPair(t, a, b)
			if err := a.enroll(t.Context(), ""); err != nil {
				t.Fatal(err)
			}
			claimant, route := c, c.routingPublic()
			switch collision {
			case "peer_route":
				route = b.routingPublic()
			case "own_route":
				route = a.routingPublic()
			case "peer_identity":
				claimant = b
			case "own_identity":
				claimant = a
			}
			link, entry := collisionInvite(t, a, a, a.routingPublic())
			if err := a.publishInvite(t.Context(), entry); err != nil {
				t.Fatal(err)
			}
			returned, err := claimant.issueGrant(a.cert)
			if err != nil {
				t.Fatal(err)
			}
			now := unixNow()
			claim := invite.Claim{Version: 1, InviteID: link.ID(), ClaimID: base64.RawURLEncoding.EncodeToString(make([]byte, 16)), Claimant: claimant.state.Chain, Audience: a.cert.Binding, RequestedScope: grant.Coauthor, ReturnGrant: returned, RoutingKey: route, MailboxKey: route, NotBefore: now, ExpiresAt: entry.Descriptor.ExpiresAt}
			packet, err := invite.CreateClaim(link, entry.Descriptor, claim, claimant.signingKey(), 1, now)
			if err != nil {
				t.Fatal(err)
			}
			// The daemon must validate independently of an honest relay's reader check.
			client, err := relayclient.New(relayclient.Options{URL: f.server.URL, Key: routePrivateForTest(t, route, a, b, c), HTTPClient: f.server.Client()})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = client.ClaimInvite(t.Context(), link.ID(), packet); err != nil {
				t.Fatal(err)
			}
			before, err := codec.Canonical(a.state)
			if err != nil {
				t.Fatal(err)
			}
			if err = a.syncInvite(t.Context(), entry); !fault.IsCode(err, fault.Conflict) {
				t.Fatalf("claim collision accepted: %v", err)
			}
			after, err := codec.Canonical(a.state)
			if err != nil || string(before) != string(after) {
				t.Fatal("rejected claim changed state", err)
			}
		})
	}
}

func collisionInvite(t *testing.T, local, issuer *Daemon, route []byte) (invite.Link, *issuedInvite) {
	t.Helper()
	now := unixNow()
	desc := invite.Descriptor{Version: 1, Inviter: issuer.state.Chain, ForwardScope: grant.Coauthor, ReturnScope: grant.Coauthor, RelayOrigin: local.state.RelayURL, NotBefore: now, ExpiresAt: now + 300, MaxUses: 1, PublisherManifestHash: previewManifest, EnrollmentVoucher: randomID() + randomID(), RoutingKey: route, MailboxKey: route}
	link, packet, err := invite.Create(desc, issuer.signingKey(), now)
	if err != nil {
		t.Fatal(err)
	}
	desc.InviteID = link.ID()
	return link, &issuedInvite{URL: link.SecretURL(), Descriptor: desc, Packet: packet}
}

func routePrivateForTest(t *testing.T, route []byte, peers ...*Daemon) ed25519.PrivateKey {
	t.Helper()
	for _, d := range peers {
		if equalBytes(route, d.routingPublic()) {
			return ed25519.NewKeyFromSeed(d.state.RoutingSeed)
		}
	}
	t.Fatal("missing fixture route signer")
	return nil
}
