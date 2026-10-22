package claude

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/temirlankasen/peerturn/internal/codec"
)

// ResponderAuthorityPaths names actual daemon paths, including custom and XDG
// selections. A home-directory guess cannot identify the authority boundary.
type ResponderAuthorityPaths struct {
	StateDirectory, ConfigFile, RuntimeDirectory string
}

// ResponderOptions is trusted daemon configuration, never peer-controlled input.
type ResponderOptions struct {
	Executable, WorkingDirectory, HomeDirectory string
	ProtectedPaths, KnownSecrets, RuntimeEnv    []string
	AuthorityPaths                              ResponderAuthorityPaths
	ParentConfiguration                         *ResponderConfiguration
	Timeout                                     time.Duration
	MaxConcurrent, MaxOutputBytes               int
	// AuthorityCheck must qualify files, aliases, env, descriptors, process and
	// live-control access for this exact launch profile. Nil always fails closed.
	// Generating deny rules or returning a successful shell exit is not proof.
	AuthorityCheck func(context.Context) error
	Observer       func(context.Context, ResponderStage, string) error
	// PrepareFork durably records the selected local UUID before native Start,
	// so recovery can remove a fork that never emitted valid init. Nil refuses
	// launch; this records custody, never process acceptance or model input.
	PrepareFork func(context.Context, string) error
	// FinishFork records successful artifact removal in the supervisor's ledger.
	FinishFork func(context.Context, string) error
	// AdmitPeerInput serializes the actual bounded submit with revoke/expiry.
	AdmitPeerInput func(context.Context, string, func() error) error
}

// AskRequest contains a daemon-verified native parent and grant-aware data frame.
type AskRequest struct {
	NativeSessionID, FramedQuestion string
	Snapshot                        *ResponderSnapshot
}

// ResponderStage describes local evidence; it never infers model input from exit.
type ResponderStage string

const (
	// ResponderStarted records native process acceptance, not model consumption.
	ResponderStarted ResponderStage = "responder_started"
	// ResponderModelInput requires a native assistant event for the peer turn.
	ResponderModelInput ResponderStage = "model_input"
	// ResponderCompleted requires a complete successful native result envelope.
	ResponderCompleted ResponderStage = "turn_completed"
	// OutboundRedacted is local outbound-policy evidence, not a sent reply.
	OutboundRedacted ResponderStage = "outbound_redacted"
)

// AskResult exposes only the final filtered answer and bounded local evidence.
type AskResult struct {
	Answer          string
	Stages          []ResponderStage
	Redactions      int
	NativeSessionID string
}

// ResponderFailure is a stable failure code with no raw child diagnostics.
type ResponderFailure string

const (
	// ProtectedPathPolicyFailed rejects absent or failed authority isolation.
	ProtectedPathPolicyFailed ResponderFailure = "protected_path_policy_failed"
	// InvalidResponderRequest rejects malformed launch identity or input.
	InvalidResponderRequest ResponderFailure = "invalid_request"
	// ResponderUnavailable rejects missing executables or unsupported profiles.
	ResponderUnavailable ResponderFailure = "responder_unavailable"
	// ResponderBusy refuses an overlapping parent or exhausted daemon capacity.
	ResponderBusy ResponderFailure = "responder_busy"
	// ResponderFailed rejects exit failures and unconfirmed final output.
	ResponderFailed ResponderFailure = "responder_failed"
	// ResponderTimeout means the receiver-owned deadline elapsed.
	ResponderTimeout ResponderFailure = "runtime_timeout"
	// ResponderCancelled discards output after cancellation or revocation.
	ResponderCancelled ResponderFailure = "cancelled"
	// ResponderBudgetExceeded bounds stdout before retaining a whole answer.
	ResponderBudgetExceeded ResponderFailure = "budget_exceeded"
	// RedactionFailed never falls back to an unfiltered answer.
	RedactionFailed ResponderFailure = "redaction_failed"
	// ManagedPolicyPresent refuses managed code or settings before it can run.
	ManagedPolicyPresent ResponderFailure = "managed_policy_present"
	// ManagedPolicyUnverified covers sources that cannot be inspected reliably.
	ManagedPolicyUnverified ResponderFailure = "managed_policy_unverified"
	// ProfileMismatch refuses unexpected native tools, MCP, plugins or hooks.
	ProfileMismatch ResponderFailure = "profile_mismatch"
	// SnapshotUnavailable rejects missing or changed native conversation evidence.
	SnapshotUnavailable ResponderFailure = "snapshot_unavailable"
	// ModelMismatch rejects model or effort drift before peer input.
	ModelMismatch ResponderFailure = "model_mismatch"
)

// ResponderError deliberately omits paths, native identities and child output.
type ResponderError struct {
	Code ResponderFailure
}

func (e *ResponderError) Error() string { return string(e.Code) }

