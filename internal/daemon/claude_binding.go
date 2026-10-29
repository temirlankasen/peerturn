package daemon

import (
	"context"
	"path/filepath"
	"sort"
	"time"

	"github.com/temirlankasen/peerturn/internal/claude"
	"github.com/temirlankasen/peerturn/internal/claudeipc"
	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/runtimepolicy"
)

func rebindError() error { return localipc.NewError("needs_rebind", "needs_rebind") }

// Renew only the current helper's lease before waiting on transport serialization.
// A slow relay operation must not expire a healthy polling native session.
func (d *Daemon) touchClaudeLease(r localipc.Request) {
	if r.Capability != "" || r.Instance == "" || r.Method == "adapter.open" {
		return
	}
	switch r.Method {
	case "adapter.poll", "adapter.authorize", "adapter.release", "adapter.result", "adapter.boundary", "adapter.boundary-attached", "adapter.close", "adapter.contract":
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.claudeBinding != nil {
			if err := d.claudeBinding.Touch(r.Instance, time.Now()); err != nil {
				return
			}
		}
	}
}

// Recording owner input must survive a relay operation outlasting the hook.
// Inform delivery still uses operation serialization and rechecks the binding.
func (d *Daemon) recordClaudeOwnerInvites(ctx context.Context, r localipc.Request) {
	if r.Method != "adapter.boundary" || r.Capability != "" || len(r.OwnerInvites) == 0 {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	b := d.claudeBinding
	if ctx.Err() != nil || d.persistenceFailed || b == nil || !b.CapabilityAvailable("owner_invitation") || r.NativeSession != b.NativeSessionID || r.NativeSocket != b.Socket || r.Instance != b.Instance || !b.Ready(time.Now()) {
		return
	}
	d.recordOwnerInvites(r.OwnerInvites, b.MCPEpoch())
}

func (d *Daemon) claudeDispatch(ctx context.Context, r localipc.Request) (any, error) {
	now := time.Now()
	if r.Method == "adapter.open" {
		if d.state.Codex != nil {
			return nil, rebindError()
		}
		binding, err := claude.NewBinding(r.NativeSession, r.NativeSocket, r.RuntimeVersion, r.Provider, now)
		if err != nil {
			if runtimepolicy.ErrorCode(err) != "" {
				return nil, err
			}
			return nil, rebindError()
		}
		if r.WorkingDirectory != "" {
			if !configPath(r.WorkingDirectory) {
				return nil, rebindError()
			}
			project, projectErr := filepath.EvalSymlinks(r.WorkingDirectory)
			if projectErr != nil || project == string(filepath.Separator) {
				return nil, rebindError()
			}
			binding.WorkingDirectory = project
		}
		if len(r.NativeConfiguration) != 0 {
			var configuration claude.ResponderConfiguration
			if codec.Decode(r.NativeConfiguration, 16<<10, &configuration) != nil {
				return nil, rebindError()
			}
			binding.ParentConfiguration = &configuration
		}
		if d.state.NativeSessionID != "" && d.state.NativeSessionID != r.NativeSession {
			return nil, rebindError()
		}
		if d.claudeBinding != nil && d.claudeBinding.Ready(now) {
			return nil, localipc.NewError("already_running", "bound")
		}
		if err = d.db.RecoverInDoubt(ctx, unixNow()); err != nil {
			return nil, err
		}
		d.wipeOwnerInvites()
		d.claudeBinding, d.claudeAttempt = binding, nil
		// Token/nonce binding remains available when this optional read-only
		// process proof cannot be captured; adapter.current then fails closed.
		d.claudeCurrentProof = claudeipc.CaptureNativeProcess(ctx, binding.Socket)
		d.claudeNotice = nil
		return map[string]string{"instance": binding.Instance}, nil
	}
	b := d.claudeBinding
	if b == nil {
		return nil, rebindError()
	}
	switch r.Method {
	case "adapter.challenge":
		nonce, err := b.Challenge(r.NativeSocket, now)
		if err != nil {
			return nil, rebindError()
		}
		return map[string]string{"nonce": nonce}, nil
	case "adapter.confirm":
		if err := b.Confirm(r.Nonce, r.NativeSession, r.Instance, now); err != nil {
			return nil, rebindError()
		}
		if !d.claudeCurrentProof.Valid(ctx) {
			d.claudeCurrentProof = nil
		}
		d.state.NativeSessionID = b.NativeSessionID
		d.state.ClaudeProject = b.WorkingDirectory
		if err := d.save(); err != nil {
			return nil, err
		}
		return map[string]bool{"bound": true}, nil
	case "adapter.resolve":
		if r.NativeSocket != b.Socket || !b.Ready(now) || r.Nonce != b.ConfirmedNonce {
			return nil, rebindError()
		}
		return map[string]string{"session": d.state.Label, "binding": "native_claude", "binding_epoch": b.MCPEpoch()}, nil
	case "adapter.current":
		if r.NativeSocket != b.Socket || !b.Ready(now) || !d.claudeCurrentProof.HasDescendant(ctx, localipc.PeerPID(ctx)) {
			return nil, rebindError()
		}
		return map[string]string{"session": d.state.Label, "binding": "native_claude", "binding_epoch": b.MCPEpoch()}, nil
	case "adapter.boundary", "adapter.boundary-attached":
		if r.NativeSession != b.NativeSessionID || r.NativeSocket != b.Socket {
			return nil, rebindError()
		}
		if err := b.Touch(r.Instance, now); err != nil {
			return nil, rebindError()
		}
		if !b.Ready(now) {
			if r.Method == "adapter.boundary" {
				return map[string]string{"notice_id": "", "context": ""}, nil
			}
			return nil, rebindError()
		}
		if !b.CapabilityAvailable("inform_boundary") {
			return nil, claude.RuntimeContractFailure(b.RuntimeVersion, "claude.owner_boundary", "runtime")
		}
		if r.Method == "adapter.boundary-attached" {
			return d.claudeBoundaryAttached(ctx, r)
		}
		return d.claudeBoundary(ctx)
	}
	if err := b.Touch(r.Instance, now); err != nil {
		return nil, rebindError()
	}
	if r.Method == "adapter.contract" {
		failure := claude.RuntimeContractFailure(b.RuntimeVersion, r.Kind, "runtime")
		if failure == nil {
			return nil, localipc.NewError("invalid_arguments", "rejected")
		}
		if b.ContractFailures == nil {
			b.ContractFailures = map[string]*runtimepolicy.Error{}
		}
		b.ContractFailures[r.Kind] = failure
		return map[string]bool{"recorded": true}, nil
	}
	if r.Method == "adapter.close" {
		d.cancelAskWorkers()
		if err := d.db.RecoverInDoubt(ctx, unixNow()); err != nil {
			return nil, err
		}
		d.wipeOwnerInvites()
		d.claudeBinding = nil
		d.claudeCurrentProof = nil
		d.claudeAttempt, d.claudeNotice = nil, nil
		return map[string]bool{"closed": true}, nil
	}
	if !b.Ready(now) {
		return adapterEvent{Type: "status"}, nil
	}
	switch r.Method {
	case "adapter.poll":
		if !b.CapabilityAvailable("coauthor") {
			pending, err := d.db.PendingInformCount(ctx)
			return adapterEvent{Type: "status", InformPending: pending}, err
		}
		return d.claudePoll(ctx)
	case "adapter.authorize":
		if !b.CapabilityAvailable("coauthor") {
			return nil, claude.RuntimeContractFailure(b.RuntimeVersion, "claude.lifecycle", "runtime")
		}
		return d.claudeAuthorize(ctx, r)
	case "adapter.release":
		return d.claudeRelease(ctx, r)
	case "adapter.result":
		return d.claudeResult(ctx, r)
	default:
		return nil, localipc.NewError("unsupported", "unsupported")
	}
}

func (d *Daemon) claudeStatus(ctx context.Context, s *Status) error {
	if d.state.NativeSessionID != "" && d.claudeBinding == nil {
		s.RuntimePolicy = runtimepolicy.NewReport(runtimepolicy.Claude, "")
		s.RuntimePolicy.Source = "session.version"
		s.RuntimePolicy.Contracts = []runtimepolicy.Contract{{ID: "claude.binding", Phase: "binding", Status: "not_observed", Capabilities: []string{"binding", "coauthor", "owner_invitation", "inform_boundary"}}}
	}
	if b := d.claudeBinding; b != nil {
		s.RuntimeVersion, s.Provider = b.RuntimeVersion, b.Provider
		s.RuntimePolicy = runtimepolicy.NewReport(runtimepolicy.Claude, b.RuntimeVersion)
		s.RuntimePolicy.Source = "session.version"
		bindingStatus := "not_observed"
		if b.Ready(time.Now()) {
			bindingStatus = "passed"
		}
		s.RuntimePolicy.Contracts = append(s.RuntimePolicy.Contracts, runtimepolicy.Contract{ID: "claude.binding", Phase: "binding", Status: bindingStatus, Capabilities: []string{"binding", "coauthor", "owner_invitation", "inform_boundary"}})
		ids := make([]string, 0, len(b.ContractFailures))
		for id := range b.ContractFailures {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			failure := b.ContractFailures[id]
			s.RuntimePolicy.Contracts = append(s.RuntimePolicy.Contracts, runtimepolicy.Contract{ID: failure.ContractID, Phase: failure.Phase, Status: "failed", Capabilities: failure.AffectedCapabilities, Action: failure.Action})
		}
	}
	var err error
	s.InformPending, err = d.db.PendingInformCount(ctx)
	if err != nil {
		return err
	}
	if d.state.NativeSessionID == "" {
		return nil
	}
	uncertain, uncertainErr := d.db.UncertainDeliveries(ctx)
	if uncertainErr != nil {
		return uncertainErr
	}
	s.Binding = "needs_rebind"
	s.Warning = "Coauthor is strong trust with the receiving agent's normal capabilities, not a sandbox. Peer text can influence later owner turns. Ask is unavailable until authority isolation and snapshot/model continuity are qualified."
	s.SupportedScopes = []string{"coauthor", "inform"}
	if d.claudeBinding.Ready(time.Now()) {
		s.Binding = "native_claude"
		s.Capabilities.AttachExisting = true
		s.Capabilities.WakeIdle = d.claudeBinding.CapabilityAvailable("coauthor")
		s.Capabilities.InformBoundary = d.claudeBinding.CapabilityAvailable("inform_boundary")
		s.Capabilities.BoundaryDelivery = s.Capabilities.InformBoundary
		s.Capabilities.ReceiptStages = append(s.Capabilities.ReceiptStages, "adapter_accepted", "offered", "delivered", "in_doubt", "rejected")
		if uncertain {
			s.State = "in_doubt"
			s.Capabilities.WakeIdle = false
		}
	} else {
		s.State = "needs_rebind"
	}
	return nil
}
