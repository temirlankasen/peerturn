package codex

import (
	"context"
	"path/filepath"
	"time"
)

// NewResponder defaults to 120 seconds, 16 KiB final output, eight tools, two
// daemon forks and one per parent/conversation. It never supplies a live fallback.
func NewResponder(cfg ResponderConfig) (*Responder, error) {
	if !filepath.IsAbs(cfg.Executable) || !filepath.IsAbs(cfg.Directory) {
		return nil, &ResponderError{Reason: "responder_unavailable"}
	}
	if cfg.Deadline == 0 {
		cfg.Deadline = 120 * time.Second
	}
	if cfg.MaxOutput == 0 {
		cfg.MaxOutput = 16 << 10
	}
	if cfg.MaxConcurrent == 0 {
		cfg.MaxConcurrent = 2
	}
	if cfg.Deadline <= 0 || cfg.Deadline > 120*time.Second || cfg.MaxOutput <= 0 || cfg.MaxOutput > 16<<10 || cfg.MaxConcurrent < 1 || cfg.MaxConcurrent > 2 {
		return nil, &ResponderError{Reason: "budget_exceeded"}
	}
	if _, err := responderEnvironment(cfg.Environment); err != nil {
		return nil, err
	}
	if cfg.run == nil {
		cfg.run = runResponderProcess
	}
	cfg.Environment = append([]string(nil), cfg.Environment...)
	cfg.Secrets = append([]KnownSecret(nil), cfg.Secrets...)
	return &Responder{cfg: cfg, limits: ResponderLimits{cfg.Deadline, cfg.MaxOutput, 8, 1_000_000, 10_000_000},
		parents: make(map[string]bool), conversations: make(map[string]bool)}, nil
}

// Capability refuses to infer security qualification from successful native launch.
func (r *Responder) Capability(ctx context.Context) ResponderCapability {
	if r.cfg.Verify == nil {
		return ResponderCapability{Reason: "protected_path_policy_failed"}
	}
	if err := r.cfg.Verify(ctx, ResponderRequest{}); err != nil {
		return ResponderCapability{Reason: responderReason(err)}
	}
	if r.cfg.Budget == nil {
		return ResponderCapability{Reason: "budget_unavailable"}
	}
	return ResponderCapability{Available: true}
}

// Respond returns no partial, tool, diagnostic or unredacted output on any failure.
func (r *Responder) Respond(ctx context.Context, request ResponderRequest) (ResponderResult, error) {
	ctx, cancel := context.WithTimeout(ctx, r.limits.Deadline)
	defer cancel()
	command, err := BuildResponderCommand(r.cfg.Executable, r.cfg.Directory, r.cfg.Environment, request)
	if err != nil {
		return ResponderResult{}, err
	}
	if r.cfg.Verify == nil {
		return ResponderResult{}, &ResponderError{Reason: "protected_path_policy_failed"}
	}
	if err = r.cfg.Verify(ctx, request); err != nil {
		return ResponderResult{}, safeResponderError(err)
	}
	if err = request.Authorize(ctx); err != nil {
		return ResponderResult{}, safeResponderError(err)
	}
	if err = r.acquire(request); err != nil {
		return ResponderResult{}, err
	}
	defer r.release(request)
	if r.cfg.Budget == nil {
		return ResponderResult{}, &ResponderError{Reason: "budget_unavailable"}
	}
	reservation, err := r.cfg.Budget.Reserve(ctx, request, r.limits)
	if err != nil {
		return ResponderResult{}, safeResponderError(err)
	}
	if reservation == nil {
		return ResponderResult{}, &ResponderError{Reason: "budget_unavailable"}
	}
	var result ResponderResult
	err = request.Authorize(ctx)
	if err == nil {
		result, err = r.execute(ctx, command, request)
	} else {
		err = safeResponderError(err)
	}
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer finishCancel()
	if finishErr := reservation.Finish(finishCtx, result.Usage, err == nil); finishErr != nil {
		return ResponderResult{}, &ResponderError{Reason: "budget_unavailable"}
	}
	if err != nil {
		return ResponderResult{}, err
	}
	if err = ctx.Err(); err != nil {
		return ResponderResult{}, &ResponderError{Reason: "cancelled"}
	}
	if err = request.Authorize(ctx); err != nil {
		return ResponderResult{}, safeResponderError(err)
	}
	return result, nil
}

func (r *Responder) execute(ctx context.Context, command ResponderCommand, request ResponderRequest) (ResponderResult, error) {
	raw, err := r.cfg.run(ctx, command, 256<<10)
	if err != nil {
		return ResponderResult{}, safeResponderError(err)
	}
	result, err := parseResponderOutput(raw, request.ParentThreadID, r.limits)
	if err != nil {
		return ResponderResult{}, err
	}
	redacted, err := RedactAnswer(result.Answer, r.cfg.Secrets)
	if err != nil {
		return ResponderResult{}, err
	}
	if len(redacted.Text) > r.limits.OutputBytes {
		return ResponderResult{}, &ResponderError{Reason: "budget_exceeded"}
	}
	result.Answer, result.Redactions = redacted.Text, redacted.Count
	return result, nil
}

func (r *Responder) acquire(request ResponderRequest) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active >= r.cfg.MaxConcurrent || r.parents[request.ParentThreadID] || r.conversations[request.ConversationID] {
		return &ResponderError{Reason: "budget_exceeded"}
	}
	r.active++
	r.parents[request.ParentThreadID] = true
	r.conversations[request.ConversationID] = true
	return nil
}

func (r *Responder) release(request ResponderRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.active--
	delete(r.parents, request.ParentThreadID)
	delete(r.conversations, request.ConversationID)
}

func safeResponderError(err error) error { return &ResponderError{Reason: responderReason(err)} }
