package invite

import (
	"bytes"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
)

func TestClaimRefRecoversLostAcknowledgmentAfterExpiry(t *testing.T) {
	f := newInviteFixture(t)
	beforeExpiry := f.claim.ExpiresAt - 1
	claim, err := OpenClaim(f.link, f.descriptor, f.claimWire, f.inviter.key, "", 1, beforeExpiry)
	if err != nil {
		t.Fatal("new claim failed admission", err)
	}
	claimID, digest, err := ClaimRef(f.link, f.claimWire)
	if err != nil || claimID != claim.ClaimID {
		t.Fatal("verified reference differs from admitted claim", err)
	}
	initial, err := NewState(f.link.ID(), f.descriptor.ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	stored, result, err := initial.Consume(f.link.ID(), claimID, digest, []byte("opaque-encrypted-grant-response"), beforeExpiry)
	if err != nil {
		t.Fatal(err)
	}
	// The response is lost, and the exact packet is retried after both lifetimes.
	afterExpiry := f.descriptor.ExpiresAt + 1
	if _, openErr := OpenClaim(f.link, f.descriptor, f.claimWire, f.inviter.key, "", 1, afterExpiry); !fault.IsCode(openErr, fault.Expired) {
		t.Fatal("expired claim gained new admission", openErr)
	}
	retryID, retryDigest, err := ClaimRef(f.link, f.claimWire)
	if err != nil || retryID != claimID || retryDigest != digest {
		t.Fatal("expired retry lost its authenticated reference", err)
	}
	_, retry, err := stored.Revoke().Consume(f.link.ID(), retryID, retryDigest, nil, afterExpiry)
	if err != nil || !bytes.Equal(retry, result) {
		t.Fatal("lost ACK retry did not return retained result", err)
	}
	if _, _, consumeErr := initial.Consume(f.link.ID(), retryID, retryDigest, result, afterExpiry); !fault.IsCode(consumeErr, fault.Expired) {
		t.Fatal("reference alone authorized expired first consumption", consumeErr)
	}
}

func TestClaimRefRequiresValidProofAndExactPacketBinding(t *testing.T) {
	f := newInviteFixture(t)
	changedCiphertext := f.claimWire
	changedCiphertext.Packet.Ciphertext = bytes.Clone(changedCiphertext.Packet.Ciphertext)
	changedCiphertext.Packet.Ciphertext[0] ^= 1
	changedID := f.claimWire
	changedID.Packet.InviteID = f.claim.ClaimID
	forgedProof := f.claimWire
	forgedProof.Proof = compactWithPayload(t, forgedProof.Proof, struct{}{})
	for _, test := range []struct {
		packet ClaimPacket
		code   fault.Code
	}{
		{changedCiphertext, fault.InvalidInvite}, {changedID, fault.InvalidInvite},
		{forgedProof, fault.InvalidSignature}, {ClaimPacket{}, fault.UnsupportedVersion},
	} {
		id, digest, err := ClaimRef(f.link, test.packet)
		if !fault.IsCode(err, test.code) || id != "" || digest != "" {
			t.Fatal("unauthenticated reference escaped proof validation", err)
		}
	}
	if id, digest, err := ClaimRef(Link{}, f.claimWire); !fault.IsCode(err, fault.InvalidInvite) || id != "" || digest != "" {
		t.Fatal("zero link authenticated a reference")
	}
}
