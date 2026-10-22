package identity

import (
	"crypto/ed25519"

	"github.com/temirlankasen/peerturn/internal/codec"
	peercrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/fault"
)

const maxCertificateBytes = 4096

// DecodeDevice parses a bounded device certificate payload with strict fields.
func DecodeDevice(payload []byte) (DeviceCertificate, error) {
	var cert DeviceCertificate
	if err := codec.Decode(payload, maxCertificateBytes, &cert); err != nil {
		return DeviceCertificate{}, err
	}
	if err := validateDevice(cert); err != nil {
		return DeviceCertificate{}, err
	}
	return cert, nil
}

// DecodeSession parses a bounded session certificate payload with strict fields.
func DecodeSession(payload []byte) (SessionCertificate, error) {
	var cert SessionCertificate
	if err := codec.Decode(payload, maxCertificateBytes, &cert); err != nil {
		return SessionCertificate{}, err
	}
	if err := validateSession(cert); err != nil {
		return SessionCertificate{}, err
	}
	return cert, nil
}

// IssueDevice signs a certificate only under the root matching its identity.
func IssueDevice(cert DeviceCertificate, key ed25519.PrivateKey) (string, error) {
	if err := validateDevice(cert); err != nil {
		return "", err
	}
	if len(key) != ed25519.PrivateKeySize ||
		RootFingerprint(ed25519.PublicKey(key[ed25519.SeedSize:])) != cert.IdentityID {
		return "", fault.New(fault.InvalidCertificate)
	}
	payload, err := codec.Canonical(cert)
	if err != nil {
		return "", err
	}
	return peercrypto.Sign(payload, cert.IdentityID, peercrypto.DeviceType, key)
}

// IssueSession signs a validated session certificate with its device key.
func IssueSession(cert SessionCertificate, key ed25519.PrivateKey) (string, error) {
	if err := validateSession(cert); err != nil {
		return "", err
	}
	if len(key) != ed25519.PrivateKeySize {
		return "", fault.New(fault.InvalidCertificate)
	}
	payload, err := codec.Canonical(cert)
	if err != nil {
		return "", err
	}
	kid := RootFingerprint(ed25519.PublicKey(key[ed25519.SeedSize:]))
	return peercrypto.Sign(payload, kid, peercrypto.SessionType, key)
}

// VerifyChain authenticates a chain against an existing root pin without TOFU.
func VerifyChain(chain Chain, pinnedIdentity string, now int64) (SessionCertificate, error) {
	if !peercrypto.ValidSigningKey(chain.RootPublicKey) {
		return SessionCertificate{}, fault.New(fault.InvalidCertificate)
	}
	if !validFingerprint(pinnedIdentity) || RootFingerprint(chain.RootPublicKey) != pinnedIdentity {
		return SessionCertificate{}, fault.New(fault.NeedsVerify)
	}
	devicePayload, err := peercrypto.Verify(chain.Device, pinnedIdentity, peercrypto.DeviceType, chain.RootPublicKey)
	if err != nil {
		return SessionCertificate{}, err
	}
	d, err := DecodeDevice(devicePayload)
	if err != nil {
		return SessionCertificate{}, err
	}
	if d.IdentityID != pinnedIdentity {
		return SessionCertificate{}, fault.New(fault.InvalidCertificate)
	}
	if timeErr := checkTime(d.NotBefore, d.ExpiresAt, now); timeErr != nil {
		return SessionCertificate{}, timeErr
	}
	kid := RootFingerprint(d.SigningKey)
	sessionPayload, err := peercrypto.Verify(chain.Session, kid, peercrypto.SessionType, d.SigningKey)
	if err != nil {
		return SessionCertificate{}, err
	}
	s, err := DecodeSession(sessionPayload)
	if err != nil {
		return SessionCertificate{}, err
	}
	if s.IdentityID != d.IdentityID || s.DeviceID != d.DeviceID || s.DeviceEpoch != d.DeviceEpoch ||
		s.NotBefore < d.NotBefore || s.ExpiresAt > d.ExpiresAt {
		return SessionCertificate{}, fault.New(fault.InvalidCertificate)
	}
	if err := checkTime(s.NotBefore, s.ExpiresAt, now); err != nil {
		return SessionCertificate{}, err
	}
	return s, nil
}
