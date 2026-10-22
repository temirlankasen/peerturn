package invite

import (
	"crypto/ed25519"
	"encoding/base64"

	"github.com/temirlankasen/peerturn/internal/codec"
	ptcrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/identity"
)

// ClaimRef authenticates an invitation proof and its exact packet binding without
// consulting time. It only supports lookup of an already retained Consume result;
// new consumption still requires successful OpenClaim and local slot validation.
func ClaimRef(link Link, packet ClaimPacket) (claimID, digest string, err error) {
	proof, err := authenticateProof(link, packet)
	if err != nil {
		return "", "", err
	}
	return proof.ClaimID, proof.CiphertextHash, nil
}

// ClaimantFingerprint returns the root fingerprint for the claimant chain.
// After successful OpenClaim it is verified evidence for owner display and audit;
// it does not establish the human identity of the invitation holder.
func (c Claim) ClaimantFingerprint() string {
	return identity.RootFingerprint(c.Claimant.RootPublicKey)
}

func authenticateProof(link Link, packet ClaimPacket) (claimProof, error) {
	digest, err := PacketDigest(packet.Packet)
	if err != nil {
		return claimProof{}, err
	}
	if link.secrets == nil || len(packet.Proof) == 0 || len(packet.Proof) > 2048 || packet.Packet.InviteID != link.id {
		return claimProof{}, fault.New(fault.InvalidInvite)
	}
	key := ed25519.NewKeyFromSeed(link.secrets.signingSeed[:])
	payload, err := ptcrypto.Verify(packet.Proof, link.id, ptcrypto.InviteProofType, ed25519.PublicKey(key[ed25519.SeedSize:]))
	if err != nil {
		return claimProof{}, err
	}
	var proof claimProof
	if decodeErr := codec.Decode(payload, 1024, &proof); decodeErr != nil {
		return claimProof{}, decodeErr
	}
	if proof.Version != 1 || proof.InviteID != link.id || proof.CiphertextHash != digest || !encodedBytes(proof.ClaimID, 16) || proof.ExpiresAt <= 0 {
		return claimProof{}, fault.New(fault.InvalidInvite)
	}
	reader, err := base64.RawURLEncoding.Strict().DecodeString(proof.ReaderKey)
	if err != nil || base64.RawURLEncoding.EncodeToString(reader) != proof.ReaderKey || !ptcrypto.ValidSigningKey(reader) {
		return claimProof{}, fault.New(fault.InvalidInvite)
	}
	return proof, nil
}
