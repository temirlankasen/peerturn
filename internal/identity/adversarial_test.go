package identity

import (
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/codec"
	peercrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/fault"
)

func TestCertificateEpochWindowAndEncryptionKey(t *testing.T) {
	chain, original, key := identityFixture(t)
	for _, tc := range []struct {
		name   string
		mutate func(*SessionCertificate)
	}{
		{"device epoch", func(s *SessionCertificate) { s.DeviceEpoch++ }},
		{"identity", func(s *SessionCertificate) { s.IdentityID = strings.Repeat("a", 64) }},
		{"extends device expiry", func(s *SessionCertificate) { s.ExpiresAt = fixtureNow + 7201 }},
		{"precedes device start", func(s *SessionCertificate) { s.NotBefore = fixtureNow - 11 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := original
			tc.mutate(&s)
			signed, err := IssueSession(s, key)
			if err != nil {
				t.Fatal(err)
			}
			altered := chain
			altered.Session = signed
			if _, err := VerifyChain(altered, original.IdentityID, fixtureNow); !fault.IsCode(err, fault.InvalidCertificate) {
				t.Fatalf("invalid chain accepted: %v", err)
			}
		})
	}
	for _, raw := range [][]byte{nil, make([]byte, 31), make([]byte, 32), append([]byte{1}, make([]byte, 31)...)} {
		s := original
		s.EncryptionKey = raw
		if _, err := IssueSession(s, key); !fault.IsCode(err, fault.InvalidCertificate) {
			t.Fatalf("invalid X25519 key accepted: %v", err)
		}
	}
	if _, err := VerifyChain(chain, original.IdentityID, original.NotBefore-1); !fault.IsCode(err, fault.NotYetValid) {
		t.Fatalf("future session accepted: %v", err)
	}
}

func TestCertificatePayloadAndProtectedKeyCannotBeSubstituted(t *testing.T) {
	chain, s, key := identityFixture(t)
	payload, err := codec.Canonical(s)
	if err != nil {
		t.Fatal(err)
	}
	chain.Session, err = peercrypto.Sign(payload, "wrong-kid", peercrypto.SessionType, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, verifyErr := VerifyChain(chain, s.IdentityID, fixtureNow); !fault.IsCode(verifyErr, fault.InvalidSignature) {
		t.Fatalf("protected key substitution: %v", verifyErr)
	}
	chain.Session, err = IssueSession(s, key)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(chain.Session, ".")
	parts[1] = "A" + parts[1][1:]
	chain.Session = strings.Join(parts, ".")
	if _, verifyErr := VerifyChain(chain, s.IdentityID, fixtureNow); !fault.IsCode(verifyErr, fault.InvalidSignature) {
		t.Fatalf("tampered signed payload: %v", verifyErr)
	}
	if _, err := DecodeSession([]byte(`{"wire_version":1,"wire_version":1}`)); !fault.IsCode(err, fault.InvalidEncoding) {
		t.Fatalf("duplicate fields: %v", err)
	}
}

func TestRotationRequiresOldPinAndIdenticalCrossSignedPayload(t *testing.T) {
	oldPublic, oldKey := newKey(t)
	newPublic, newPrivate := newKey(t)
	s := RotationStatement{Version: 1, OldIdentityID: RootFingerprint(oldPublic),
		NewIdentityID: RootFingerprint(newPublic), NewRootPublicKey: newPublic,
		NotBefore: fixtureNow - 1, ExpiresAt: fixtureNow + 100}
	r, err := IssueRotation(s, oldKey, newPrivate)
	if err != nil {
		t.Fatal(err)
	}
	if _, verifyErr := VerifyRotation(r, s.OldIdentityID, fixtureNow); verifyErr != nil {
		t.Fatal(verifyErr)
	}
	if _, verifyErr := VerifyRotation(r, s.NewIdentityID, fixtureNow); !fault.IsCode(verifyErr, fault.NeedsVerify) {
		t.Fatalf("rotation accepted without old pin: %v", verifyErr)
	}
	changed := s
	changed.ExpiresAt++
	payload, err := codec.Canonical(changed)
	if err != nil {
		t.Fatal(err)
	}
	r.NewSignature, err = peercrypto.Sign(payload, s.NewIdentityID, peercrypto.RotationType, newPrivate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyRotation(r, s.OldIdentityID, fixtureNow); !fault.IsCode(err, fault.InvalidCertificate) {
		t.Fatalf("different cross-signed payload accepted: %v", err)
	}
	_, unknown := newKey(t)
	if _, err := IssueRotation(s, unknown, newPrivate); !fault.IsCode(err, fault.InvalidCertificate) {
		t.Fatalf("unrelated old root: %v", err)
	}
}
