// Package claude implements the Claude Code local adapter contract.
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

	"github.com/temirlankasen/peerturn/internal/runtimepolicy"
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
	ContractFailures                                            map[string]*runtimepolicy.Error
	ConfirmedNonce                                              string
	confirmed                                                   bool
	lastSeen                                                    time.Time
	nonce                                                       string
	nonceExpiry                                                 time.Time
}

// NewBinding admits supported runtime versions without supplying native authority.
func NewBinding(session, socket, version, provider string, now time.Time) (*Binding, error) {
	if err := runtimepolicy.Check(runtimepolicy.Claude, version); err != nil {
		return nil, err
	}
	if !nativeID.MatchString(session) || !filepath.IsAbs(socket) || filepath.Clean(socket) != socket || len(socket) > 240 || (provider != "gateway" && provider != "anthropic") {
		return nil, ErrNeedsRebind
	}
	decision := runtimepolicy.Evaluate(runtimepolicy.Claude, version)
	return &Binding{NativeSessionID: session, Socket: socket, Instance: nonce128(), RuntimeVersion: decision.Version, Provider: provider, lastSeen: now}, nil
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

// MCPEpoch identifies this helper incarnation without exposing its bearer.
// Resolution requires a confirmed nonce, or daemon-verified caller ancestry to
// the native socket process pinned by UID/PID/start at open and confirm. Neither
// path independently proves human origin or authenticates the native executable.
func (b *Binding) MCPEpoch() string {
	mac := hmac.New(sha256.New, []byte(b.Instance))
	mac.Write([]byte("peerturn/mcp-epoch/v1"))
	return hex.EncodeToString(mac.Sum(nil)[:16])
}

func nonce128() string { var b [16]byte; rand.Read(b[:]); return hex.EncodeToString(b[:]) }
