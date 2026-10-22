package identity

import (
	"bytes"
	"crypto/ed25519"

	"github.com/temirlankasen/peerturn/internal/codec"
	peercrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/fault"
)

// RotationStatement identifies the old and new roots in one validity window.
type RotationStatement struct {
	// Version prevents accepting an incompatible continuity proof profile.
	Version int `json:"wire_version"`
	// OldIdentityID anchors continuity to the previously verified root pin.
	OldIdentityID string `json:"old_identity_id"`
	// NewIdentityID binds the proposed pin to the exact replacement public key.
	NewIdentityID string `json:"new_identity_id"`
	// NewRootPublicKey lets both signatures commit to the same replacement key.
	NewRootPublicKey []byte `json:"new_root_public_key"`
	// NotBefore is the inclusive Unix-second start of the continuity proof.
	NotBefore int64 `json:"not_before"`
	// ExpiresAt is the exclusive Unix-second end, bounding replayed proofs.
	ExpiresAt int64 `json:"expires_at"`
}

// Rotation proves both roots signed precisely the same continuity statement.
type Rotation struct {
	// OldRootPublicKey supplies verification material that must match the old pin.
	OldRootPublicKey []byte `json:"old_root_public_key"`
	// OldSignature proves the trusted root authorized this exact transition.
	OldSignature string `json:"old_signature"`
	// NewSignature proves possession of the replacement key over the same bytes.
	NewSignature string `json:"new_signature"`
}

// IssueRotation cross-signs an explicit change with both old and new roots.
func IssueRotation(statement RotationStatement, oldKey, newKey ed25519.PrivateKey) (Rotation, error) {
	if err := validateRotation(statement); err != nil {
		return Rotation{}, err
	}
	if len(oldKey) != ed25519.PrivateKeySize || len(newKey) != ed25519.PrivateKeySize {
		return Rotation{}, fault.New(fault.InvalidCertificate)
	}
	oldPublic := ed25519.PublicKey(oldKey[ed25519.SeedSize:])
	newPublic := ed25519.PublicKey(newKey[ed25519.SeedSize:])
	if !peercrypto.ValidSigningKey(oldPublic) || RootFingerprint(oldPublic) != statement.OldIdentityID ||
		!bytes.Equal(newPublic, statement.NewRootPublicKey) {
		return Rotation{}, fault.New(fault.InvalidCertificate)
	}
	payload, err := codec.Canonical(statement)
	if err != nil {
		return Rotation{}, err
	}
	oldSignature, err := peercrypto.Sign(payload, statement.OldIdentityID, peercrypto.RotationType, oldKey)
	if err != nil {
		return Rotation{}, err
	}
	newSignature, err := peercrypto.Sign(payload, statement.NewIdentityID, peercrypto.RotationType, newKey)
	if err != nil {
		return Rotation{}, err
	}
	return Rotation{OldRootPublicKey: oldPublic, OldSignature: oldSignature, NewSignature: newSignature}, nil
}

// DecodeRotation parses a bounded continuity statement without changing a pin.
func DecodeRotation(payload []byte) (RotationStatement, error) {
	var statement RotationStatement
	if err := codec.Decode(payload, maxCertificateBytes, &statement); err != nil {
		return RotationStatement{}, err
	}
	if err := validateRotation(statement); err != nil {
		return RotationStatement{}, err
	}
	return statement, nil
}

// VerifyRotation verifies continuity against the old pin and returns evidence only.
// Updating the pin remains an explicit owner action outside this pure verifier.
func VerifyRotation(rotation Rotation, oldPin string, now int64) (RotationStatement, error) {
	if !peercrypto.ValidSigningKey(rotation.OldRootPublicKey) {
		return RotationStatement{}, fault.New(fault.InvalidCertificate)
	}
	if !validFingerprint(oldPin) ||
		RootFingerprint(rotation.OldRootPublicKey) != oldPin {
		return RotationStatement{}, fault.New(fault.NeedsVerify)
	}
	oldPayload, err := peercrypto.Verify(rotation.OldSignature, oldPin, peercrypto.RotationType, rotation.OldRootPublicKey)
	if err != nil {
		return RotationStatement{}, err
	}
	statement, err := DecodeRotation(oldPayload)
	if err != nil {
		return RotationStatement{}, err
	}
	if statement.OldIdentityID != oldPin {
		return RotationStatement{}, fault.New(fault.NeedsVerify)
	}
	newPayload, err := peercrypto.Verify(rotation.NewSignature, statement.NewIdentityID,
		peercrypto.RotationType, statement.NewRootPublicKey)
	if err != nil {
		return RotationStatement{}, err
	}
	if !bytes.Equal(oldPayload, newPayload) {
		return RotationStatement{}, fault.New(fault.InvalidCertificate)
	}
	if err := checkTime(statement.NotBefore, statement.ExpiresAt, now); err != nil {
		return RotationStatement{}, err
	}
	return statement, nil
}

func validateRotation(s RotationStatement) error {
	if s.Version != 1 {
		return fault.New(fault.UnsupportedVersion)
	}
	if !validFingerprint(s.OldIdentityID) || !peercrypto.ValidSigningKey(s.NewRootPublicKey) ||
		RootFingerprint(s.NewRootPublicKey) != s.NewIdentityID || s.OldIdentityID == s.NewIdentityID ||
		!validLifetime(s.NotBefore, s.ExpiresAt, RotationMaxLifetime) {
		return fault.New(fault.InvalidCertificate)
	}
	return nil
}
