package daemon

import (
	"context"

	"github.com/temirlankasen/peerturn/internal/adapters/codex"
	"github.com/temirlankasen/peerturn/internal/localipc"
)

func (d *Daemon) checkOwnerTurn(ctx context.Context, r localipc.Request) error {
	if err := d.hookBinding(r); err != nil {
		return err
	}
	checkCtx, cancel := context.WithTimeout(ctx, codex.OwnerTurnTimeout)
	defer cancel()
	runtime, err := d.runtimeLocked(checkCtx)
	if err != nil {
		return err
	}
	verifier, ok := runtime.(interface {
		ValidateOwnerTurn(context.Context, codex.Binding, string) error
	})
	if !ok {
		return localipc.NewError("adapter_unavailable", "needs_rebind")
	}
	binding := *d.state.Codex
	d.mu.Unlock()
	err = verifier.ValidateOwnerTurn(checkCtx, binding, r.MessageID)
	d.mu.Lock()
	if err != nil {
		return nativeError(err)
	}
	if err = checkCtx.Err(); err != nil {
		return err
	}
	if d.persistenceFailed || d.state.Codex == nil || *d.state.Codex != binding {
		return localipc.NewError("needs_rebind", "needs_rebind")
	}
	return nil
}
