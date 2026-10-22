package protocol

import (
	"unicode/utf8"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/grant"
)

// Kind distinguishes data and core control objects without inspecting body text.
type Kind string

const (
	// Inform requests inbox storage without a peer-started model turn.
	Inform Kind = "inform"
	// Ask requests a separate restricted responder, never a live turn.
	Ask Kind = "ask"
	// Coauthor uses the receiving agent's normal capabilities and is strong trust.
	Coauthor Kind = "coauthor"
	// Reply must be bound to an existing verified conversation by the caller.
	Reply Kind = "reply"
	// Receipt reports evidence and never requests a model turn.
	Receipt Kind = "receipt"
	// KeyUpdate is handled by the identity core rather than a model.
	KeyUpdate Kind = "key_update"
	// GrantUpdate is meaningful only when the recipient issuer authorized it.
	GrantUpdate Kind = "grant_update"
	// Revoke is a core control object, never a body substring command.
	Revoke Kind = "revoke"
)

// MessagePayload binds content and conversation metadata to exact session epochs.
type MessagePayload struct {
	// MessageID must match routing so a fresh outer ID cannot replay signed content.
	MessageID string `json:"message_id"`
	// FromSession selects the verified sender rather than a display name.
	FromSession string `json:"from_session"`
	// FromSessionEpoch rejects lost sender continuity.
	FromSessionEpoch uint64 `json:"from_session_epoch"`
	// ToSession prevents cross-session delivery on the same device.
	ToSession string `json:"to_session"`
	// ToSessionEpoch rejects a stale receiving execution context.
	ToSessionEpoch uint64 `json:"to_session_epoch"`
	// SenderSeq detects gaps and replay within an exact directional channel.
	SenderSeq uint64 `json:"sender_seq"`
	// GrantID selects explicit recipient authority rather than transport membership.
	GrantID string `json:"grant_id"`
	// GrantEpoch binds the message to the current recipient revocation decision.
	GrantEpoch uint64 `json:"grant_epoch"`
	// GrantHash prevents swapping another signed policy under the same identifier.
	GrantHash string `json:"grant_hash"`
	// Kind requests typed intent without granting scope by itself.
	Kind Kind `json:"kind"`
	// BodyUTF8 is literal text with no attachment, expansion or command interpretation.
	BodyUTF8 string `json:"body_utf8"`
	// CreatedAt bounds admission against the grant's validity window.
	CreatedAt int64 `json:"created_at"`
	// ExpiresAt must match routing so retention cannot outlive signed intent.
	ExpiresAt int64 `json:"expires_at"`
	// ConversationID names a chain without resetting receiver-owned budgets.
	ConversationID string `json:"conversation_id"`
	// ParentID links a continuation to previously verified chain state.
	ParentID string `json:"parent_id,omitempty"`
	// ReplyTo binds a reply to an existing signed input.
	ReplyTo string `json:"reply_to,omitempty"`
	// Hop bounds automatic peer continuation depth.
	Hop uint64 `json:"hop"`
	// BudgetID must inherit the receiver's verified chain budget on continuation.
	BudgetID string `json:"budget_id"`
}

// DecodeMessage is called only after its exact signed bytes have been verified.
func DecodeMessage(data []byte) (MessagePayload, error) {
	var m MessagePayload
	if err := codec.Decode(data, MaxPlaintext, &m); err != nil {
		return m, err
	}
	return m, m.Validate()
}

// Validate rejects malformed chains and unknown kinds before they reach an adapter.
func (m MessagePayload) Validate() error {
	if err := grant.CheckScope(grant.Coauthor, string(m.Kind)); err != nil {
		return err
	}
	if !ValidID(m.MessageID) || !ValidToken(m.FromSession) || !ValidToken(m.ToSession) ||
		!ValidToken(m.GrantID) || !ValidID(m.ConversationID) || !ValidToken(m.BudgetID) || !validDigest(m.GrantHash) ||
		m.FromSessionEpoch == 0 || m.ToSessionEpoch == 0 || m.GrantEpoch == 0 || m.SenderSeq == 0 ||
		m.FromSessionEpoch > MaxSafeInteger || m.ToSessionEpoch > MaxSafeInteger || m.GrantEpoch > MaxSafeInteger ||
		m.CreatedAt <= 0 || m.ExpiresAt <= m.CreatedAt || m.ExpiresAt-m.CreatedAt > 7*24*60*60 ||
		m.ExpiresAt > MaxSafeInteger || !utf8.ValidString(m.BodyUTF8) {
		return fault.New(fault.InvalidEncoding)
	}
	if len(m.BodyUTF8) > MaxPlaintext {
		return fault.New(fault.TooLarge)
	}
	if m.Hop > 8 {
		return fault.New(fault.BudgetExceeded)
	}
	if m.ParentID != "" && !ValidID(m.ParentID) || m.ReplyTo != "" && !ValidID(m.ReplyTo) {
		return fault.New(fault.InvalidEncoding)
	}
	if m.Kind == Reply && (m.ParentID == "" || m.ReplyTo == "") {
		return fault.New(fault.ScopeDenied)
	}
	return nil
}

func validDigest(s string) bool { return len(s) == 64 && ValidID(s[:32]) && ValidID(s[32:]) }
