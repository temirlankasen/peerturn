package daemon

import (
	"context"
	"path/filepath"
	"time"

	"github.com/temirlankasen/peerturn/internal/claude"
	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/localipc"
)

func rebindError() error { return localipc.NewError("needs_rebind", "needs_rebind") }

// Renew only the current helper's lease before waiting on transport serialization.
// A slow relay operation must not expire a healthy polling native session.
func (d *Daemon) touchClaudeLease(r localipc.Request) {
	if r.Capability != "" || r.Instance == "" || r.Method == "adapter.open" {
		return
	}
	switch r.Method {
	case "adapter.poll", "adapter.authorize", "adapter.release", "adapter.result", "adapter.boundary", "adapter.boundary-attached", "adapter.close":
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.claudeBinding != nil {
			if err := d.claudeBinding.Touch(r.Instance, time.Now()); err != nil {
				return
			}
		}
	}
}

func (d *Daemon) claudeDispatch(ctx context.Context, r localipc.Request) (any, error) {
	now := time.Now()
	if r.Method == "adapter.open" {
		if d.state.Codex != nil {
			return nil, rebindError()
		}
		binding, err := claude.NewBinding(r.NativeSession, r.NativeSocket, r.RuntimeVersion, r.Provider, now)
		if err != nil {
			if r.RuntimeVersion != "2.1.306" {
				return nil, localipc.NewError("unsupported_version", "unsupported")
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
		d.claudeBinding, d.claudeAttempt = binding, nil
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
		if r.Method == "adapter.boundary-attached" {
			return d.claudeBoundaryAttached(ctx, r)
		}
		d.recordOwnerInvites(r.OwnerInvites, b.MCPEpoch())
		return d.claudeBoundary(ctx)
	}
	if err := b.Touch(r.Instance, now); err != nil {
		return nil, rebindError()
	}
	if r.Method == "adapter.close" {
		d.cancelAskWorkers()
		if err := d.db.RecoverInDoubt(ctx, unixNow()); err != nil {
			return nil, err
		}
		d.claudeBinding = nil
		d.claudeAttempt, d.claudeNotice = nil, nil
		return map[string]bool{"closed": true}, nil
	}
	if !b.Ready(now) {
		return adapterEvent{Type: "status"}, nil
	}
	switch r.Method {
	case "adapter.poll":
		return d.claudePoll(ctx)
	case "adapter.authorize":
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
		s.RuntimeVersion, s.Provider = d.claudeBinding.RuntimeVersion, d.claudeBinding.Provider
		s.Capabilities.AttachExisting, s.Capabilities.WakeIdle = true, true
		s.Capabilities.InformBoundary, s.Capabilities.BoundaryDelivery = true, true
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
