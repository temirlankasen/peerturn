package invite

import (
	"crypto/ed25519"
	"testing"

	ptcrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/identity"
)

func FuzzSignedDescriptorPayload(f *testing.F) {
	fixture := newInviteFixture(f)
	f.Add(canonical(f, fixture.descriptor))
	f.Add([]byte(`{"wire_version":1,"wire_version":1}`))
	f.Fuzz(func(t *testing.T, payload []byte) {
		if len(payload) > 12<<10 {
			return
		}
		signed, err := ptcrypto.Sign(payload, identity.RootFingerprint(fixture.inviter.cert.SigningKey), ptcrypto.InviteType, fixture.inviter.sign)
		if err != nil {
			return
		}
		link, packet := sealDescriptor(t, fixture.link, fixture.inviter.chain, signed)
		descriptor, cert, openErr := OpenDescriptor(link, packet, fixture.descriptor.PublisherManifestHash, testNow)
		if openErr != nil && (descriptor.Version != 0 || cert.Version != 0) {
			t.Fatal("invalid verified payload exposed partial authority")
		}
	})
}

func FuzzSignedClaimPayload(f *testing.F) {
	fixture := newInviteFixture(f)
	f.Add(canonical(f, fixture.claim))
	f.Add([]byte(`{"wire_version":1,"wire_version":1}`))
	f.Fuzz(func(t *testing.T, payload []byte) {
		if len(payload) > 12<<10 {
			return
		}
		signed, err := ptcrypto.Sign(payload, identity.RootFingerprint(fixture.claimant.cert.SigningKey), ptcrypto.ClaimType, fixture.claimant.sign)
		if err != nil {
			return
		}
		packet := sealClaim(t, fixture, signed)
		claim, openErr := OpenClaim(fixture.link, fixture.descriptor, packet, fixture.inviter.key, fixture.claimant.cert.IdentityID, 1, testNow)
		if openErr != nil && claim.Version != 0 {
			t.Fatal("invalid verified claim exposed partial authority")
		}
	})
}

func FuzzSignedProofPayload(f *testing.F) {
	fixture := newInviteFixture(f)
	f.Add(canonical(f, decodeProof(f, fixture.claimWire)))
	f.Add([]byte(`{"wire_version":1,"wire_version":1}`))
	f.Fuzz(func(t *testing.T, payload []byte) {
		if len(payload) > 1024 {
			return
		}
		signed, err := ptcrypto.Sign(payload, fixture.link.ID(), ptcrypto.InviteProofType, ed25519.NewKeyFromSeed(fixture.link.secrets.signingSeed[:]))
		if err != nil {
			return
		}
		packet := fixture.claimWire
		packet.Proof = signed
		proof, verifyErr := verifyProof(fixture.link, fixture.descriptor, packet, testNow)
		if verifyErr != nil && proof.Version != 0 {
			t.Fatal("invalid verified proof exposed partial authority")
		}
		claimID, digest, refErr := ClaimRef(fixture.link, packet)
		if refErr != nil && (claimID != "" || digest != "") {
			t.Fatal("invalid proof exposed a retained-result reference")
		}
	})
}
