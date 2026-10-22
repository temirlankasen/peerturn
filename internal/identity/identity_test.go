package identity

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
)

const fixtureNow int64 = 1_800_000_000

func TestVerifyChainRejectsUnpinnedRoot(t *testing.T) {
	chain, cert, _ := identityFixture(t)
	if _, err := VerifyChain(chain, "untrusted", fixtureNow); !fault.IsCode(err, fault.NeedsVerify) {
		t.Fatalf("unexpected root decision: %v", err)
	}
	verified, err := VerifyChain(chain, cert.IdentityID, fixtureNow)
	if err != nil || !EqualBinding(verified.Binding, cert.Binding) {
		t.Fatalf("valid chain rejected: %v", err)
	}
}

func TestVerifyChainRejectsExpiredAndWrongDevice(t *testing.T) {
	chain, cert, deviceKey := identityFixture(t)
	cert.DeviceID = "other-device"
	var err error
	chain.Session, err = IssueSession(cert, deviceKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyChain(chain, cert.IdentityID, fixtureNow); !fault.IsCode(err, fault.InvalidCertificate) {
		t.Fatalf("wrong device accepted: %v", err)
	}
	chain, cert, _ = identityFixture(t)
	if _, err := VerifyChain(chain, cert.IdentityID, cert.ExpiresAt); !fault.IsCode(err, fault.Expired) {
		t.Fatalf("expired chain accepted: %v", err)
	}
}

func identityFixture(t testing.TB) (Chain, SessionCertificate, ed25519.PrivateKey) {
	t.Helper()
	rootPub, root := newKey(t)
	devicePub, device := newKey(t)
	sessionPub, _ := newKey(t)
	id := RootFingerprint(rootPub)
	d := DeviceCertificate{Version: 1, IdentityID: id, DeviceID: "device-a", DeviceEpoch: 1,
		SigningKey: devicePub, NotBefore: fixtureNow - 10, ExpiresAt: fixtureNow + 7200}
	binding := Binding{IdentityID: id, DeviceID: d.DeviceID,
		DeviceEpoch: 1, SessionID: "session-a", SessionEpoch: 1, KeyEpoch: 1, SigningKey: sessionPub}
	s := SessionCertificate{Version: 1, Binding: binding,
		EncryptionKey: encryptionKey(t), NotBefore: fixtureNow - 5, ExpiresAt: fixtureNow + 3600}
	dc, err := IssueDevice(d, root)
	if err != nil {
		t.Fatal(err)
	}
	sc, err := IssueSession(s, device)
	if err != nil {
		t.Fatal(err)
	}
	return Chain{RootPublicKey: rootPub, Device: dc, Session: sc}, s, device
}

func newKey(t testing.TB) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, key
}

func encryptionKey(t testing.TB) []byte {
	t.Helper()
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key.PublicKey().Bytes()
}
