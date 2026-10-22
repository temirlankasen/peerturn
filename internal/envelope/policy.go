package envelope

import (
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/identity"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// Routing is a proposed destination, never evidence that a runtime accepted input.
type Routing string

const (
	// Live uses the receiving session's normal authority; coauthor is strong trust.
	Live Routing = "live"
	// Responder requires a qualified restricted fork; no fallback to live is allowed.
	Responder Routing = "responder"
	// InboxNoWake preserves text without starting a model turn, including replies.
	InboxNoWake Routing = "inbox_no_wake"
	// Control processes a validated receipt without a model or automatic response.
	Control Routing = "control"
)

// decideRouting is the single scope dispatcher after grant and conversation checks.
// Body text is deliberately absent: only the verified grant can permit the intent.
func decideRouting(m protocol.MessagePayload, g grant.Grant, c *Conversation, admit bool) (Routing, error) {
	if err := grant.CheckScope(g.Scope, string(m.Kind)); err != nil {
		return "", err
	}
	// A parked continuation may precede its parent; only admission can check that
	// conversation. Duplicates also retain their original decision without dispatch.
	if !admit {
		return "", nil
	}
	if err := checkKind(m, g, c); err != nil {
		return "", err
	}
	switch m.Kind {
	case protocol.Coauthor:
		return Live, nil
	case protocol.Ask:
		return Responder, nil
	case protocol.Inform, protocol.Reply:
		return InboxNoWake, nil
	case protocol.Receipt:
		return Control, nil
	case protocol.KeyUpdate, protocol.GrantUpdate, protocol.Revoke:
		return "", fault.New(fault.ScopeDenied)
	}
	return "", fault.New(fault.ScopeDenied)
}

// Conversation is trusted prior chain state, bound to the signed peer and reply route.
type Conversation struct {
	// ID prevents a reply from jumping to an unrelated conversation.
	ID string
	// ParentID is the already verified input being continued.
	ParentID string
	// BudgetID prevents a continuation from selecting a fresh budget.
	BudgetID string
	// Hop is the prior depth, so the next message must advance by exactly one.
	Hop uint64
	// Peer binds continuation authority to the exact signed sender.
	Peer identity.Binding
}

// BudgetSnapshot is receiver-owned usage, scoped to the grant and receiving session.
// The caller rolls windows and persists projected counters in M1b, never from sender IDs.
type BudgetSnapshot struct {
	// MinuteStart identifies the trusted message-rate window.
	MinuteStart int64
	// MessagesInMinute counts accepted messages across sender-selected conversations.
	MessagesInMinute uint64
	// BurstUsed is the receiver's consumed burst capacity.
	BurstUsed uint64
	// TurnWindowStart identifies the trusted session turn-rate window.
	TurnWindowStart int64
	// TurnsInTenMinutes bounds automatic model turns independently of message count.
	TurnsInTenMinutes uint64
}

func checkKind(m protocol.MessagePayload, g grant.Grant, c *Conversation) error {
	switch m.Kind {
	case protocol.Inform, protocol.Ask, protocol.Coauthor, protocol.Reply:
		if m.ParentID == "" {
			if m.Hop != 0 || m.ReplyTo != "" || m.Kind == protocol.Reply || c != nil {
				return fault.New(fault.ScopeDenied)
			}
			return nil
		}
		if c == nil || c.ID != m.ConversationID || c.ParentID != m.ParentID || c.BudgetID != m.BudgetID ||
			!identity.EqualBinding(c.Peer, g.Subject) || c.Hop >= 8 || m.Hop != c.Hop+1 {
			return fault.New(fault.ScopeDenied)
		}
		if m.Kind == protocol.Reply && (m.ReplyTo != c.ParentID || g.ReplyRoute == nil || !identity.EqualBinding(*g.ReplyRoute, g.Subject)) {
			return fault.New(fault.ScopeDenied)
		}
		return nil
	case protocol.Receipt:
		_, err := protocol.DecodeReceipt([]byte(m.BodyUTF8))
		return err
	case protocol.KeyUpdate, protocol.GrantUpdate, protocol.Revoke:
		// These require their own typed issuer-signed control operation, not peer text.
		return fault.New(fault.ScopeDenied)
	}
	return fault.New(fault.ScopeDenied)
}

func checkBudget(m protocol.MessagePayload, l grant.Limits, b BudgetSnapshot, now int64) error {
	if m.Hop > l.MaxHops || b.MinuteStart > now || b.TurnWindowStart > now {
		return fault.New(fault.BudgetExceeded)
	}
	if b.BurstUsed >= l.MaxBurst {
		return fault.New(fault.BudgetExceeded)
	}
	if now-b.MinuteStart < 60 && b.MessagesInMinute >= l.MaxMessagesPerMinute {
		return fault.New(fault.BudgetExceeded)
	}
	if m.Kind == protocol.Coauthor && now-b.TurnWindowStart < 600 && b.TurnsInTenMinutes >= l.MaxTurnsPerTenMinutes {
		return fault.New(fault.BudgetExceeded)
	}
	return nil
}
