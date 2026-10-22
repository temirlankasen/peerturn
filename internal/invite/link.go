// Package invite implements pure invitation verification and one-use state transitions.
package invite

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"strings"

	"github.com/temirlankasen/peerturn/internal/fault"
)

// MaxLinkBytes bounds pasted links before parsing.
const MaxLinkBytes = 2048

// Link keeps invitation secrets private; SecretURL is the explicit export operation.
type Link struct {
	origin, id, fingerprint, descriptorHash string
	secrets                                 *linkSecrets
}

// ParseLink accepts only the version-one fragment capability format.
func ParseLink(raw string) (Link, error) {
	if len(raw) > MaxLinkBytes {
		return Link{}, fault.New(fault.TooLarge)
	}
	u, err := url.Parse(raw)
	if err != nil || u == nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.RawPath != "" {
		return Link{}, fault.New(fault.InvalidInvite)
	}
	id, ok := strings.CutPrefix(u.Path, "/i/")
	if !ok || !encodedBytes(id, 16) || u.RawFragment != "" || strings.ContainsAny(u.Fragment, "%+;") {
		return Link{}, fault.New(fault.InvalidInvite)
	}
	origin := "https://" + u.Host
	if !validOrigin(origin) {
		return Link{}, fault.New(fault.InvalidInvite)
	}
	values, err := url.ParseQuery(u.Fragment)
	if err != nil || len(values) != 5 {
		return Link{}, fault.New(fault.InvalidInvite)
	}
	for _, name := range []string{"v", "ik", "ek", "fp", "mh"} {
		if len(values[name]) != 1 {
			return Link{}, fault.New(fault.InvalidInvite)
		}
	}
	if values.Get("v") != "1" {
		return Link{}, fault.New(fault.UnsupportedVersion)
	}
	if !encodedBytes(values.Get("ik"), 32) || !encodedBytes(values.Get("ek"), 32) || !validHash(values.Get("fp")) || !validHash(values.Get("mh")) {
		return Link{}, fault.New(fault.InvalidInvite)
	}
	seed, err := base64.RawURLEncoding.DecodeString(values.Get("ik"))
	if err != nil {
		return Link{}, fault.New(fault.InvalidInvite)
	}
	key, err := base64.RawURLEncoding.DecodeString(values.Get("ek"))
	if err != nil {
		return Link{}, fault.New(fault.InvalidInvite)
	}
	link := Link{origin: origin, id: id, fingerprint: values.Get("fp"), descriptorHash: values.Get("mh"), secrets: &linkSecrets{signingSeed: &[32]byte{}, encryptionKey: &[32]byte{}}}
	copy(link.secrets.signingSeed[:], seed)
	copy(link.secrets.encryptionKey[:], key)
	return link, nil
}

// FetchURL strips every fragment secret without doing network I/O.
func (l Link) FetchURL() string { return l.origin + "/i/" + l.id }

// SecretURL explicitly exports the complete capability for trusted transfer.
func (l Link) SecretURL() string {
	if l.secrets == nil {
		return ""
	}
	return l.FetchURL() + "#v=1&ik=" + base64.RawURLEncoding.EncodeToString(l.secrets.signingSeed[:]) +
		"&ek=" + base64.RawURLEncoding.EncodeToString(l.secrets.encryptionKey[:]) + "&fp=" + l.fingerprint + "&mh=" + l.descriptorHash
}

// String redacts secrets during normal formatting and logging.
func (l Link) String() string { return l.FetchURL() + "#[redacted]" }

// GoString redacts secrets during detailed Go formatting.
func (l Link) GoString() string { return l.String() }

// ID returns the public random routing identifier.
func (l Link) ID() string { return l.id }

// Fingerprint returns the inviter identity pin carried by the trusted link.
func (l Link) Fingerprint() string { return l.fingerprint }

func newLink(origin, fingerprint string) (Link, error) {
	if !validOrigin(origin) || !validHash(fingerprint) {
		return Link{}, fault.New(fault.InvalidInvite)
	}
	var id [16]byte
	var seed [ed25519.SeedSize]byte
	if _, err := rand.Read(id[:]); err != nil {
		return Link{}, fault.New(fault.InvalidInvite)
	}
	if _, err := rand.Read(seed[:]); err != nil {
		return Link{}, fault.New(fault.InvalidInvite)
	}
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return Link{}, fault.New(fault.InvalidInvite)
	}
	link := Link{origin: origin, id: base64.RawURLEncoding.EncodeToString(id[:]), fingerprint: fingerprint, secrets: &linkSecrets{signingSeed: &seed, encryptionKey: &[32]byte{}}}
	copy(link.secrets.encryptionKey[:], key.Bytes())
	return link, nil
}

func validOrigin(raw string) bool {
	if len(raw) > 256 || strings.ContainsAny(raw, "\\%\t\r\n ") {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && u.String() == raw && u.Scheme == "https" && u.Host != "" && u.Hostname() != "" && !strings.HasSuffix(u.Host, ":") && u.Host == strings.ToLower(u.Host) &&
		u.User == nil && u.Opaque == "" && u.Path == "" && u.RawQuery == "" && !u.ForceQuery && u.Fragment == ""
}

func validHash(raw string) bool {
	decoded, err := hex.DecodeString(raw)
	return err == nil && len(decoded) == 32 && raw == strings.ToLower(raw)
}

func encodedBytes(raw string, size int) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	return err == nil && len(decoded) == size && base64.RawURLEncoding.EncodeToString(decoded) == raw
}
