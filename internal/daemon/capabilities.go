package daemon

import (
	"context"
	"crypto/hmac"
	"time"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/localipc"
)

const (
	capabilityTTL         = 15 * time.Minute
	capabilityMaxLifetime = 24 * time.Hour
)

type capabilityBinding struct {
	session        string
	claudeInstance string
	nativeEpoch    string
	expiresAt      time.Time
	absoluteExpiry time.Time
}

func (d *Daemon) newCapability(ctx context.Context, request localipc.Request) (any, error) {
	if len(d.capabilities) >= 128 {
		return nil, fault.New(fault.BudgetExceeded)
	}
	capability := randomID() + randomID()
	now := time.Now()
	binding := capabilityBinding{session: request.Session, expiresAt: now.Add(capabilityTTL), absoluteExpiry: now.Add(capabilityMaxLifetime)}
	if d.state.NativeSessionID != "" || d.claudeBinding != nil {
		if !d.claudeBinding.Ready(now) || d.claudeBinding.NativeSessionID != d.state.NativeSessionID {
			return nil, rebindError()
		}
		binding.claudeInstance = d.claudeBinding.Instance
		if hmac.Equal([]byte(request.BindingEpoch), []byte(d.claudeBinding.MCPEpoch())) {
			binding.nativeEpoch = request.BindingEpoch
		}
	} else if d.state.Codex != nil && request.NativeThread == d.state.Codex.ThreadID {
		binding.nativeEpoch = "codex:" + request.NativeThread
	}
	if lifetime, ok := localipc.ConnectionLifetime(ctx); ok {
		context.AfterFunc(lifetime, func() {
			d.mu.Lock()
			defer d.mu.Unlock()
			delete(d.capabilities, capability)
		})
	}
	d.capabilities[capability] = binding
	epoch := binding.nativeEpoch
	if d.state.Codex != nil {
		epoch = ""
	}
	response := map[string]string{"capability": capability, "binding_epoch": epoch}
	if d.state.Codex != nil {
		response["runtime_version"] = d.state.Codex.Version
	}
	return response, nil
}

// Joining issues fresh peer authority, so a label/capability alone cannot outlive
// its proven Claude incarnation or promote an existing preview proxy to native.
func (d *Daemon) checkJoinCapability(capability string) error {
	if capability == "" {
		return nil
	}
	binding, ok := d.capabilities[capability]
	now := time.Now()
	if !ok || binding.session != d.state.Label || !now.Before(binding.expiresAt) || !now.Before(binding.absoluteExpiry) {
		return localipc.NewError("unbound", "unbound")
	}
	if binding.nativeEpoch == "" {
		return rebindError()
	}
	if d.state.Codex != nil {
		if binding.nativeEpoch != "codex:"+d.state.Codex.ThreadID {
			return rebindError()
		}
		return nil
	}
	if !d.claudeBinding.Ready(now) || d.claudeBinding.NativeSessionID != d.state.NativeSessionID || binding.claudeInstance != d.claudeBinding.Instance {
		return rebindError()
	}
	return nil
}

func (d *Daemon) expireCapabilities() {
	now := time.Now()
	for capability, binding := range d.capabilities {
		if !now.Before(binding.expiresAt) || !now.Before(binding.absoluteExpiry) {
			delete(d.capabilities, capability)
		}
	}
}

func (d *Daemon) validCapability(capability, session string) bool {
	binding, ok := d.capabilities[capability]
	now := time.Now()
	if !ok || binding.session != session || !now.Before(binding.expiresAt) || !now.Before(binding.absoluteExpiry) {
		return false
	}
	binding.expiresAt = now.Add(capabilityTTL)
	if binding.expiresAt.After(binding.absoluteExpiry) {
		binding.expiresAt = binding.absoluteExpiry
	}
	d.capabilities[capability] = binding
	return true
}
