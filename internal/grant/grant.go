package grant

import (
	"bytes"
	"crypto/ed25519"

	"github.com/temirlankasen/peerturn/internal/codec"
	peercrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/identity"
)

// Decode parses a bounded signed-grant payload without granting authority.
func Decode(payload []byte) (Grant, error) {
	var g Grant
	if err := codec.Decode(payload, maxGrantBytes, &g); err != nil {
		return Grant{}, err
	}
	if err := validate(g); err != nil {
		return Grant{}, err
	}
	return g, nil
}

// Issue signs a grant only with the recipient issuer's exact session key.
func Issue(g Grant, key ed25519.PrivateKey) (string, error) {
	if err := validate(g); err != nil {
		return "", err
	}
	if len(key) != ed25519.PrivateKeySize || !bytes.Equal(key[ed25519.SeedSize:], g.Issuer.SigningKey) {
		return "", fault.New(fault.InvalidGrant)
	}
	payload, err := codec.Canonical(g)
	if err != nil {
		return "", err
	}
	return peercrypto.Sign(payload, identity.RootFingerprint(g.Issuer.SigningKey), peercrypto.GrantType, key)
}

// Verify authenticates a directional grant using already authenticated session certificates.
// The caller supplies its authoritative current epoch; sender text cannot update it.
func Verify(compact string, issuer, subject identity.SessionCertificate, currentEpoch uint64, now int64) (Grant, error) {
	if err := identity.ValidateCertificate(issuer, now); err != nil {
		return Grant{}, err
	}
	if err := identity.ValidateCertificate(subject, now); err != nil {
		return Grant{}, err
	}
	payload, err := peercrypto.Verify(compact, identity.RootFingerprint(issuer.SigningKey),
		peercrypto.GrantType, issuer.SigningKey)
	if err != nil {
		return Grant{}, err
	}
	g, err := Decode(payload)
	if err != nil {
		return Grant{}, err
	}
	if !identity.EqualBinding(g.Issuer, issuer.Binding) || !identity.EqualBinding(g.Subject, subject.Binding) {
		return Grant{}, fault.New(fault.AudienceMismatch)
	}
	if err := CompareEpoch(g.GrantEpoch, currentEpoch); err != nil {
		return Grant{}, err
	}
	if now < g.NotBefore {
		return Grant{}, fault.New(fault.NotYetValid)
	}
	if now >= g.ExpiresAt {
		return Grant{}, fault.New(fault.Expired)
	}
	if g.NotBefore < issuer.NotBefore || g.NotBefore < subject.NotBefore ||
		g.ExpiresAt > issuer.ExpiresAt || g.ExpiresAt > subject.ExpiresAt {
		return Grant{}, fault.New(fault.InvalidGrant)
	}
	return g, nil
}

// Hash binds envelopes to the exact signed compact grant, not a reserialization.
func Hash(compact string) string { return peercrypto.Digest([]byte(compact)) }
