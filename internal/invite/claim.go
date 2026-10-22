package invite

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"encoding/base64"

	"github.com/temirlankasen/peerturn/internal/codec"
	ptcrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/identity"
)

// Claim is signed by the joining session and encrypted to the inviter session.
type Claim struct {
	// Version fixes the claim's signed wire profile.
	Version int `json:"wire_version"`
	// InviteID prevents moving the claim to another invitation slot.
	InviteID string `json:"invite_id"`
	// ClaimID identifies a retry without authorizing another use.
	ClaimID string `json:"claim_id"`
	// Claimant pins the joining session that signs this request.
	Claimant identity.Chain `json:"claimant"`
	// Audience binds every identity, device, session, key, and continuity epoch.
	Audience identity.Binding `json:"audience"`
	// RequestedScope cannot exceed the inviter's signed forward ceiling.
	RequestedScope grant.Scope `json:"requested_scope"`
	// ReturnGrant is issued by the joining recipient for the reverse direction.
	ReturnGrant string `json:"return_grant"`
	// RoutingKey authorizes relay routing independently of application scope.
	RoutingKey []byte `json:"routing_key"`
	// MailboxKey identifies the joining mailbox's public routing authority.
	MailboxKey []byte `json:"mailbox_key"`
	// NotBefore prevents using a claim before its signed validity window.
	NotBefore int64 `json:"not_before"`
	// ExpiresAt cannot extend the invitation's short capability lifetime.
	ExpiresAt int64 `json:"expires_at"`
}

// ClaimPacket exposes only ciphertext and an invitation-key proof to the relay.
type ClaimPacket struct {
	// Packet keeps the joining session, grants, and scopes hidden from the relay.
	Packet Packet `json:"packet"`
	// Proof authenticates slot possession without disclosing the fragment secret.
	Proof string `json:"proof"`
}

type claimProof struct {
	// Version prevents accepting a differently interpreted proof profile.
	Version int `json:"wire_version"`
	// InviteID binds the external proof to one relay slot.
	InviteID string `json:"invite_id"`
	// ClaimID binds duplicate detection to the encrypted claimant's identifier.
	ClaimID string `json:"claim_id"`
	// ReaderKey binds relay claim reads to the encrypted joining mailbox key.
	ReaderKey string `json:"reader_key"`
	// CiphertextHash authenticates canonical packet bytes, including HPKE enc.
	CiphertextHash string `json:"ciphertext_hash"`
	// ExpiresAt prevents replaying a proof beyond its encrypted claim's lifetime.
	ExpiresAt int64 `json:"expires_at"`
}

// DecodeClaimPacket strictly decodes a bounded encrypted claim and its proof.
func DecodeClaimPacket(data []byte) (ClaimPacket, error) {
	var packet ClaimPacket
	if err := codec.Decode(data, MaxPacketBytes, &packet); err != nil {
		return ClaimPacket{}, err
	}
	if err := validatePacket(packet.Packet); err != nil {
		return ClaimPacket{}, err
	}
	if len(packet.Proof) == 0 || len(packet.Proof) > 2048 {
		return ClaimPacket{}, fault.New(fault.InvalidInvite)
	}
	return packet, nil
}

// CreateClaim seals a joining session signature and return grant to the inviter.
// The caller supplies its locally authoritative return-grant epoch.
func CreateClaim(link Link, descriptor Descriptor, claim Claim, signingKey ed25519.PrivateKey, returnGrantEpoch uint64, now int64) (ClaimPacket, error) {
	inviter, err := validateDescriptor(descriptor, link.fingerprint, now)
	if err != nil {
		return ClaimPacket{}, err
	}
	claimant, err := validateClaim(link, descriptor, claim, identity.RootFingerprint(claim.Claimant.RootPublicKey), inviter, returnGrantEpoch, now)
	if err != nil {
		return ClaimPacket{}, err
	}
	if len(signingKey) != ed25519.PrivateKeySize || !bytes.Equal(signingKey[ed25519.SeedSize:], claimant.SigningKey) {
		return ClaimPacket{}, fault.New(fault.InvalidSignature)
	}
	payload, err := codec.Canonical(claim)
	if err != nil {
		return ClaimPacket{}, err
	}
	signed, err := ptcrypto.Sign(payload, identity.RootFingerprint(claimant.SigningKey), ptcrypto.ClaimType, signingKey)
	if err != nil {
		return ClaimPacket{}, err
	}
	content, err := codec.Canonical(signedContent{claim.Claimant, signed})
	if err != nil {
		return ClaimPacket{}, err
	}
	key, err := ecdh.X25519().NewPublicKey(inviter.EncryptionKey)
	if err != nil {
		return ClaimPacket{}, fault.New(fault.InvalidCertificate)
	}
	aad, err := packetAAD(link.id, "claim")
	if err != nil {
		return ClaimPacket{}, err
	}
	enc, ciphertext, err := ptcrypto.SealFor(ptcrypto.ClaimContext, key, aad, content)
	if err != nil {
		return ClaimPacket{}, err
	}
	packet := Packet{Version: 1, InviteID: link.id, Enc: enc, Ciphertext: ciphertext}
	digest, err := PacketDigest(packet)
	if err != nil {
		return ClaimPacket{}, err
	}
	proofPayload, err := codec.Canonical(claimProof{Version: 1, InviteID: link.id, ClaimID: claim.ClaimID, ReaderKey: base64.RawURLEncoding.EncodeToString(claim.MailboxKey), CiphertextHash: digest, ExpiresAt: claim.ExpiresAt})
	if err != nil {
		return ClaimPacket{}, err
	}
	if link.secrets == nil {
		return ClaimPacket{}, fault.New(fault.InvalidInvite)
	}
	inviteKey := ed25519.NewKeyFromSeed(link.secrets.signingSeed[:])
	proof, err := ptcrypto.Sign(proofPayload, link.id, ptcrypto.InviteProofType, inviteKey)
	if err != nil {
		return ClaimPacket{}, err
	}
	return ClaimPacket{Packet: packet, Proof: proof}, nil
}

