package grant

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/identity"
)

const fixtureNow int64 = 1_800_000_000

func TestScopeAllowsOnlyRecipientApprovedIntent(t *testing.T) {
	for _, tc := range []struct {
		scope Scope
		kind  string
		code  fault.Code
	}{
		{Inform, "inform", ""}, {Inform, "receipt", ""}, {Inform, "ask", fault.ScopeDenied},
		{Inform, "coauthor", fault.ScopeDenied}, {Inform, "reply", fault.ScopeDenied},
		{Ask, "inform", ""}, {Ask, "ask", ""}, {Ask, "reply", ""}, {Ask, "coauthor", fault.ScopeDenied},
		{Coauthor, "inform", ""}, {Coauthor, "ask", ""}, {Coauthor, "coauthor", ""},
		{Scope("owner"), "inform", fault.InvalidGrant}, {Coauthor, "owner approved", fault.ScopeDenied},
	} {
		err := CheckScope(tc.scope, tc.kind)
		if tc.code == "" && err != nil || tc.code != "" && !fault.IsCode(err, tc.code) {
			t.Fatalf("%s/%s: %v (want %s)", tc.scope, tc.kind, err, tc.code)
		}
	}
}

func TestGrantExactSessionAndRevocation(t *testing.T) {
	g, issuer, subject, key := grantFixture(t)
	compact, err := Issue(g, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(compact, issuer, subject, 1, fixtureNow); err != nil {
		t.Fatal(err)
	}
	other := subject
	other.SessionID = "other-session"
	if _, err := Verify(compact, issuer, other, 1, fixtureNow); !fault.IsCode(err, fault.AudienceMismatch) {
		t.Fatalf("wrong subject accepted: %v", err)
	}
	if _, err := Verify(compact, issuer, subject, 2, fixtureNow); !fault.IsCode(err, fault.Revoked) {
		t.Fatalf("revoked grant accepted: %v", err)
	}
}

func grantFixture(t testing.TB) (Grant, identity.SessionCertificate, identity.SessionCertificate, ed25519.PrivateKey) {
	t.Helper()
	ipub, ikey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	spub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	b := identity.Binding{IdentityID: identity.RootFingerprint(ipub), DeviceID: "device-a", DeviceEpoch: 1,
		SessionID: "session-a", SessionEpoch: 1, KeyEpoch: 1, SigningKey: ipub}
	s := identity.Binding{IdentityID: identity.RootFingerprint(spub), DeviceID: "device-b", DeviceEpoch: 1,
		SessionID: "session-b", SessionEpoch: 1, KeyEpoch: 1, SigningKey: spub}
	i := identity.SessionCertificate{Version: 1, Binding: b, EncryptionKey: encryptionKey(t),
		NotBefore: fixtureNow - 100, ExpiresAt: fixtureNow + 7200}
	sc := identity.SessionCertificate{Version: 1, Binding: s, EncryptionKey: encryptionKey(t),
		NotBefore: fixtureNow - 100, ExpiresAt: fixtureNow + 7200}
	g := Grant{Version: 1, GrantID: "grant-a", Issuer: b, Subject: s, Scope: Coauthor,
		GrantEpoch: 1, NotBefore: fixtureNow - 1, ExpiresAt: fixtureNow + 3600, Limits: DefaultLimits()}
	return g, i, sc, ikey
}

func encryptionKey(t testing.TB) []byte {
	t.Helper()
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key.PublicKey().Bytes()
}
