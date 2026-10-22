package protocol

import (
	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/fault"
)

// ReceiptPayload preserves the difference between storage, input, completion and reply.
type ReceiptPayload struct {
	// MessageID associates evidence with one immutable submission.
	MessageID string `json:"message_id"`
	// AttemptID distinguishes native attempts without promising exactly-once effects.
	AttemptID string `json:"attempt_id"`
	// Stage preserves the difference between storage, acceptance and execution.
	Stage string `json:"stage"`
	// EvidenceKind records what supports a stage rather than inferring it from exit status.
	EvidenceKind string `json:"evidence_kind"`
	// At timestamps the evidence without implying causal runtime binding.
	At int64 `json:"at"`
	// NativeInputID can support reconciliation and remains inside ciphertext.
	NativeInputID string `json:"native_input_id,omitempty"`
	// NativeTurnID binds a completion to the observed runtime turn when available.
	NativeTurnID string `json:"native_turn_id,omitempty"`
	// Outcome records a bounded terminal result without replacing Stage.
	Outcome string `json:"outcome,omitempty"`
	// ReasonCode carries a bounded machine-readable reason rather than raw diagnostics.
	ReasonCode string `json:"reason_code,omitempty"`
}

// DecodeReceipt accepts bounded typed evidence, never a claim of exactly-once effects.
func DecodeReceipt(data []byte) (ReceiptPayload, error) {
	var r ReceiptPayload
	if err := codec.Decode(data, 4096, &r); err != nil {
		return r, err
	}
	return r, r.Validate()
}

// Validate keeps unsupported receipt stages from silently implying delivery.
func (r ReceiptPayload) Validate() error {
	if !ValidID(r.MessageID) || !ValidID(r.AttemptID) || !ValidToken(r.EvidenceKind) || r.At <= 0 || r.At > MaxSafeInteger ||
		len(r.NativeInputID) > 256 || len(r.NativeTurnID) > 256 || len(r.Outcome) > 128 || len(r.ReasonCode) > 128 {
		return fault.New(fault.InvalidEncoding)
	}
	switch r.Stage {
	case "sender_stored", "relay_stored", "receiver_stored", "offered", "delivered", "adapter_accepted", "responder_started", "model_input", "turn_completed", "reply_redacted", "reply", "responder_failed", "in_doubt", "rejected", "expired", "revoked", "cancelled":
		return nil
	default:
		return fault.New(fault.InvalidEncoding)
	}
}
