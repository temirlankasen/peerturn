package invite

import (
	"bytes"
	"encoding/base64"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
)

func TestDescriptorRoutingKeysAreOptionalButMustBeValidTogether(t *testing.T) {
	f := newInviteFixture(t)
	d := f.descriptor
	d.InviteID = ""
	d.RoutingKey, d.MailboxKey = f.inviter.cert.SigningKey, f.claimant.cert.SigningKey
	link, packet, err := Create(d, f.inviter.sign, testNow)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := OpenDescriptor(link, packet, d.PublisherManifestHash, testNow)
	if err != nil || !bytes.Equal(got.RoutingKey, d.RoutingKey) || !bytes.Equal(got.MailboxKey, d.MailboxKey) {
		t.Fatal("signed routing keys lost", err)
	}
	for _, mutate := range []func(*Descriptor){
		func(d *Descriptor) { d.RoutingKey = nil },
		func(d *Descriptor) { d.MailboxKey = nil },
		func(d *Descriptor) { d.RoutingKey = make([]byte, 32) },
		func(d *Descriptor) { d.MailboxKey = []byte{1} },
		func(d *Descriptor) { d.MailboxKey = bytes.Repeat([]byte{255}, 32) },
	} {
		invalid := d
		mutate(&invalid)
		if _, _, createErr := Create(invalid, f.inviter.sign, testNow); !fault.IsCode(createErr, fault.InvalidInvite) {
			t.Fatal("invalid optional routing key accepted", createErr)
		}
	}
	if bytes.Contains(canonical(t, f.descriptor), []byte("routing_key")) || bytes.Contains(canonical(t, f.descriptor), []byte("mailbox_key")) {
		t.Fatal("legacy descriptor gained empty routing fields")
	}
}

func TestDescriptorRoutingKeysCannotChangeUnderOriginalSignature(t *testing.T) {
	f := newInviteFixture(t)
	d := f.descriptor
	d.RoutingKey, d.MailboxKey = f.inviter.cert.SigningKey, f.claimant.cert.SigningKey
	link, packet := sealDescriptor(t, f.link, f.inviter.chain, compactWithPayload(t, descriptorCompact(t, f), d))
	if _, _, err := OpenDescriptor(link, packet, d.PublisherManifestHash, testNow); !fault.IsCode(err, fault.InvalidSignature) {
		t.Fatal("routing key substitution accepted", err)
	}
}

func TestClaimProofPinsRelayReaderToEncryptedMailboxKey(t *testing.T) {
	f := newInviteFixture(t)
	proof := decodeProof(t, f.claimWire)
	if proof.ReaderKey != base64.RawURLEncoding.EncodeToString(f.claim.MailboxKey) {
		t.Fatal("claim relay proof omitted mailbox reader")
	}
	proof.ReaderKey = base64.RawURLEncoding.EncodeToString(f.inviter.cert.SigningKey)
	changed := signProof(t, f.link, f.claimWire.Packet, proof)
	if _, err := OpenClaim(f.link, f.descriptor, changed, f.inviter.key, "", 1, testNow); !fault.IsCode(err, fault.InvalidInvite) {
		t.Fatal("proof capability substituted encrypted mailbox owner", err)
	}
	for _, invalid := range []string{"", "malformed", base64.RawURLEncoding.EncodeToString(make([]byte, 32))} {
		proof.ReaderKey = invalid
		changed = signProof(t, f.link, f.claimWire.Packet, proof)
		if _, _, err := ClaimRef(f.link, changed); !fault.IsCode(err, fault.InvalidInvite) {
			t.Fatal("invalid reader key authenticated a claim reference", err)
		}
	}
}
