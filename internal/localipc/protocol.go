// Package localipc implements private, same-uid daemon communication.
package localipc

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/invite"
	"github.com/temirlankasen/peerturn/internal/runtimepolicy"
)

// SocketName is the fixed private daemon endpoint.
const SocketName = "daemon.sock"

// MaxFrame bounds all IPC allocation before decoding.
const MaxFrame = 1024 * 1024

// MaxInvitationTextBytes bounds explicitly pasted invitation prose before parsing.
const MaxInvitationTextBytes = 64 * 1024

// Request contains the entire allowlisted local command surface.
type Request struct {
	Method              string          `json:"method"`
	Session             string          `json:"session,omitempty"`
	Peer                string          `json:"peer,omitempty"`
	Kind                string          `json:"kind,omitempty"`
	Body                string          `json:"body,omitempty"`
	MessageID           string          `json:"message_id,omitempty"`
	GrantID             string          `json:"grant_id,omitempty"`
	Link                string          `json:"link,omitempty"`
	InvitationHandle    string          `json:"invitation_handle,omitempty"`
	InvitationText      string          `json:"invitation_text,omitempty"`
	Scope               string          `json:"scope,omitempty"`
	Capability          string          `json:"capability,omitempty"`
	Lease               bool            `json:"lease,omitempty"`
	Cursor              string          `json:"cursor,omitempty"`
	NativeSession       string          `json:"native_session,omitempty"`
	NativeSocket        string          `json:"native_socket,omitempty"`
	RuntimeVersion      string          `json:"runtime_version,omitempty"`
	Provider            string          `json:"provider,omitempty"`
	Instance            string          `json:"instance,omitempty"`
	Nonce               string          `json:"nonce,omitempty"`
	AttemptID           string          `json:"attempt_id,omitempty"`
	Outcome             string          `json:"outcome,omitempty"`
	NativeThread        string          `json:"native_thread,omitempty"`
	BindingEpoch        string          `json:"binding_epoch,omitempty"`
	OwnerInvites        []string        `json:"owner_invites,omitempty"`
	WorkingDirectory    string          `json:"working_directory,omitempty"`
	NativeConfiguration json.RawMessage `json:"native_configuration,omitempty"`
}

// Error is the stable, sanitized public failure representation.
type Error struct {
	Code        string            `json:"code"`
	Message     string            `json:"message"`
	Retryable   bool              `json:"retryable"`
	State       string            `json:"state"`
	Invitations []OwnerInvitation `json:"invitations,omitempty"`
}

func (e *Error) Error() string { return e.Code }

// NewError produces an error without copying diagnostic or user input.
func NewError(code, state string) *Error {
	return &Error{Code: code, Message: actionMessage(code), Retryable: codeExit(code) == 4 && code != "canceled" && code != "request_cancelled", State: state}
}

// PublicError removes internal error details before crossing an IPC boundary.
func PublicError(err error) *Error {
	if public, ok := errors.AsType[*Error](err); ok {
		return public
	}
	if runtimepolicy.ErrorCode(err) != "" {
		return RuntimeError(err)
	}
	if protocol, ok := errors.AsType[*fault.Error](err); ok {
		return categorizedError(string(protocol.Code))
	}
	if transport, ok := errors.AsType[publicTransportError](err); ok {
		return categorizedError(transportCode(transport))
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return NewError("timeout", "pending")
	}
	if errors.Is(err, context.Canceled) {
		return NewError("canceled", "pending")
	}
	return NewError("unavailable", "unavailable")
}

// ExitCode maps stable failures to the CLI contract.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	return codeExit(PublicError(err).Code)
}

