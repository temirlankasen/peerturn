package invite

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/grant"
)

func TestDescriptorRoundTripIndependentKeysAndPins(t *testing.T) {
	f := newInviteFixture(t)
	d, cert, err := OpenDescriptor(f.link, f.packet, f.descriptor.PublisherManifestHash, testNow)
	if err != nil || cert.SessionID != f.inviter.cert.SessionID || !bytes.Equal(canonical(t, d), canonical(t, f.descriptor)) {
		t.Fatal("descriptor did not verify", err)
	}
	if bytes.Equal(f.link.secrets.signingSeed[:], f.link.secrets.encryptionKey[:]) || bytes.Equal(f.link.secrets.signingSeed[:], f.inviter.sign.Seed()) || bytes.Equal(f.link.secrets.encryptionKey[:], f.inviter.key.Bytes()) {
		t.Fatal("invitation reused a session key or converted a signing seed")
	}
	wrongPin := f.link
	wrongPin.fingerprint = strings.Repeat("b", 64)
	if _, _, err := OpenDescriptor(wrongPin, f.packet, d.PublisherManifestHash, testNow); !fault.IsCode(err, fault.NeedsVerify) {
		t.Fatal("changed identity pin accepted", err)
	}
	if _, _, err := OpenDescriptor(f.link, f.packet, strings.Repeat("b", 64), testNow); !fault.IsCode(err, fault.NeedsVerify) {
		t.Fatal("untrusted publisher accepted", err)
	}
	if _, _, err := OpenDescriptor(f.link, f.packet, d.PublisherManifestHash, d.ExpiresAt); !fault.IsCode(err, fault.Expired) {
		t.Fatal("expired descriptor accepted", err)
	}
	if _, _, err := OpenDescriptor(f.link, f.packet, d.PublisherManifestHash, d.NotBefore-1); !fault.IsCode(err, fault.NotYetValid) {
		t.Fatal("future descriptor accepted", err)
	}
}

func TestEveryDescriptorFieldIsAuthenticated(t *testing.T) {
	f := newInviteFixture(t)
	compact := descriptorCompact(t, f)
	cases := map[string]func(*Descriptor){
		"version": func(d *Descriptor) { d.Version++ }, "id": func(d *Descriptor) { d.InviteID = strings.Repeat("a", 22) },
		"root":   func(d *Descriptor) { d.Inviter.RootPublicKey = bytes.Repeat([]byte{1}, 32) },
		"device": func(d *Descriptor) { d.Inviter.Device += "x" }, "session": func(d *Descriptor) { d.Inviter.Session += "x" },
		"forward": func(d *Descriptor) { d.ForwardScope = grant.Ask }, "return": func(d *Descriptor) { d.ReturnScope = grant.Inform },
		"relay":      func(d *Descriptor) { d.RelayOrigin = "https://evil.example" },
		"not_before": func(d *Descriptor) { d.NotBefore-- }, "expires": func(d *Descriptor) { d.ExpiresAt++ },
		"uses": func(d *Descriptor) { d.MaxUses++ }, "publisher": func(d *Descriptor) { d.PublisherManifestHash = strings.Repeat("b", 64) },
		"voucher": func(d *Descriptor) { d.EnrollmentVoucher += "x" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			d := f.descriptor
			mutate(&d)
			link, packet := sealDescriptor(t, f.link, f.inviter.chain, compactWithPayload(t, compact, d))
			if _, _, err := OpenDescriptor(link, packet, f.descriptor.PublisherManifestHash, testNow); !fault.IsCode(err, fault.InvalidSignature) {
				t.Fatal("tampered signed descriptor accepted", err)
			}
		})
	}
}

func TestDescriptorCiphertextHashAndEncryption(t *testing.T) {
	f := newInviteFixture(t)
	packet := f.packet
	packet.Ciphertext = bytes.Clone(packet.Ciphertext)
	packet.Ciphertext[0] ^= 1
	if _, _, err := OpenDescriptor(f.link, packet, f.descriptor.PublisherManifestHash, testNow); !fault.IsCode(err, fault.InvalidInvite) {
		t.Fatal("changed encrypted descriptor hash accepted", err)
	}
	link := f.link
	var err error
	link.descriptorHash, err = PacketDigest(packet)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, openErr := OpenDescriptor(link, packet, f.descriptor.PublisherManifestHash, testNow); !fault.IsCode(openErr, fault.DecryptionFailed) {
		t.Fatal("tampered ciphertext decrypted", openErr)
	}
	link = f.link
	secrets := *link.secrets
	link.secrets = &secrets
	encryptionKey := *link.secrets.encryptionKey
	link.secrets.encryptionKey = &encryptionKey
	link.secrets.encryptionKey[10] ^= 1
	if _, _, openErr := OpenDescriptor(link, f.packet, f.descriptor.PublisherManifestHash, testNow); !fault.IsCode(openErr, fault.DecryptionFailed) {
		t.Fatal("wrong invitation decryption key accepted", openErr)
	}
	// Context kinds separate a descriptor from a claim even under the same key.
	key, err := ecdh.X25519().NewPrivateKey(f.link.secrets.encryptionKey[:])
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(key.PublicKey().Bytes(), ed25519.NewKeyFromSeed(f.link.secrets.signingSeed[:])[32:]) {
		t.Fatal("independent key types unexpectedly equal")
	}
}

func TestDescriptorRejectsInvalidSignedPolicy(t *testing.T) {
	f := newInviteFixture(t)
	cases := []struct {
		mutate func(*Descriptor)
		code   fault.Code
	}{
		{func(d *Descriptor) { d.ForwardScope = "receipt" }, fault.InvalidGrant},
		{func(d *Descriptor) { d.ReturnScope = "unknown" }, fault.InvalidGrant},
		{func(d *Descriptor) { d.ExpiresAt = d.NotBefore + MaxLifetime + 1 }, fault.InvalidInvite},
		{func(d *Descriptor) { d.MaxUses = 2 }, fault.InvalidInvite},
		{func(d *Descriptor) { d.EnrollmentVoucher = "" }, fault.InvalidInvite},
		{func(d *Descriptor) { d.RelayOrigin += "/path" }, fault.InvalidInvite},
		{func(d *Descriptor) { d.PublisherManifestHash = "untrusted" }, fault.InvalidInvite},
	}
	for i, test := range cases {
		d := f.descriptor
		d.InviteID = ""
		test.mutate(&d)
		if _, _, err := Create(d, f.inviter.sign, testNow); !fault.IsCode(err, test.code) {
			t.Fatalf("invalid policy %d accepted: %v", i, err)
		}
	}
	d := f.descriptor
	d.InviteID, d.ForwardScope = "", "unknown"
	if _, _, err := Create(d, f.inviter.sign, testNow); !fault.IsCode(err, fault.InvalidGrant) {
		t.Fatal("invalid invitation scope accepted", err)
	}
	d.ForwardScope = grant.Coauthor
	if _, _, err := Create(d, f.claimant.sign, testNow); !fault.IsCode(err, fault.InvalidSignature) {
		t.Fatal("non-inviter signing key accepted", err)
	}
}
