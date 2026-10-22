package daemon

import (
	"context"
	"errors"
	"os"
	"runtime"
	"time"

	"github.com/temirlankasen/peerturn/internal/claude"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/keystore"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/store"
)

// captureAskOptions copies local launch inputs under mu, without contacting an
// OS service, hashing an executable or opening the native conversation journal.
func (d *Daemon) captureAskOptions() (claude.ResponderOptions, error) {
	var options claude.ResponderOptions
	if d.askCleanupFailed {
		return options, localipc.NewError("responder_unavailable", "fork_cleanup_failed")
	}
	if d.state.Codex != nil || d.state.NativeSessionID == "" || !d.claudeBinding.Ready(time.Now()) {
		return options, localipc.NewError("responder_unavailable", "native_binding_unavailable")
	}
	if !d.authoritySecretsIsolated {
		return options, localipc.NewError("responder_unavailable", "authority_file_key")
	}
	if d.state.ClaudeProject == "" || d.state.ClaudeProject != d.claudeBinding.WorkingDirectory {
		return options, localipc.NewError("responder_unavailable", "bound_project_unavailable")
	}
	paths, protected, err := d.responderDirectories()
	if err != nil {
		return options, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return options, err
	}
	if _, err = claude.QualifyResponderProjectRoot(d.state.ClaudeProject, home); err != nil {
		return options, err
	}
	if reason := responderPlatformFailure(runtime.GOOS); reason != "" {
		return options, localipc.NewError("responder_unavailable", reason)
	}
	policy := defaultAskPolicy()
	if d.state.AskBudget != nil {
		policy = d.state.AskBudget.Policy
	}
	if !validAskPolicy(policy) || d.state.AskBudget != nil && !validAskBudget(d.state.AskBudget) {
		return options, localipc.NewError("responder_unavailable", "budget_unavailable")
	}
	options = claude.ResponderOptions{WorkingDirectory: d.state.ClaudeProject,
		HomeDirectory: home, AuthorityPaths: paths, ProtectedPaths: protected,
		Timeout: time.Duration(policy.DeadlineSeconds) * time.Second, MaxConcurrent: policy.MaxConcurrent,
		MaxOutputBytes: policy.MaxOutputBytes, AuthorityCheck: d.checkAuthorityKey}
	for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "ANTHROPIC_MODEL",
		"ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CONFIG_DIR"} {
		if value, found := os.LookupEnv(key); found {
			options.RuntimeEnv = append(options.RuntimeEnv, key+"="+value)
		}
	}
	for _, secret := range [][]byte{d.state.RootSeed, d.state.DeviceSeed, d.state.SigningSeed, d.state.EncryptionKey, d.state.RoutingSeed} {
		options.KnownSecrets = append(options.KnownSecrets, string(secret))
	}
	if parent := d.claudeBinding.ParentConfiguration; parent != nil {
		copyOfParent := *parent
		options.ParentConfiguration = &copyOfParent
	}
	return options, nil
}

func qualifyAskOptions(ctx context.Context, options claude.ResponderOptions, native string) (claude.ResponderOptions, *claude.ResponderSnapshot, error) {
	if options.AuthorityCheck == nil || options.AuthorityCheck(ctx) != nil {
		return options, nil, localipc.NewError("responder_unavailable", "keystore_unavailable")
	}
	options, err := claude.ConfigureResponder(options, options.ParentConfiguration)
	if err != nil {
		return options, nil, err
	}
	if err = claude.CheckResponderProfile(ctx, options); err != nil {
		return options, nil, err
	}
	snapshot, err := claude.AcquireResponderSnapshot(options, native)
	if err != nil {
		return options, nil, err
	}
	return options, snapshot, nil
}

func askFailureReason(err error) string {
	if errors.Is(err, keystore.ErrUnavailable) {
		return "keystore_unavailable"
	}
	if failure, ok := errors.AsType[*claude.ResponderError](err); ok {
		return string(failure.Code)
	}
	if failure, ok := errors.AsType[*localipc.Error](err); ok {
		if failure.State != "" {
			return failure.State
		}
		return failure.Code
	}
	if failure, ok := errors.AsType[*fault.Error](err); ok {
		return string(failure.Code)
	}
	return "responder_failed"
}

func askPeerFailure(reason string) string {
	switch reason {
	case "budget_exceeded", "responder_busy", "backpressure", "conflict":
		return "busy"
	case "responder_failed", "runtime_timeout", "redaction_failed", "cancelled", "storage_unavailable", "sealing_failed":
		return "failed"
	default:
		return "unavailable"
	}
}

// startClaudeAsk holds both locks only for cheap admission and durable quota
// reservation. Qualification and the supervised child run without either lock.
func (d *Daemon) startClaudeAsk(ctx context.Context, item store.InboxEntry) error {
	if d.state.AskBudget != nil {
		for _, run := range d.state.AskBudget.Runs {
			if run.Active && run.Message == item.MessageID {
				return nil
			}
		}
	}
	peer, err := d.claudeSender(item)
	if err != nil {
		return d.rejectResponderReason(ctx, item, askFailureReason(err))
	}
	if err = d.checkAskBudget(peer, d.state.NativeSessionID, item.ConversationID, item.MessageID, unixNow()); err != nil {
		return d.rejectResponderReason(ctx, item, askFailureReason(err))
	}
	_, peerState, err := d.askPeer(ctx, item.MessageID)
	if err != nil {
		return d.rejectResponderReason(ctx, item, askFailureReason(err))
	}
	outbound, _, err := d.outgoing(peerState)
	if err != nil {
		return d.rejectResponderReason(ctx, item, askFailureReason(err))
	}
	if err = d.db.CheckAskAdmission(ctx, store.AskAdmission{MessageID: item.MessageID, Parent: d.cert.Binding,
		ParentContext: bindingContext(d.state), NotificationGrantID: outbound.GrantID, Now: unixNow()}); err != nil {
		if retryCustody(err) {
			return err
		}
		return d.rejectResponderReason(ctx, item, askFailureReason(err))
	}
	options, err := d.captureAskOptions()
	if err != nil {
		return d.rejectResponderReason(ctx, item, askFailureReason(err))
	}
	text, err := d.framedEntry(item, "ask")
	if err != nil {
		return d.rejectResponderReason(ctx, item, askFailureReason(err))
	}
	budget, err := d.reserveAskBudget(peer, d.state.NativeSessionID, item.ConversationID, item.MessageID, unixNow())
	if err != nil {
		return d.rejectResponderReason(ctx, item, askFailureReason(err))
	}
	runCtx, cancel := context.WithDeadline(d.askContext, time.Unix(budget.Deadline, 0))
	d.askActive[budget.ID] = cancel
	d.askWorkers.Add(1)
	native, instance := d.state.NativeSessionID, d.claudeBinding.Instance
	go func() {
		defer d.askWorkers.Done()
		defer cancel()
		d.prepareClaudeAsk(runCtx, budget, item, native, instance, text, outbound.GrantID, options)
	}()
	return nil
}
