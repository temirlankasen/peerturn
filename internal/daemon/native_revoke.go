package daemon

import (
	"context"
	"time"
)

func (d *Daemon) deleteNativeGrant(ctx context.Context, grantID string) error {
	if d.state.Codex == nil {
		return nil
	}
	queues, err := d.db.NativeQueues(ctx, grantID)
	if err != nil {
		return err
	}
	deleteCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	runtime, err := d.runtimeLocked(deleteCtx)
	if err != nil {
		return nil
	} // Local revoke remains authoritative on native failure.
	binding := *d.state.Codex
	for _, q := range queues {
		deleted := len(q.ItemIDs) > 0
		for _, id := range q.ItemIDs {
			ok, deleteErr := networkCall(deleteCtx, d, func() (bool, error) { return runtime.Delete(deleteCtx, binding, id) })
			if deleteErr != nil || !ok {
				deleted = false
			}
		}
		if deleted {
			if err = d.db.NativeDequeued(ctx, q.MessageID); err != nil {
				return err
			}
		}
	}
	return nil
}
