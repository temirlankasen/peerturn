// Package identity verifies exact root, device, and session key bindings.
package identity

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"encoding/hex"

	peercrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/fault"
)

const maxSafeInteger uint64 = 1<<53 - 1

// Certificate lifetime caps are Unix seconds, including any renewal overlap.
const (
	// DeviceMaxLifetime limits a root's offline delegation to 30 days.
	DeviceMaxLifetime int64 = 30 * 24 * 60 * 60
	// SessionMaxLifetime permits daily key rotation plus five minutes of overlap.
	// It does not extend expired authority or the separate backlog key retention.
	SessionMaxLifetime int64 = 24*60*60 + 5*60
	// RotationMaxLifetime bounds the replay window of a root continuity proof.
	RotationMaxLifetime int64 = 3 * 24 * 60 * 60
)

// Binding identifies one exact signing session and its continuity epochs.
type Binding struct {
	// IdentityID binds authority to a root fingerprint instead of a display name.
	IdentityID string `json:"identity_id"`
	// DeviceID prevents another device under the same root from inheriting access.
	DeviceID string `json:"device_id"`
	// DeviceEpoch separates device incarnations when keys or trust change.
	DeviceEpoch uint64 `json:"device_epoch"`
	// SessionID selects one session without automatically attaching its neighbors.
	SessionID string `json:"session_id"`
	// SessionEpoch prevents lost continuity from reviving an earlier session grant.
	SessionEpoch uint64 `json:"session_epoch"`
	// KeyEpoch binds messages and grants to one session key generation.
	KeyEpoch uint64 `json:"key_epoch"`
	// SigningKey pins the exact sender key rather than trusting identifiers alone.
	SigningKey []byte `json:"signing_key"`
}

// DeviceCertificate is a root-authorized device signing key.
type DeviceCertificate struct {
	// Version prevents silently interpreting a different certificate profile.
	Version int `json:"wire_version"`
	// IdentityID requires the signed certificate to name its pinned issuing root.
	IdentityID string `json:"identity_id"`
	// DeviceID limits root-delegated signing authority to one device.
	DeviceID string `json:"device_id"`
	// DeviceEpoch binds the delegation to one device incarnation.
	DeviceEpoch uint64 `json:"device_epoch"`
	// SigningKey delegates session certification without exposing the root key.
	SigningKey []byte `json:"signing_key"`
	// NotBefore is the inclusive Unix-second start of the root's delegation.
	NotBefore int64 `json:"not_before"`
	// ExpiresAt is the exclusive Unix-second end, bounding stale delegation.
	ExpiresAt int64 `json:"expires_at"`
}

// SessionCertificate carries device-authorized signing and encryption keys.
type SessionCertificate struct {
	// Binding preserves the full device and session authority boundary.
	Binding
	// Version prevents silently interpreting a different session profile.
	Version int `json:"wire_version"`
	// EncryptionKey identifies the X25519 recipient independently of signing keys.
	EncryptionKey []byte `json:"encryption_key"`
	// NotBefore is the inclusive Unix-second start within the device's delegation.
	NotBefore int64 `json:"not_before"`
	// ExpiresAt is the exclusive Unix-second end, limiting the key's authority.
	ExpiresAt int64 `json:"expires_at"`
}

// Chain carries signed certificates without exposing private key material.
type Chain struct {
	// RootPublicKey must match an existing pin; carrying it does not establish trust.
	RootPublicKey []byte `json:"root_public_key"`
	// Device carries the root-signed compact JWS establishing device delegation.
	Device string `json:"device"`
	// Session carries the device-signed compact JWS establishing session keys.
	Session string `json:"session"`
}

// RootFingerprint is the SHA-256 identity of an Ed25519 public key.
func RootFingerprint(key ed25519.PublicKey) string { return peercrypto.Digest(key) }

// EqualBinding compares every authority-bearing field, including the signing key.
func EqualBinding(a, b Binding) bool {
	return a.IdentityID == b.IdentityID && a.DeviceID == b.DeviceID &&
		a.DeviceEpoch == b.DeviceEpoch && a.SessionID == b.SessionID &&
		a.SessionEpoch == b.SessionEpoch && a.KeyEpoch == b.KeyEpoch &&
		bytes.Equal(a.SigningKey, b.SigningKey)
}

// ValidateBinding rejects wildcard, incomplete, or non-interoperable bindings.
func ValidateBinding(b Binding) error {
	if !validFingerprint(b.IdentityID) || !validID(b.DeviceID) || !validID(b.SessionID) ||
		!validEpoch(b.DeviceEpoch) || !validEpoch(b.SessionEpoch) || !validEpoch(b.KeyEpoch) ||
		!peercrypto.ValidSigningKey(b.SigningKey) {
		return fault.New(fault.InvalidCertificate)
	}
	return nil
}

// ValidateCertificate checks a session certificate's shape and validity window.
// Its signature and chain must separately be verified by VerifyChain.
func ValidateCertificate(s SessionCertificate, now int64) error {
	if err := validateSession(s); err != nil {
		return err
	}
	return checkTime(s.NotBefore, s.ExpiresAt, now)
}

func validateSession(s SessionCertificate) error {
	if s.Version != 1 {
		return fault.New(fault.UnsupportedVersion)
	}
	if err := ValidateBinding(s.Binding); err != nil {
		return err
	}
	if !validLifetime(s.NotBefore, s.ExpiresAt, SessionMaxLifetime) || !validEncryptionKey(s.EncryptionKey) {
		return fault.New(fault.InvalidCertificate)
	}
	return nil
}

func validateDevice(d DeviceCertificate) error {
	if d.Version != 1 {
		return fault.New(fault.UnsupportedVersion)
	}
	if !validFingerprint(d.IdentityID) || !validID(d.DeviceID) || !validEpoch(d.DeviceEpoch) ||
		!peercrypto.ValidSigningKey(d.SigningKey) || !validLifetime(d.NotBefore, d.ExpiresAt, DeviceMaxLifetime) {
		return fault.New(fault.InvalidCertificate)
	}
	return nil
}

func validFingerprint(s string) bool {
	if len(s) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(s)
	return err == nil && hex.EncodeToString(decoded) == s
}

func validID(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, c := range []byte(s) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '-' || c == '_' || c == '.' {
			continue
		}
		return false
	}
	return true
}

func validEpoch(e uint64) bool { return e > 0 && e <= maxSafeInteger }

func validWindow(start, end int64) bool {
	return start >= 0 && end > start && end <= int64(maxSafeInteger)
}

func validLifetime(start, end, maximum int64) bool {
	return validWindow(start, end) && end-start <= maximum
}

func checkTime(start, end, now int64) error {
	if now < start {
		return fault.New(fault.NotYetValid)
	}
	if now >= end {
		return fault.New(fault.Expired)
	}
	return nil
}

func validEncryptionKey(raw []byte) bool {
	pub, err := ecdh.X25519().NewPublicKey(raw)
	if err != nil {
		return false
	}
	// An exchange with a fixed non-secret probe scalar rejects low-order points.
	probe, err := ecdh.X25519().NewPrivateKey(make([]byte, 32))
	if err != nil {
		return false
	}
	_, err = probe.ECDH(pub)
	return err == nil
}
