package invite

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"

	"github.com/temirlankasen/peerturn/internal/codec"
	ptcrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/identity"
)

// MaxLifetime is the maximum invitation validity in seconds.
const MaxLifetime int64 = 600

// Descriptor is the inviter-signed policy inside the invitation HPKE ciphertext.
type Descriptor struct {
	// Version fixes the signed application profile and prevents negotiation.
	Version int `json:"wire_version"`
	// InviteID binds this policy to one random public relay slot.
	InviteID string `json:"invite_id"`
	// Inviter authenticates the exact recipient session through pinned certificates.
	Inviter identity.Chain `json:"inviter"`
	// ForwardScope is the inviter's ceiling on the joining session's requests.
	ForwardScope grant.Scope `json:"forward_scope"`
	// ReturnScope proposes the independent grant issued by the joining recipient.
	ReturnScope grant.Scope `json:"return_scope"`
	// RelayOrigin prevents moving an otherwise valid policy to another relay.
	RelayOrigin string `json:"relay_origin"`
	// NotBefore prevents early use of a future invitation.
	NotBefore int64 `json:"not_before"`
	// ExpiresAt bounds capability theft to at most ten minutes after NotBefore.
	ExpiresAt int64 `json:"expires_at"`
	// MaxUses is fixed to one; persistent enforcement belongs to the caller.
	MaxUses int `json:"max_uses"`
	// PublisherManifestHash must match a separately trusted distribution manifest.
	PublisherManifestHash string `json:"publisher_manifest_hash"`
	// EnrollmentVoucher carries the inviter's one-mailbox enrollment authorization.
	EnrollmentVoucher string `json:"enrollment_voucher"`
	// RoutingKey pins the inviter's relay writer independently of session authority.
	RoutingKey []byte `json:"routing_key,omitempty"`
	// MailboxKey pins the inviter's relay reader; both routing keys appear together.
	MailboxKey []byte `json:"mailbox_key,omitempty"`
}

// Create generates independent invitation signing and X25519 keys and encrypts policy.
// InviteID is generated here; caller-supplied IDs are rejected to prevent slot reuse.
func Create(descriptor Descriptor, signingKey ed25519.PrivateKey, now int64) (Link, Packet, error) {
	if descriptor.InviteID != "" {
		return Link{}, Packet{}, fault.New(fault.InvalidInvite)
	}
	pin := identity.RootFingerprint(descriptor.Inviter.RootPublicKey)
	link, err := newLink(descriptor.RelayOrigin, pin)
	if err != nil {
		return Link{}, Packet{}, err
	}
	descriptor.InviteID = link.id
	cert, err := validateDescriptor(descriptor, pin, now)
	if err != nil {
		return Link{}, Packet{}, err
	}
	if len(signingKey) != ed25519.PrivateKeySize || !bytes.Equal(signingKey[ed25519.SeedSize:], cert.SigningKey) {
		return Link{}, Packet{}, fault.New(fault.InvalidSignature)
	}
	payload, err := codec.Canonical(descriptor)
	if err != nil {
		return Link{}, Packet{}, err
	}
	signed, err := ptcrypto.Sign(payload, identity.RootFingerprint(cert.SigningKey), ptcrypto.InviteType, signingKey)
	if err != nil {
		return Link{}, Packet{}, err
	}
	content, err := codec.Canonical(signedContent{descriptor.Inviter, signed})
	if err != nil {
		return Link{}, Packet{}, err
	}
	key, err := ecdh.X25519().NewPrivateKey(link.secrets.encryptionKey[:])
	if err != nil {
		return Link{}, Packet{}, fault.New(fault.InvalidInvite)
	}
	aad, err := packetAAD(link.id, "descriptor")
	if err != nil {
		return Link{}, Packet{}, err
	}
	enc, ciphertext, err := ptcrypto.SealFor(ptcrypto.DescriptorContext, key.PublicKey(), aad, content)
	if err != nil {
		return Link{}, Packet{}, err
	}
	packet := Packet{Version: 1, InviteID: link.id, Enc: enc, Ciphertext: ciphertext}
	link.descriptorHash, err = PacketDigest(packet)
	if err != nil {
		return Link{}, Packet{}, err
	}
	return link, packet, nil
}

