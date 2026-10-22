package daemon

import (
	"context"
	"time"

	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/store"
)

// admitAskInput serializes the final authority check and the single peer-frame
// write with revocation, helper replacement, unbinding and owner cancellation.
func (d *Daemon) admitAskInput(ctx context.Context, run store.AskRun, instance, native string, submit func() error) error {
	d.opMu.Lock()
	d.mu.Lock()
	err := d.checkAskInput(ctx, run, instance, native, submit)
	d.mu.Unlock()
	d.opMu.Unlock()
	if err != nil {
		return err
	}
	// A slow or locked keyring cannot hold the owner operation fence. Repeat
	// every cheap authority check after this fresh load, before the single write.
	if err = d.checkAuthorityKey(ctx); err != nil {
		return err
	}
	d.opMu.Lock()
	defer d.opMu.Unlock()
	d.mu.Lock()
	defer d.mu.Unlock()
	if err = d.checkAskInput(ctx, run, instance, native, submit); err != nil {
		return err
	}
	return submit()
}

func (d *Daemon) checkAskInput(ctx context.Context, run store.AskRun, instance, native string, submit func() error) error {
	if d.persistenceFailed || native == "" || submit == nil {
		return localipc.NewError("responder_unavailable", "protected_path_policy_failed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !d.claudeBinding.Ready(time.Now()) || d.claudeBinding.Instance != instance {
		return localipc.NewError("needs_rebind", "needs_rebind")
	}
	if err := d.refreshPeerRevocations(ctx); err != nil {
		return err
	}
	if err := d.db.CheckAskAuthority(ctx, run.RunID, unixNow()); err != nil {
		return err
	}
	return nil
}