// OpenClaim validates proof, HPKE, the joining identity, session signature,
// exact audience, requested scope, and its recipient-issued directional return grant.
// descriptor must be the inviter's locally issued policy, not a peer-supplied copy.
// An empty claimantPin enables first contact: only after the invitation proof
// verifies is the fingerprint derived from the chain and that chain authenticated.
func OpenClaim(link Link, descriptor Descriptor, packet ClaimPacket, encryptionKey *ecdh.PrivateKey, claimantPin string, returnGrantEpoch uint64, now int64) (Claim, error) {
	inviter, err := validateDescriptor(descriptor, link.fingerprint, now)
	if err != nil {
		return Claim{}, err
	}
	proof, err := verifyProof(link, descriptor, packet, now)
	if err != nil {
		return Claim{}, err
	}
	if encryptionKey == nil || !bytes.Equal(encryptionKey.PublicKey().Bytes(), inviter.EncryptionKey) {
		return Claim{}, fault.New(fault.AudienceMismatch)
	}
	aad, err := packetAAD(link.id, "claim")
	if err != nil {
		return Claim{}, err
	}
	plaintext, err := ptcrypto.OpenFor(ptcrypto.ClaimContext, encryptionKey, packet.Packet.Enc, aad, packet.Packet.Ciphertext)
	if err != nil {
		return Claim{}, err
	}
	content, err := decodeSignedContent(plaintext)
	if err != nil {
		return Claim{}, err
	}
	if claimantPin == "" {
		claimantPin = identity.RootFingerprint(content.Chain.RootPublicKey)
	}
	verified, chain, _, err := verifyContent(content, claimantPin, ptcrypto.ClaimType, now)
	if err != nil {
		return Claim{}, err
	}
	var claim Claim
	if decodeErr := codec.Decode(verified, MaxPacketBytes/2, &claim); decodeErr != nil {
		return Claim{}, decodeErr
	}
	if !equalChain(chain, claim.Claimant) {
		return Claim{}, fault.New(fault.InvalidCertificate)
	}
	if claim.ClaimID != proof.ClaimID || claim.ExpiresAt != proof.ExpiresAt || base64.RawURLEncoding.EncodeToString(claim.MailboxKey) != proof.ReaderKey {
		return Claim{}, fault.New(fault.InvalidInvite)
	}
	_, err = validateClaim(link, descriptor, claim, claimantPin, inviter, returnGrantEpoch, now)
	if err != nil {
		return Claim{}, err
	}
	return claim, nil
}

func verifyProof(link Link, descriptor Descriptor, packet ClaimPacket, now int64) (claimProof, error) {
	proof, err := authenticateProof(link, packet)
	if err != nil {
		return claimProof{}, err
	}
	if descriptor.InviteID != link.id || descriptor.RelayOrigin != link.origin || proof.ExpiresAt > descriptor.ExpiresAt {
		return claimProof{}, fault.New(fault.InvalidInvite)
	}
	if now >= proof.ExpiresAt {
		return claimProof{}, fault.New(fault.Expired)
	}
	return proof, nil
}

func validateClaim(link Link, descriptor Descriptor, claim Claim, pin string, inviter identity.SessionCertificate, epoch uint64, now int64) (identity.SessionCertificate, error) {
	if claim.Version != 1 {
		return identity.SessionCertificate{}, fault.New(fault.UnsupportedVersion)
	}
	if claim.InviteID != link.id || descriptor.InviteID != link.id || descriptor.RelayOrigin != link.origin || !encodedBytes(claim.ClaimID, 16) || len(claim.RoutingKey) != 32 || len(claim.MailboxKey) != 32 || len(claim.ReturnGrant) == 0 || len(claim.ReturnGrant) > 16<<10 {
		return identity.SessionCertificate{}, fault.New(fault.InvalidInvite)
	}
	if !identity.EqualBinding(claim.Audience, inviter.Binding) {
		return identity.SessionCertificate{}, fault.New(fault.AudienceMismatch)
	}
	if claim.NotBefore < descriptor.NotBefore || claim.ExpiresAt > descriptor.ExpiresAt || claim.ExpiresAt <= claim.NotBefore {
		return identity.SessionCertificate{}, fault.New(fault.InvalidInvite)
	}
	if now < claim.NotBefore {
		return identity.SessionCertificate{}, fault.New(fault.NotYetValid)
	}
	if now >= claim.ExpiresAt {
		return identity.SessionCertificate{}, fault.New(fault.Expired)
	}
	if err := grant.CheckScope(claim.RequestedScope, string(claim.RequestedScope)); err != nil {
		return identity.SessionCertificate{}, err
	}
	if err := grant.CheckScope(descriptor.ForwardScope, string(claim.RequestedScope)); err != nil {
		return identity.SessionCertificate{}, err
	}
	claimant, err := identity.VerifyChain(claim.Claimant, pin, now)
	if err != nil {
		return identity.SessionCertificate{}, err
	}
	returned, err := grant.Verify(claim.ReturnGrant, claimant, inviter, epoch, now)
	if err != nil {
		return identity.SessionCertificate{}, err
	}
	if err := grant.CheckScope(descriptor.ReturnScope, string(returned.Scope)); err != nil {
		return identity.SessionCertificate{}, err
	}
	return claimant, nil
}