// OpenDescriptor checks URL pins, encrypted packet hash, chain, signature and policy.
// expectedManifest must be a publisher manifest hash from a separately trusted source.
func OpenDescriptor(link Link, packet Packet, expectedManifest string, now int64) (Descriptor, identity.SessionCertificate, error) {
	digest, err := PacketDigest(packet)
	if err != nil {
		return Descriptor{}, identity.SessionCertificate{}, err
	}
	if digest != link.descriptorHash || packet.InviteID != link.id {
		return Descriptor{}, identity.SessionCertificate{}, fault.New(fault.InvalidInvite)
	}
	if link.secrets == nil {
		return Descriptor{}, identity.SessionCertificate{}, fault.New(fault.InvalidInvite)
	}
	key, err := ecdh.X25519().NewPrivateKey(link.secrets.encryptionKey[:])
	if err != nil {
		return Descriptor{}, identity.SessionCertificate{}, fault.New(fault.InvalidInvite)
	}
	aad, err := packetAAD(link.id, "descriptor")
	if err != nil {
		return Descriptor{}, identity.SessionCertificate{}, err
	}
	plaintext, err := ptcrypto.OpenFor(ptcrypto.DescriptorContext, key, packet.Enc, aad, packet.Ciphertext)
	if err != nil {
		return Descriptor{}, identity.SessionCertificate{}, err
	}
	verified, chain, _, err := verifySignedContent(plaintext, link.fingerprint, ptcrypto.InviteType, now)
	if err != nil {
		return Descriptor{}, identity.SessionCertificate{}, err
	}
	var descriptor Descriptor
	if decodeErr := codec.Decode(verified, MaxPacketBytes/2, &descriptor); decodeErr != nil {
		return Descriptor{}, identity.SessionCertificate{}, decodeErr
	}
	if !equalChain(chain, descriptor.Inviter) {
		return Descriptor{}, identity.SessionCertificate{}, fault.New(fault.InvalidCertificate)
	}
	if descriptor.InviteID != link.id || descriptor.RelayOrigin != link.origin {
		return Descriptor{}, identity.SessionCertificate{}, fault.New(fault.AudienceMismatch)
	}
	if !validHash(expectedManifest) || descriptor.PublisherManifestHash != expectedManifest {
		return Descriptor{}, identity.SessionCertificate{}, fault.New(fault.NeedsVerify)
	}
	cert, err := validateDescriptor(descriptor, link.fingerprint, now)
	if err != nil {
		return Descriptor{}, identity.SessionCertificate{}, err
	}
	return descriptor, cert, nil
}

func validateDescriptor(d Descriptor, pin string, now int64) (identity.SessionCertificate, error) {
	if d.Version != 1 {
		return identity.SessionCertificate{}, fault.New(fault.UnsupportedVersion)
	}
	if !encodedBytes(d.InviteID, 16) || !validOrigin(d.RelayOrigin) || d.MaxUses != 1 || !validHash(d.PublisherManifestHash) || len(d.EnrollmentVoucher) == 0 || len(d.EnrollmentVoucher) > 2048 {
		return identity.SessionCertificate{}, fault.New(fault.InvalidInvite)
	}
	if d.NotBefore < 0 || d.ExpiresAt <= d.NotBefore || d.ExpiresAt-d.NotBefore > MaxLifetime {
		return identity.SessionCertificate{}, fault.New(fault.InvalidInvite)
	}
	if (len(d.RoutingKey) != 0 || len(d.MailboxKey) != 0) && (!ptcrypto.ValidSigningKey(d.RoutingKey) || !ptcrypto.ValidSigningKey(d.MailboxKey)) {
		return identity.SessionCertificate{}, fault.New(fault.InvalidInvite)
	}
	if now < d.NotBefore {
		return identity.SessionCertificate{}, fault.New(fault.NotYetValid)
	}
	if now >= d.ExpiresAt {
		return identity.SessionCertificate{}, fault.New(fault.Expired)
	}
	for _, scope := range []grant.Scope{d.ForwardScope, d.ReturnScope} {
		if err := grant.CheckScope(scope, string(scope)); err != nil {
			return identity.SessionCertificate{}, err
		}
	}
	cert, err := identity.VerifyChain(d.Inviter, pin, now)
	if err != nil {
		return identity.SessionCertificate{}, err
	}
	if d.NotBefore < cert.NotBefore || d.ExpiresAt > cert.ExpiresAt {
		return identity.SessionCertificate{}, fault.New(fault.InvalidInvite)
	}
	return cert, nil
}
