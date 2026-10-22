package invite

import (
	"encoding/base64"
	"reflect"
	"strings"
	"testing"

	ptcrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/identity"
)

func TestSignedJSONIsNeverParsedBeforeSignature(t *testing.T) {
	f := newInviteFixture(t)
	for _, invalidJSON := range []string{"{", `{"wire_version":1,"wire_version":1}`, `{"unexpected":"sensitive"}`} {
		parts := strings.Split(descriptorCompact(t, f), ".")
		parts[1] = base64.RawURLEncoding.EncodeToString([]byte(invalidJSON))
		link, packet := sealDescriptor(t, f.link, f.inviter.chain, strings.Join(parts, "."))
		if _, _, err := OpenDescriptor(link, packet, f.descriptor.PublisherManifestHash, testNow); !fault.IsCode(err, fault.InvalidSignature) {
			t.Fatal("descriptor JSON parsed before rejecting signature", err)
		}
		parts = strings.Split(claimCompact(t, f), ".")
		parts[1] = base64.RawURLEncoding.EncodeToString([]byte(invalidJSON))
		claimPacket := sealClaim(t, f, strings.Join(parts, "."))
		if _, err := OpenClaim(f.link, f.descriptor, claimPacket, f.inviter.key, f.claimant.cert.IdentityID, 1, testNow); !fault.IsCode(err, fault.InvalidSignature) {
			t.Fatal("claim JSON parsed before rejecting signature", err)
		}
	}
}

func TestDescriptorValidationErrorsReturnNoPartialAuthority(t *testing.T) {
	f := newInviteFixture(t)
	for _, scope := range []grant.Scope{"unknown", "receipt"} {
		d := f.descriptor
		d.ForwardScope = scope
		signed, err := ptcrypto.Sign(canonical(t, d), identity.RootFingerprint(f.inviter.cert.SigningKey), ptcrypto.InviteType, f.inviter.sign)
		if err != nil {
			t.Fatal(err)
		}
		link, packet := sealDescriptor(t, f.link, f.inviter.chain, signed)
		got, cert, openErr := OpenDescriptor(link, packet, d.PublisherManifestHash, testNow)
		if !fault.IsCode(openErr, fault.InvalidGrant) || !reflect.DeepEqual(got, Descriptor{}) || !reflect.DeepEqual(cert, identity.SessionCertificate{}) {
			t.Fatal("rejected descriptor leaked partial authority", openErr)
		}
	}
	d, cert, err := OpenDescriptor(f.link, f.packet, f.descriptor.PublisherManifestHash, f.descriptor.ExpiresAt)
	if !fault.IsCode(err, fault.Expired) || !reflect.DeepEqual(d, Descriptor{}) || !reflect.DeepEqual(cert, identity.SessionCertificate{}) {
		t.Fatal("expired descriptor leaked partial authority", err)
	}
}

func TestClaimValidationErrorsReturnNoPartialAuthority(t *testing.T) {
	f := newInviteFixture(t)
	for _, scope := range []grant.Scope{"unknown", "receipt"} {
		claim := f.claim
		claim.RequestedScope = scope
		signed, err := ptcrypto.Sign(canonical(t, claim), identity.RootFingerprint(f.claimant.cert.SigningKey), ptcrypto.ClaimType, f.claimant.sign)
		if err != nil {
			t.Fatal(err)
		}
		packet := sealClaim(t, f, signed)
		got, openErr := OpenClaim(f.link, f.descriptor, packet, f.inviter.key, f.claimant.cert.IdentityID, 1, testNow)
		if !fault.IsCode(openErr, fault.InvalidGrant) || !reflect.DeepEqual(got, Claim{}) {
			t.Fatal("rejected claim leaked partial authority", openErr)
		}
	}
	got, err := OpenClaim(f.link, f.descriptor, f.claimWire, f.inviter.key, f.claimant.cert.IdentityID, 1, f.claim.ExpiresAt)
	if !fault.IsCode(err, fault.Expired) || !reflect.DeepEqual(got, Claim{}) {
		t.Fatal("expired claim leaked partial authority", err)
	}
}

func TestEncryptedChainHintMustMatchSignedChain(t *testing.T) {
	f := newInviteFixture(t)
	d := f.descriptor
	d.Inviter = f.claimant.chain
	signed, err := ptcrypto.Sign(canonical(t, d), identity.RootFingerprint(f.inviter.cert.SigningKey), ptcrypto.InviteType, f.inviter.sign)
	if err != nil {
		t.Fatal(err)
	}
	link, packet := sealDescriptor(t, f.link, f.inviter.chain, signed)
	if _, _, openErr := OpenDescriptor(link, packet, d.PublisherManifestHash, testNow); !fault.IsCode(openErr, fault.InvalidCertificate) {
		t.Fatal("signed descriptor changed its chain hint", openErr)
	}
	claim := f.claim
	claim.Claimant = f.inviter.chain
	signed, err = ptcrypto.Sign(canonical(t, claim), identity.RootFingerprint(f.claimant.cert.SigningKey), ptcrypto.ClaimType, f.claimant.sign)
	if err != nil {
		t.Fatal(err)
	}
	claimPacket := sealClaim(t, f, signed)
	if _, openErr := OpenClaim(f.link, f.descriptor, claimPacket, f.inviter.key, f.claimant.cert.IdentityID, 1, testNow); !fault.IsCode(openErr, fault.InvalidCertificate) {
		t.Fatal("signed claim changed its chain hint", openErr)
	}
}
