package invite

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
)

func TestFirstContactClaimAuthenticatesWithoutPriorPin(t *testing.T) {
	f := newInviteFixture(t)
	claim, err := OpenClaim(f.link, f.descriptor, f.claimWire, f.inviter.key, "", 1, testNow)
	if err != nil || !bytes.Equal(canonical(t, claim), canonical(t, f.claim)) {
		t.Fatal("first contact requires proof-authenticated claimant chain", err)
	}
	if claim.ClaimantFingerprint() != f.claimant.cert.IdentityID {
		t.Fatal("first contact did not return the verified claimant fingerprint")
	}
}

func TestFirstContactRequiresProofBeforeDecryption(t *testing.T) {
	f := newInviteFixture(t)
	packet := f.claimWire
	packet.Proof = compactWithPayload(t, packet.Proof, struct{}{})
	got, err := OpenClaim(f.link, f.descriptor, packet, nil, "", 1, testNow)
	if !fault.IsCode(err, fault.InvalidSignature) || !reflect.DeepEqual(got, Claim{}) {
		t.Fatal("first contact inspected claimant before invitation proof", err)
	}
}

func TestFirstContactRejectsForgedSessionSignature(t *testing.T) {
	f := newInviteFixture(t)
	packet := sealClaim(t, f, compactWithPayload(t, claimCompact(t, f), struct{}{}))
	got, err := OpenClaim(f.link, f.descriptor, packet, f.inviter.key, "", 1, testNow)
	if !fault.IsCode(err, fault.InvalidSignature) || !reflect.DeepEqual(got, Claim{}) {
		t.Fatal("proof alone established session authority", err)
	}
}

func TestFirstContactAuthenticatesDerivedRootChain(t *testing.T) {
	f := newInviteFixture(t)
	compact := claimCompact(t, f)
	f.claimant.chain.RootPublicKey = f.inviter.chain.RootPublicKey
	packet := sealClaim(t, f, compact)
	got, err := OpenClaim(f.link, f.descriptor, packet, f.inviter.key, "", 1, testNow)
	if !fault.IsCode(err, fault.InvalidSignature) || !reflect.DeepEqual(got, Claim{}) {
		t.Fatal("derived fingerprint bypassed chain authentication")
	}
}
