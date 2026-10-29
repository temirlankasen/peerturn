// Package codex connects an explicitly bound live thread to its shared daemon.
package codex

import (
	"context"
	"errors"

	"github.com/temirlankasen/peerturn/internal/runtimepolicy"
)

// RPC permits deterministic tests without starting or discovering native sessions.
type RPC interface {
	Call(ctx context.Context, method string, params, result any) error
}

// Binding identifies an exact thread in one local daemon incarnation.
type Binding struct {
	ThreadID string `json:"thread_id"`
	DaemonID string `json:"daemon_id"`
	Version  string `json:"version"`
}

// QueueResult proves native queue acceptance, not model input or turn completion.
type QueueResult struct {
	ItemIDs             []string `json:"item_ids"`
	ClientUserMessageID string   `json:"client_user_message_id"`
}

// Capabilities describes only evidence available from this transport probe.
type Capabilities struct {
	AttachExisting  bool                     `json:"attach_existing"`
	WakeIdle        bool                     `json:"wake_idle"`
	ForkResponder   bool                     `json:"fork_responder"`
	SupportedScopes []string                 `json:"supported_scopes"`
	ReceiptStages   []string                 `json:"receipt_stages"`
	Version         string                   `json:"version"`
	Contracts       []runtimepolicy.Contract `json:"contracts"`
}

// Error carries a stable failure code without native paths, bodies or diagnostics.
type Error struct {
	Code string
}

func (e *Error) Error() string { return e.Code }

// IsCode classifies an adapter failure without parsing untrusted native text.
func IsCode(err error, code string) bool {
	var target *Error
	return (errors.As(err, &target) && target != nil && target.Code == code) || runtimepolicy.ErrorCode(err) == code
}

func failure(code string) error { return &Error{Code: code} }

// RPCError reports a native JSON-RPC rejection without retaining its message.
type RPCError struct {
	Code int
}

func (*RPCError) Error() string { return "codex_rpc_rejected" }

type nativeInput struct {
	Type         string `json:"type"`
	Text         string `json:"text"`
	TextElements []any  `json:"text_elements"`
}

type queueParams struct {
	ThreadID            string        `json:"threadId"`
	Input               []nativeInput `json:"input"`
	ClientUserMessageID string        `json:"clientUserMessageId"`
}

type queuedSubmission struct {
	ID                  string        `json:"id"`
	Input               []nativeInput `json:"input"`
	ClientUserMessageID string        `json:"clientUserMessageId"`
}
