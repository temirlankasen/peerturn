package daemon

import (
	"context"
	"time"

	"github.com/temirlankasen/peerturn/internal/adapters/codex"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/runtimepolicy"
)

type nativeProber interface {
	Probe(context.Context, string) (codex.Binding, codex.Capabilities, error)
}

func (d *Daemon) nativeRuntimeVersion(status *Status) {
	if runtime, ok := d.native.(interface{ RuntimeVersion() string }); ok {
		status.RuntimeVersion = runtime.RuntimeVersion()
		status.RuntimePolicy.Decision = runtimepolicy.Evaluate(runtimepolicy.Codex, status.RuntimeVersion)
	}
}

// Check before recording an attempted effect, so a failed read-only contract
// leaves pending custody available for recovery without an ambiguous receipt.
func (d *Daemon) checkNativeCoauthor(ctx context.Context) error {
	probe, ok := d.native.(nativeProber)
	if !ok {
		return localipc.NewError("adapter_unavailable", "needs_rebind")
	}
	checkCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancel()
	thread := d.state.Codex.ThreadID
	d.mu.Unlock()
	_, caps, err := probe.Probe(checkCtx, thread)
	d.mu.Lock()
	if err != nil {
		return nativeError(err)
	}
	if !caps.WakeIdle {
		return localipc.NewError("adapter_unavailable", "needs_rebind")
	}
	return nil
}
