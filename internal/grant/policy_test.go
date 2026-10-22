package grant

import (
	"testing"

	"github.com/temirlankasen/peerturn/internal/codec"
	peercrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/identity"
)

func TestPolicyNeverWidensScope(t *testing.T) {
	for _, tc := range []struct {
		scope Scope
		kind  string
		allow bool
	}{
		{Inform, "inform", true}, {Inform, "receipt", true}, {Inform, "ask", false},
		{Inform, "coauthor", false}, {Inform, "reply", false},
		{Ask, "ask", true}, {Ask, "reply", true}, {Ask, "coauthor", false},
		{Ask, "revoke", false}, {Coauthor, "coauthor", true}, {Coauthor, "ask", true},
		{Coauthor, "inform", true}, {Coauthor, "receipt", true}, {Coauthor, "revoke", true},
		{Coauthor, "owner approved", false},
	} {
		err := CheckScope(tc.scope, tc.kind)
		if tc.allow && err != nil || !tc.allow && !fault.IsCode(err, fault.ScopeDenied) {
			t.Fatalf("%s/%s: %v", tc.scope, tc.kind, err)
		}
	}
}

func TestSignedGrantCannotChangeAnyExactBinding(t *testing.T) {
	g, issuer, subject, key := grantFixture(t)
	for _, mutate := range []func(*identity.Binding){
		func(b *identity.Binding) { b.IdentityID = issuer.IdentityID },
		func(b *identity.Binding) { b.DeviceID = "other-device" },
		func(b *identity.Binding) { b.DeviceEpoch++ },
		func(b *identity.Binding) { b.SessionID = "other-session" },
		func(b *identity.Binding) { b.SessionEpoch++ },
		func(b *identity.Binding) { b.KeyEpoch++ },
		func(b *identity.Binding) { b.SigningKey = issuer.SigningKey },
	} {
		altered := g
		mutate(&altered.Subject)
		compact, err := Issue(altered, key)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Verify(compact, issuer, subject, 1, fixtureNow); !fault.IsCode(err, fault.AudienceMismatch) {
			t.Fatalf("altered subject accepted: %v", err)
		}
	}
	compact, err := Issue(g, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, verifyErr := Verify(compact, subject, issuer, 1, fixtureNow); !fault.IsCode(verifyErr, fault.InvalidSignature) {
		t.Fatalf("reverse direction: %v", verifyErr)
	}
	altered := g
	altered.Issuer.DeviceEpoch++
	signed, err := Issue(altered, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(signed, issuer, subject, 1, fixtureNow); !fault.IsCode(err, fault.AudienceMismatch) {
		t.Fatalf("altered issuer epoch accepted: %v", err)
	}
}

func TestGrantTimeLimitsRouteAndUnsignedWidening(t *testing.T) {
	g, issuer, subject, key := grantFixture(t)
	compact, err := Issue(g, key)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		now  int64
		code fault.Code
	}{{g.NotBefore - 1, fault.NotYetValid}, {g.ExpiresAt, fault.Expired}} {
		if _, err := Verify(compact, issuer, subject, 1, tc.now); !fault.IsCode(err, tc.code) {
			t.Fatalf("wrong time decision: %v", err)
		}
	}
	for _, mutate := range []func(*Grant){
		func(g *Grant) { g.ExpiresAt = g.NotBefore + MaxLifetime + 1 },
		func(g *Grant) { g.Limits.MaxHops = 9 },
		func(g *Grant) { g.Limits.MaxBurst = 0 },
		func(g *Grant) { g.ReplyRoute = &g.Issuer },
		func(g *Grant) { g.Subject.SessionID = "*" },
	} {
		altered := g
		mutate(&altered)
		if _, err := Issue(altered, key); !fault.IsCode(err, fault.InvalidGrant) {
			t.Fatalf("invalid authority accepted: %v", err)
		}
	}
	for _, scope := range []Scope{Inform, Ask} {
		restricted := g
		restricted.Scope = scope
		if scope == Ask {
			restricted.ReplyRoute = &restricted.Subject
		}
		payload, err := codec.Canonical(restricted)
		if err != nil {
			t.Fatal(err)
		}
		signed, err := peercrypto.Sign(payload, identity.RootFingerprint(issuer.SigningKey), peercrypto.GrantType, key)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Verify(signed, issuer, subject, 1, fixtureNow); err != nil {
			t.Fatalf("recipient-approved signed grant rejected: %v", err)
		}
	}
}

func TestEpochDistinguishesRevokeAndFuture(t *testing.T) {
	for _, tc := range []struct {
		present uint64
		current uint64
		code    fault.Code
	}{{1, 2, fault.Revoked}, {2, 1, fault.EpochMismatch}, {0, 1, fault.EpochMismatch},
		{1, 0, fault.EpochMismatch}, {1 << 53, 1, fault.EpochMismatch}} {
		if err := CompareEpoch(tc.present, tc.current); !fault.IsCode(err, tc.code) {
			t.Fatalf("epoch %d/%d: %v", tc.present, tc.current, err)
		}
	}
}

func TestGrantWindowCannotExceedEitherCertificate(t *testing.T) {
	g, issuer, subject, key := grantFixture(t)
	compact, err := Issue(g, key)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		issuer bool
		start  bool
	}{
		{"issuer_start", true, true}, {"issuer_end", true, false},
		{"subject_start", false, true}, {"subject_end", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			i, s := issuer, subject
			cert := &s
			if tc.issuer {
				cert = &i
			}
			if tc.start {
				cert.NotBefore = g.NotBefore + 1
			} else {
				cert.ExpiresAt = g.ExpiresAt - 1
			}
			if _, err := Verify(compact, i, s, 1, fixtureNow); !fault.IsCode(err, fault.InvalidGrant) {
				t.Fatalf("grant escaped certificate window: %v", err)
			}
		})
	}
}
