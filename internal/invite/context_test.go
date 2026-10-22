package invite

import (
	"crypto/ecdh"
	"testing"

	ptcrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/fault"
)

func TestInvitationProducersUseDedicatedHPKEContexts(t *testing.T) {
	f := newInviteFixture(t)
	key, err := ecdh.X25519().NewPrivateKey(f.link.secrets.encryptionKey[:])
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		kind   string
		key    *ecdh.PrivateKey
		packet Packet
	}{
		{"descriptor", key, f.packet}, {"claim", f.inviter.key, f.claimWire.Packet},
	} {
		aad, err := packetAAD(f.link.ID(), test.kind)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ptcrypto.OpenFor(ptcrypto.EnvelopeContext, test.key, test.packet.Enc, aad, test.packet.Ciphertext); !fault.IsCode(err, fault.DecryptionFailed) {
			t.Fatal("invitation producer used envelope context", err)
		}
	}
}

func TestInvitationReceiversRejectEnvelopeHPKEContext(t *testing.T) {
	f := newInviteFixture(t)
	key, err := ecdh.X25519().NewPrivateKey(f.link.secrets.encryptionKey[:])
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"descriptor", "claim"} {
		publicKey := key.PublicKey()
		content := signedContent{f.inviter.chain, descriptorCompact(t, f)}
		if kind == "claim" {
			publicKey = f.inviter.key.PublicKey()
			content = signedContent{f.claimant.chain, claimCompact(t, f)}
		}
		aad, err := packetAAD(f.link.ID(), kind)
		if err != nil {
			t.Fatal(err)
		}
		enc, ciphertext, err := ptcrypto.SealFor(ptcrypto.EnvelopeContext, publicKey, aad, canonical(t, content))
		if err != nil {
			t.Fatal(err)
		}
		packet := Packet{1, f.link.ID(), enc, ciphertext}
		digest, err := PacketDigest(packet)
		if err != nil {
			t.Fatal(err)
		}
		if kind == "descriptor" {
			link := f.link
			link.descriptorHash = digest
			if _, _, err := OpenDescriptor(link, packet, f.descriptor.PublisherManifestHash, testNow); !fault.IsCode(err, fault.DecryptionFailed) {
				t.Fatal("descriptor accepted envelope context", err)
			}
		} else {
			proof := decodeProof(t, f.claimWire)
			proof.CiphertextHash = digest
			claimPacket := signProof(t, f.link, packet, proof)
			if _, err := OpenClaim(f.link, f.descriptor, claimPacket, f.inviter.key, "", 1, testNow); !fault.IsCode(err, fault.DecryptionFailed) {
				t.Fatal("claim accepted envelope context", err)
			}
		}
	}
}
