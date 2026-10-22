package invite

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/codec"
	ptcrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/identity"
)

const testNow int64 = 1_800_000_000

type sessionFixture struct {
	chain identity.Chain
	cert  identity.SessionCertificate
	sign  ed25519.PrivateKey
	key   *ecdh.PrivateKey
}

type inviteFixture struct {
	link       Link
	descriptor Descriptor
	packet     Packet
	inviter    sessionFixture
	claimant   sessionFixture
	claim      Claim
	claimWire  ClaimPacket
}

func newSessionFixture(t testing.TB, name string) sessionFixture {
	t.Helper()
	rootPub, root, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	devicePub, device, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sessionPub, signing, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := identity.RootFingerprint(rootPub)
	dc, err := identity.IssueDevice(identity.DeviceCertificate{Version: 1, IdentityID: id, DeviceID: "device-" + name,
		DeviceEpoch: 1, SigningKey: devicePub, NotBefore: testNow - 100, ExpiresAt: testNow + 7200}, root)
	if err != nil {
		t.Fatal(err)
	}
	cert := identity.SessionCertificate{Version: 1, IdentityID: id, DeviceID: "device-" + name,
		DeviceEpoch: 1, SessionID: "session-" + name, SessionEpoch: 1, KeyEpoch: 1, SigningKey: sessionPub,
		EncryptionKey: key.PublicKey().Bytes(), NotBefore: testNow - 50, ExpiresAt: testNow + 3600}
	sc, err := identity.IssueSession(cert, device)
	if err != nil {
		t.Fatal(err)
	}
	return sessionFixture{identity.Chain{RootPublicKey: rootPub, Device: dc, Session: sc}, cert, signing, key}
}

func newInviteFixture(t testing.TB) inviteFixture {
	t.Helper()
	inviter, claimant := newSessionFixture(t, "inviter"), newSessionFixture(t, "claimant")
	descriptor := Descriptor{Version: 1, Inviter: inviter.chain, ForwardScope: grant.Coauthor, ReturnScope: grant.Coauthor,
		RelayOrigin: "https://relay.example", NotBefore: testNow, ExpiresAt: testNow + MaxLifetime, MaxUses: 1,
		PublisherManifestHash: strings.Repeat("a", 64), EnrollmentVoucher: "synthetic-one-mailbox-voucher"}
	link, packet, err := Create(descriptor, inviter.sign, testNow)
	if err != nil {
		t.Fatal(err)
	}
	descriptor.InviteID = link.ID()
	returned, err := grant.Issue(grant.Grant{Version: 1, GrantID: "return-grant", Issuer: claimant.cert.Binding,
		Subject: inviter.cert.Binding, Scope: grant.Coauthor, GrantEpoch: 1, NotBefore: testNow, ExpiresAt: testNow + 1800,
		Limits: grant.DefaultLimits()}, claimant.sign)
	if err != nil {
		t.Fatal(err)
	}
	claim := Claim{Version: 1, InviteID: link.ID(), ClaimID: base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("c", 16))),
		Claimant: claimant.chain, Audience: inviter.cert.Binding, RequestedScope: grant.Coauthor, ReturnGrant: returned,
		RoutingKey: []byte(strings.Repeat("r", 32)), MailboxKey: []byte(strings.Repeat("m", 32)),
		NotBefore: testNow, ExpiresAt: testNow + 300}
	claimWire, err := CreateClaim(link, descriptor, claim, claimant.sign, 1, testNow)
	if err != nil {
		t.Fatal(err)
	}
	return inviteFixture{link, descriptor, packet, inviter, claimant, claim, claimWire}
}

func canonical(t testing.TB, value any) []byte {
	t.Helper()
	data, err := codec.Canonical(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func sealDescriptor(t testing.TB, link Link, chain identity.Chain, compact string) (Link, Packet) {
	t.Helper()
	key, err := ecdh.X25519().NewPrivateKey(link.secrets.encryptionKey[:])
	if err != nil {
		t.Fatal(err)
	}
	aad, err := packetAAD(link.ID(), "descriptor")
	if err != nil {
		t.Fatal(err)
	}
	enc, ciphertext, err := ptcrypto.SealFor(ptcrypto.DescriptorContext, key.PublicKey(), aad, canonical(t, signedContent{chain, compact}))
	if err != nil {
		t.Fatal(err)
	}
	packet := Packet{1, link.ID(), enc, ciphertext}
	link.descriptorHash, err = PacketDigest(packet)
	if err != nil {
		t.Fatal(err)
	}
	return link, packet
}

func compactWithPayload(t testing.TB, compact string, value any) string {
	t.Helper()
	parts := strings.Split(compact, ".")
	if len(parts) != 3 {
		t.Fatal("malformed synthetic JWS")
	}
	parts[1] = base64.RawURLEncoding.EncodeToString(canonical(t, value))
	return strings.Join(parts, ".")
}

func descriptorCompact(t testing.TB, fixture inviteFixture) string {
	t.Helper()
	key, err := ecdh.X25519().NewPrivateKey(fixture.link.secrets.encryptionKey[:])
	if err != nil {
		t.Fatal(err)
	}
	aad, err := packetAAD(fixture.link.ID(), "descriptor")
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := ptcrypto.OpenFor(ptcrypto.DescriptorContext, key, fixture.packet.Enc, aad, fixture.packet.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	content, err := decodeSignedContent(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	return content.Signed
}