func codeExit(code string) int {
	switch code {
	case "in_doubt":
		return 7
	case "unsupported", "unsupported_version", "runtime_version_unknown", "runtime_contract_changed", "newer_schema", "guard_unavailable", "adapter_unavailable", "responder_unavailable":
		return 6
	case "unbound", "binding_error", "binding_mismatch", "needs_rebind":
		return 3
	case "unavailable", "timeout", "canceled", "waiting_inviter", "already_running", "transport_unavailable", "runtime_unavailable", "store_unavailable", "storage_unavailable", "receipt_unavailable", "backpressure", "quota", "rate_limit", "poll_backpressure", "poll_limit", "stream_limit", "shutdown", "request_cancelled", "invalid_response":
		return 4
	case "invalid_signature", "decryption_failed", "invalid_certificate", "invalid_grant", "auth_failed", "needs_verify", "uid_mismatch", "relay_key_mismatch", "sealing_failed", "signing_unavailable", "invalid_invite_proof", "claim_reader_mismatch":
		return 5
	default:
		return 2
	}
}

type response struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

// Validate checks bounded command fields before the handler sees them.
func (r Request) Validate() error {
	if len(r.NativeConfiguration) > 16<<10 {
		return NewError("too_large", "rejected")
	}
	if len(r.OwnerInvites) > 32 || len(r.OwnerInvites) > 0 && r.Method != "adapter.boundary" && r.Method != "codex_inform" {
		return NewError("invalid_arguments", "rejected")
	}
	for _, raw := range r.OwnerInvites {
		if _, err := invite.ParseLink(raw); err != nil {
			return NewError("invalid_arguments", "rejected")
		}
	}
	if !utf8.ValidString(r.Body) || len(r.Body) > 16*1024 || len(r.Link) > 64*1024 || len(r.InvitationText) > MaxInvitationTextBytes || len(r.WorkingDirectory) > 4096 {
		return NewError("too_large", "rejected")
	}
	if !utf8.ValidString(r.InvitationText) || strings.ContainsRune(r.InvitationText, '\x00') {
		return NewError("invalid_encoding", "rejected")
	}
	if !utf8.ValidString(r.WorkingDirectory) || strings.ContainsAny(r.WorkingDirectory, "\x00\r\n") {
		return NewError("invalid_encoding", "rejected")
	}
	if (r.InvitationText != "" && r.Method != "join.text") || (r.Method == "join.text" && (strings.TrimSpace(r.InvitationText) == "" || r.Link != "")) {
		return NewError("invalid_arguments", "rejected")
	}
	for _, value := range []string{r.InvitationHandle, r.Method, r.Session, r.Peer, r.Kind, r.MessageID, r.GrantID, r.Scope, r.Capability, r.Cursor, r.NativeSession, r.NativeSocket, r.RuntimeVersion, r.Provider, r.Instance, r.Nonce, r.AttemptID, r.Outcome, r.NativeThread, r.BindingEpoch} {
		if len(value) > 512 || !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n") {
			return NewError("invalid_encoding", "rejected")
		}
	}
	if r.InvitationHandle != "" && (r.Method != "join.owner" || len(r.InvitationHandle) != 12 || strings.Trim(r.InvitationHandle, "0123456789abcdef") != "") {
		return NewError("invalid_arguments", "rejected")
	}
	if r.Method == "join.owner" && (r.Link != "" || r.InvitationText != "") {
		return NewError("invalid_arguments", "rejected")
	}

	if r.Method == "" || (r.Lease && (r.Method != "hello" || r.Capability != "")) {
		return NewError("invalid_encoding", "rejected")
	}
	return nil
}

func readFrame(reader io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint32(header[:])
	if size == 0 || size > MaxFrame {
		return nil, NewError("too_large", "rejected")
	}
	data := make([]byte, int(size))
	_, err := io.ReadFull(reader, data)
	return data, err
}

func writeFrame(writer io.Writer, data []byte) error {
	size := len(data)
	if size <= 0 || size > MaxFrame {
		return NewError("too_large", "rejected")
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(size))
	for _, part := range [][]byte{header[:], data} {
		if _, err := io.Copy(writer, strings.NewReader(string(part))); err != nil {
			return err
		}
	}
	return nil
}
