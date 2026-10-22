// Package grant enforces recipient-issued, directional session authority.
package grant

import (
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/identity"
)

// Scope is the recipient-approved ceiling on a peer's intent.
type Scope string

const (
	// Inform permits inbox text without waking a model.
	Inform Scope = "inform"
	// Ask permits a fork question and its bound conversation reply.
	Ask Scope = "ask"
	// Coauthor grants the receiving agent's normal capabilities; it is not a sandbox.
	Coauthor Scope = "coauthor"
	// DefaultLifetime is the default owner policy lifetime in seconds.
	DefaultLifetime int64 = 24 * 60 * 60
	// MaxLifetime bounds every signed grant to thirty days in seconds.
	MaxLifetime int64 = 30 * DefaultLifetime
)

const (
	maxSafeInteger uint64 = 1<<53 - 1
	maxGrantBytes         = 16 * 1024
)

// Limits are receiver-enforced ceilings; a sender cannot reset these counters.
type Limits struct {
	// MaxMessagesPerMinute bounds accepted traffic for this directional grant.
	MaxMessagesPerMinute uint64 `json:"max_messages_per_minute"`
	// MaxBurst prevents a sender from spending the whole traffic budget at once.
	MaxBurst uint64 `json:"max_burst"`
	// MaxTurnsPerTenMinutes bounds automatic model work independently of traffic.
	MaxTurnsPerTenMinutes uint64 `json:"max_turns_per_ten_minutes"`
	// MaxHops bounds automatic conversation chains to prevent unbounded loops.
	MaxHops uint64 `json:"max_hops"`
	// MaxOutstandingAsks limits concurrent questions within a conversation.
	MaxOutstandingAsks uint64 `json:"max_outstanding_asks"`
}

// Grant is a recipient signature's exact authorization for one sender session.
type Grant struct {
	// Version prevents silently accepting an incompatible authorization profile.
	Version int `json:"wire_version"`
	// GrantID selects the recipient's independently revocable authorization.
	GrantID string `json:"grant_id"`
	// Issuer pins the receiving session whose signature can grant this direction.
	Issuer identity.Binding `json:"issuer"`
	// Subject prevents another sender session or key from inheriting the grant.
	Subject identity.Binding `json:"subject"`
	// Scope sets the recipient's ceiling; message text cannot widen it.
	Scope Scope `json:"scope"`
	// GrantEpoch must match live recipient policy so stale grants cannot revive.
	GrantEpoch uint64 `json:"grant_epoch"`
	// NotBefore is the inclusive Unix-second start of the owner's authorization.
	NotBefore int64 `json:"not_before"`
	// ExpiresAt is the exclusive Unix-second end, bounding unattended access.
	ExpiresAt int64 `json:"expires_at"`
	// Limits commits to receiver-enforced budgets rather than sender requests.
	Limits Limits `json:"limits"`
	// ReplyRoute restricts responses to the exact signed peer, never arbitrary peers.
	ReplyRoute *identity.Binding `json:"reply_route,omitempty"`
}

// DefaultLimits returns the signed baseline limits from the security contract.
func DefaultLimits() Limits {
	return Limits{MaxMessagesPerMinute: 10, MaxBurst: 5, MaxTurnsPerTenMinutes: 20,
		MaxHops: 8, MaxOutstandingAsks: 1}
}

// CheckScope enforces the recipient's ceiling independently of body text and routing.
// Control kinds still require their own issuer check in the envelope pipeline.
func CheckScope(scope Scope, kind string) error {
	if scope != Inform && scope != Ask && scope != Coauthor {
		return fault.New(fault.InvalidGrant)
	}
	switch kind {
	case "inform", "receipt":
		return nil
	case "ask", "reply":
		if scope == Ask || scope == Coauthor {
			return nil
		}
	case "coauthor", "key_update", "grant_update", "revoke":
		if scope == Coauthor {
			return nil
		}
	}
	return fault.New(fault.ScopeDenied)
}

// CompareEpoch refuses stale grants and never accepts a future sender epoch.
func CompareEpoch(present, current uint64) error {
	if present == 0 || current == 0 || present > maxSafeInteger || current > maxSafeInteger {
		return fault.New(fault.EpochMismatch)
	}
	if present < current {
		return fault.New(fault.Revoked)
	}
	if present > current {
		return fault.New(fault.EpochMismatch)
	}
	return nil
}

func validate(g Grant) error {
	if g.Version != 1 {
		return fault.New(fault.UnsupportedVersion)
	}
	if !validID(g.GrantID) || g.GrantEpoch == 0 || g.GrantEpoch > maxSafeInteger ||
		g.NotBefore < 0 || g.ExpiresAt <= g.NotBefore || g.ExpiresAt > int64(maxSafeInteger) ||
		g.ExpiresAt-g.NotBefore > MaxLifetime || !validLimits(g.Limits) {
		return fault.New(fault.InvalidGrant)
	}
	if identity.ValidateBinding(g.Issuer) != nil || identity.ValidateBinding(g.Subject) != nil {
		return fault.New(fault.InvalidGrant)
	}
	if err := CheckScope(g.Scope, "receipt"); err != nil {
		return err
	}
	if g.ReplyRoute != nil && (g.Scope == Inform || !identity.EqualBinding(*g.ReplyRoute, g.Subject)) {
		return fault.New(fault.InvalidGrant)
	}
	if g.Scope == Ask && g.ReplyRoute == nil {
		return fault.New(fault.InvalidGrant)
	}
	return nil
}

func validLimits(l Limits) bool {
	return l.MaxMessagesPerMinute > 0 && l.MaxMessagesPerMinute <= maxSafeInteger &&
		l.MaxBurst > 0 && l.MaxBurst <= maxSafeInteger &&
		l.MaxTurnsPerTenMinutes > 0 && l.MaxTurnsPerTenMinutes <= maxSafeInteger &&
		l.MaxHops > 0 && l.MaxHops <= 8 && l.MaxOutstandingAsks > 0 && l.MaxOutstandingAsks <= maxSafeInteger
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
