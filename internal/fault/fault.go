// Package fault defines stable public failure codes without sensitive details.
package fault

import "errors"

// Code identifies a stable machine-readable failure.
type Code string

// Protocol failure codes are independent of diagnostic text and wrapping.
const (
	// InvalidEncoding rejects ambiguous or malformed input before interpretation.
	InvalidEncoding Code = "invalid_encoding"
	// TooLarge identifies bounded-input rejection without exposing the input.
	TooLarge Code = "too_large"
	// UnsupportedVersion prevents interpreting unknown wire versions as v1.
	UnsupportedVersion Code = "unsupported_version"
	// InvalidSignature keeps unauthenticated content outside the trusted pipeline.
	InvalidSignature Code = "invalid_signature"
	// DecryptionFailed hides which recipient or integrity check failed.
	DecryptionFailed Code = "decryption_failed"
	// InvalidCertificate rejects certificate chains that cannot establish authority.
	InvalidCertificate Code = "invalid_certificate"
	// NeedsVerify requires an explicit trust decision instead of replacing a pin.
	NeedsVerify Code = "needs_verify"
	// AudienceMismatch prevents authority from crossing exact recipient bindings.
	AudienceMismatch Code = "audience_mismatch"
	// EpochMismatch prevents stale authority from crossing a rotation boundary.
	EpochMismatch Code = "epoch_mismatch"
	// InvalidGrant rejects authority that lacks a valid recipient-issued grant.
	InvalidGrant Code = "invalid_grant"
	// Expired prevents using authority after its signed validity window.
	Expired Code = "expired"
	// NotYetValid prevents using authority before its signed validity window.
	NotYetValid Code = "not_yet_valid"
	// ScopeDenied distinguishes a forbidden action from malformed authority.
	ScopeDenied Code = "scope_denied"
	// Revoked prevents previously valid authority from remaining usable.
	Revoked Code = "revoked"
	// Conflict identifies incompatible reuse of an existing protocol identifier.
	Conflict Code = "conflict"
	// Replay rejects repeated protocol use after its replay boundary is consumed.
	Replay Code = "replay"
	// SequenceGap identifies authenticated future traffic that may be parked within quota.
	SequenceGap Code = "sequence_gap"
	// SequenceWindowExceeded is permanently outside the bounded parking window.
	SequenceWindowExceeded Code = "sequence_window_exceeded"
	// InvalidInvite rejects onboarding material that cannot establish its proof.
	InvalidInvite Code = "invalid_invite"
	// AlreadyUsed prevents a single-use onboarding capability from being reused.
	AlreadyUsed Code = "already_used"
	// BudgetExceeded prevents admission beyond the recipient's granted limits.
	BudgetExceeded Code = "budget_exceeded"
)

// Error carries a code without including untrusted or secret input.
type Error struct {
	// Code lets callers classify failures without parsing sensitive diagnostics.
	Code Code
}

// Error returns the stable failure code.
func (e *Error) Error() string { return string(e.Code) }

// New constructs an error with a stable code.
func New(code Code) error { return &Error{Code: code} }

// IsCode reports whether err or its wrapping chain has code.
func IsCode(err error, code Code) bool {
	var target *Error
	return errors.As(err, &target) && target != nil && target.Code == code
}