// Responder runs a fixed restricted native fork. It does not qualify the runtime,
// reserve receiver quotas, or authorize a reply commit; the daemon owns those.
type Responder struct {
	options   ResponderOptions
	mu        sync.Mutex
	active    map[string]bool
	preflight func(context.Context, ResponderOptions) error
}

// NewResponder validates fixed configuration without claiming runtime isolation.
func NewResponder(options ResponderOptions) (*Responder, error) {
	if err := validateResponderOptions(options); err != nil {
		return nil, err
	}
	if options.Timeout == 0 {
		options.Timeout = 2 * time.Minute
	}
	if options.MaxConcurrent == 0 {
		options.MaxConcurrent = 2
	}
	if options.MaxOutputBytes == 0 {
		options.MaxOutputBytes = 16 << 10
	}
	options.ProtectedPaths = append([]string(nil), options.ProtectedPaths...)
	options.KnownSecrets = append([]string(nil), options.KnownSecrets...)
	options.RuntimeEnv = append([]string(nil), options.RuntimeEnv...)
	for _, entry := range options.RuntimeEnv {
		key, value, _ := strings.Cut(entry, "=")
		if key == "ANTHROPIC_API_KEY" || key == "ANTHROPIC_AUTH_TOKEN" || key == "CLAUDE_CODE_OAUTH_TOKEN" {
			options.KnownSecrets = append(options.KnownSecrets, value)
		}
	}
	if len(options.KnownSecrets) > 128 {
		return nil, &ResponderError{Code: InvalidResponderRequest}
	}
	return &Responder{options: options, active: make(map[string]bool), preflight: CheckResponderProfile}, nil
}

// Run never retries a native submission. Callers must supply a fresh grant check
// and exact binding/snapshot/model qualification before admission and reply commit.
func (r *Responder) Run(ctx context.Context, request AskRequest) (AskResult, error) {
	var result AskResult
	if !validNativeSessionID(request.NativeSessionID) || len(request.FramedQuestion) == 0 ||
		len(request.FramedQuestion) > 65536 || !utf8.ValidString(request.FramedQuestion) {
		return result, &ResponderError{Code: InvalidResponderRequest}
	}
	if err := request.Snapshot.verify(request.NativeSessionID, r.options.WorkingDirectory); err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(ctx, r.options.Timeout)
	defer cancel()
	if code := responderContextFailure(ctx); code != "" {
		return result, &ResponderError{Code: code}
	}
	if !r.reserve(request.NativeSessionID) {
		return result, &ResponderError{Code: ResponderBusy}
	}
	defer r.release(request.NativeSessionID)
	if r.options.AuthorityCheck == nil || r.options.AuthorityCheck(ctx) != nil {
		return result, &ResponderError{Code: ProtectedPathPolicyFailed}
	}
	if err := r.preflight(ctx, r.options); err != nil {
		return result, err
	}
	if code := responderContextFailure(ctx); code != "" {
		return result, &ResponderError{Code: code}
	}
	return r.execute(ctx, cancel, request)
}

func responderCommand(ctx context.Context, executable string) *exec.Cmd {
	// The executable was validated as an absolute trusted configuration path;
	// the fixed argument profile is assembled separately, never by a shell.
	return exec.CommandContext(ctx, executable)
}

func (r *Responder) reserve(parent string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active[parent] || len(r.active) >= r.options.MaxConcurrent {
		return false
	}
	r.active[parent] = true
	return true
}

func (r *Responder) release(parent string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.active, parent)
}

func responderContextFailure(ctx context.Context) ResponderFailure {
	if ctx.Err() == context.DeadlineExceeded {
		return ResponderTimeout
	}
	if ctx.Err() != nil {
		return ResponderCancelled
	}
	return ""
}

func decodeResponderAnswer(data []byte, parent string, limit int) (string, error) {
	failure := &ResponderError{Code: ResponderFailed}
	if codec.Validate(data, limit) != nil {
		return "", failure
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return "", failure
	}
	var kind, subtype, session, answer string
	var isError bool
	for name, target := range map[string]any{
		"type": &kind, "subtype": &subtype, "session_id": &session, "is_error": &isError,
	} {
		if len(fields[name]) == 0 || string(fields[name]) == "null" || json.Unmarshal(fields[name], target) != nil {
			return "", failure
		}
	}
	if kind == "result" && validNativeSessionID(session) && session != parent &&
		(subtype == "error_max_turns" || subtype == "error_max_budget_usd") {
		return "", &ResponderError{Code: ResponderBudgetExceeded}
	}
	if len(fields["result"]) == 0 || string(fields["result"]) == "null" || json.Unmarshal(fields["result"], &answer) != nil {
		return "", failure
	}
	if kind != "result" || subtype != "success" || isError || !validNativeSessionID(session) ||
		session == parent || strings.TrimSpace(answer) == "" {
		return "", failure
	}
	return answer, nil
}
