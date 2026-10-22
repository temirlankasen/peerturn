package invite

import (
	"reflect"
	"testing"

	ptcrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/identity"
)

func returnGrantForScope(t *testing.T, f inviteFixture, scope grant.Scope) string {
	t.Helper()
	g := grant.Grant{Version: 1, GrantID: "return-grant", Issuer: f.claimant.cert.Binding,
		Subject: f.inviter.cert.Binding, Scope: scope, GrantEpoch: 1, NotBefore: testNow,
		ExpiresAt: testNow + 1800, Limits: grant.DefaultLimits()}
	if scope == grant.Ask {
		g.ReplyRoute = &g.Subject
	}
	signed, err := grant.Issue(g, f.claimant.sign)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func TestInvitationSupportsIndependentDirectionalScopes(t *testing.T) {
	f := newInviteFixture(t)
	for _, forward := range []grant.Scope{grant.Inform, grant.Ask, grant.Coauthor} {
		for _, reverse := range []grant.Scope{grant.Inform, grant.Ask, grant.Coauthor} {
			t.Run(string(forward)+"/"+string(reverse), func(t *testing.T) {
				d := f.descriptor
				d.InviteID, d.ForwardScope, d.ReturnScope = "", forward, reverse
				link, encrypted, err := Create(d, f.inviter.sign, testNow)
				if err != nil {
					t.Fatal("valid invitation scope rejected", err)
				}
				d, _, err = OpenDescriptor(link, encrypted, d.PublisherManifestHash, testNow)
				if err != nil {
					t.Fatal(err)
				}
				claim := f.claim
				claim.InviteID, claim.RequestedScope = link.ID(), forward
				claim.ReturnGrant = returnGrantForScope(t, f, reverse)
				packet, err := CreateClaim(link, d, claim, f.claimant.sign, 1, testNow)
				if err != nil {
					t.Fatal(err)
				}
				opened, err := OpenClaim(link, d, packet, f.inviter.key, "", 1, testNow)
				if err != nil || opened.RequestedScope != forward || opened.ReturnGrant != claim.ReturnGrant {
					t.Fatal("claim scope directions changed", err)
				}
			})
		}
	}
}

func TestClaimScopeCeilingsRemainDirectional(t *testing.T) {
	f := newInviteFixture(t)
	cases := []struct{ ceiling, requested, reverseCeiling, returned grant.Scope }{
		{grant.Inform, grant.Ask, grant.Coauthor, grant.Coauthor},
		{grant.Ask, grant.Coauthor, grant.Coauthor, grant.Coauthor},
		{grant.Coauthor, grant.Inform, grant.Inform, grant.Ask},
		{grant.Coauthor, grant.Inform, grant.Ask, grant.Coauthor},
	}
	for _, test := range cases {
		d, claim := f.descriptor, f.claim
		d.ForwardScope, d.ReturnScope = test.ceiling, test.reverseCeiling
		claim.RequestedScope = test.requested
		claim.ReturnGrant = returnGrantForScope(t, f, test.returned)
		if _, err := CreateClaim(f.link, d, claim, f.claimant.sign, 1, testNow); !fault.IsCode(err, fault.ScopeDenied) {
			t.Fatal("sender accepted widening of local scope ceiling", err)
		}
		// A valid peer signature cannot bypass the receiving inviter's local policy.
		signed, err := ptcrypto.Sign(canonical(t, claim), identity.RootFingerprint(f.claimant.cert.SigningKey), ptcrypto.ClaimType, f.claimant.sign)
		if err != nil {
			t.Fatal(err)
		}
		packet := sealClaim(t, f, signed)
		got, err := OpenClaim(f.link, d, packet, f.inviter.key, "", 1, testNow)
		if !fault.IsCode(err, fault.ScopeDenied) || !reflect.DeepEqual(got, Claim{}) {
			t.Fatal("receiver took reverse scope from request instead of verified grant", err)
		}
	}
}
