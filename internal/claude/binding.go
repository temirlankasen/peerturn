// Package claude implements the pinned Claude Code local adapter contract.
package claude

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"path/filepath"
	"regexp"
	"time"
)

var nativeID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ErrNeedsRebind closes native admission when continuity cannot be established.
var ErrNeedsRebind = errors.New("needs_rebind")

// Binding uses a per-instance challenge, never cwd, names or peer supplied text.
// Callers serialize access and persist NativeSessionID separately from the lease.
type Binding struct {
	NativeSessionID, Socket, Instance, RuntimeVersion, Provider string
	WorkingDirectory                                            string
	ParentConfiguration                                         *ResponderConfiguration
	ConfirmedNonce                                              string
	confirmed                                                   bool
	lastSeen                                                    time.Time
	nonce                                                       string
	nonceExpiry                                                 time.Time
}

// NewBinding gates the private mod and socket protocol to the qualified version.
func NewBinding(session, socket, version, provider string, now time.Time) (*Binding, error) {
	if version != "2.1.306" {
		return nil, errors.New("unsupported_version")
	}
	if !nativeID.MatchString(session) || !filepath.IsAbs(socket) || filepath.Clean(socket) != socket || len(socket) > 240 || (provider != "gateway" && provider != "anthropic") {
		return nil, ErrNeedsRebind
	}
	return &Binding{NativeSessionID: session, Socket: socket, Instance: nonce128(), RuntimeVersion: version, Provider: provider, lastSeen: now}, nil
}

// Challenge requires the Bash child's exact native socket and a live helper.
func (b *Binding) Challenge(socket string, now time.Time) (string, error) {
	if socket != b.Socket || now.Sub(b.lastSeen) > 20*time.Second {
		return "", ErrNeedsRebind
	}
	b.nonce, b.nonceExpiry = nonce128()+nonce128(), now.Add(10*time.Second)
	return b.nonce, nil
}

// Confirm consumes a nonce only after the mod reports its host supplied UUID.
func (b *Binding) Confirm(nonce, session, instance string, now time.Time) error {
	if b.nonce == "" || subtle.ConstantTimeCompare([]byte(nonce), []byte(b.nonce)) != 1 || session != b.NativeSessionID || instance != b.Instance || !now.Before(b.nonceExpiry) {
		return ErrNeedsRebind
	}
	b.ConfirmedNonce, b.nonce = b.nonce, ""
	b.confirmed, b.lastSeen = true, now
	return nil
}

// Touch renews only the active helper instance, never an old process after reload.
func (b *Binding) Touch(instance string, now time.Time) error {
	if instance != b.Instance {
		return ErrNeedsRebind
	}
	if now.Sub(b.lastSeen) > 20*time.Second {
		b.confirmed = false
		return ErrNeedsRebind
	}
	b.lastSeen = now
	return nil
}

// Ready is a short lease; daemon/helper death does not leave advertised ingress.
func (b *Binding) Ready(now time.Time) bool {
	return b != nil && b.confirmed && now.Sub(b.lastSeen) <= 20*time.Second
}

// MCPEpoch proves a resolved incarnation without exposing the helper bearer.
func (b *Binding) MCPEpoch() string {
	mac := hmac.New(sha256.New, []byte(b.Instance))
	mac.Write([]byte("peerturn/mcp-epoch/v1"))
	return hex.EncodeToString(mac.Sum(nil)[:16])
}

func nonce128() string { var b [16]byte; rand.Read(b[:]); return hex.EncodeToString(b[:]) }
