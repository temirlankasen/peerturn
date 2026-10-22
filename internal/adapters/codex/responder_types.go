package codex

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/temirlankasen/peerturn/internal/fault"
)

// ResponderError contains a stable reason without native diagnostics or peer text.
type ResponderError struct{ Reason string }

func (e *ResponderError) Error() string { return e.Reason }

func responderReason(err error) string {
	var failure *ResponderError
	if errors.As(err, &failure) && failure != nil {
		switch failure.Reason {
		case "responder_failed", "responder_unavailable", "protected_path_policy_failed", "sandbox_unavailable",
			"snapshot_unavailable", "model_mismatch", "budget_unavailable", "budget_exceeded", "redaction_failed", "cancelled", "revoked", "expired":
			return failure.Reason
		}
	}
	for _, code := range []fault.Code{fault.Revoked, fault.Expired, fault.BudgetExceeded} {
		if fault.IsCode(err, code) {
			return string(code)
		}
	}
	return "responder_failed"
}

// ResponderRequest is receiver-owned admission metadata, never decoded from peer text.
// Authorize checks the current grant before spawn and again before releasing a result;
// the caller must still serialize its durable reply commit with local revocation.
type ResponderRequest struct {
	ParentThreadID string
	ParentEpoch    uint64
	InstanceID     string
	SnapshotID     string
	Model          string
	Effort         string
	MessageID      string
	AttemptID      string
	ConversationID string
	GrantID        string
	Peer           string
	Body           string
	Authorize      func(context.Context) error
}

// ResponderLimits are reserved atomically before any paid process is started.
type ResponderLimits struct {
	Deadline      time.Duration
	OutputBytes   int
	ToolCalls     int
	RunMicroUSD   int64
	DailyMicroUSD int64
}

// ResponderUsage describes native reported tokens separately from local byte limits.
type ResponderUsage struct {
	InputTokens           int64 `json:"input_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	CacheWriteInputTokens int64 `json:"cache_write_input_tokens"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
}

// ResponderBudget must persist a reservation keyed by attempt and daemon day, refusing
// unknown estimates, reused attempts and exhausted daily/per-run ceilings. A failed
// process does not imply no billing and must not make an attempt replayable.
type ResponderBudget interface {
	Reserve(context.Context, ResponderRequest, ResponderLimits) (BudgetReservation, error)
}

// BudgetReservation reconciles usage durably. Complete is false for ambiguous billing.
type BudgetReservation interface {
	Finish(ctx context.Context, usage ResponderUsage, complete bool) error
}

// ResponderConfig is trusted local configuration. Verify must qualify this exact
// executable/profile/launch environment, authority files/env/FD/proc/IPC isolation,
// inherited MCP (including PeerTurn), hosted tools/hooks/plugins/shell initialization,
// network-off tools, native tool/cost/resource caps and provider usage accounting.
// It must also verify the exact committed parent snapshot and effective model/effort.
// Nil fails closed: stock read-only Codex is not authority-secret isolation evidence.
// An empty request is a capability probe and must not claim snapshot qualification.
type ResponderConfig struct {
	Executable    string
	Directory     string
	Environment   []string
	Deadline      time.Duration
	MaxOutput     int
	MaxConcurrent int
	Verify        func(context.Context, ResponderRequest) error
	Budget        ResponderBudget
	Secrets       []KnownSecret
	run           func(context.Context, ResponderCommand, int) ([]byte, error)
}

// ResponderCapability keeps native feasibility distinct from qualified ask support.
type ResponderCapability struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// ResponderResult is a whole validated final answer; errors always return its zero value.
type ResponderResult struct {
	Answer        string
	ChildThreadID string
	Redactions    int
	Usage         ResponderUsage
}

// Responder runs separate native forks, never submitting into the owner's live queue.
type Responder struct {
	cfg           ResponderConfig
	limits        ResponderLimits
	mu            sync.Mutex
	active        int
	parents       map[string]bool
	conversations map[string]bool
}

// ResponderCommand keeps peer data off argv and credentials out of inherited env.
type ResponderCommand struct {
	Executable  string
	Args        []string
	Directory   string
	Environment []string
	Stdin       []byte
}
