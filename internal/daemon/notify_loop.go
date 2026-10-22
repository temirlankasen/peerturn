package daemon

import (
	"context"
	"time"

	"github.com/temirlankasen/peerturn/internal/relayclient"
)

func (d *Daemon) notifyLoop(ctx context.Context) {
	for {
		d.mu.Lock()
		enrolled := d.state.Enrolled
		d.mu.Unlock()
		if enrolled {
			break
		}
		if !waitNotify(ctx, 250*time.Millisecond) {
			return
		}
	}
	opts := relayclient.NotifyOptions{
		Frontier: func(ctx context.Context) (int64, error) {
			frontier, err := d.db.AckFrontier(ctx, d.mailbox())
			if err != nil {
				return 0, err
			}
			return wireFrontier(frontier)
		},
		OnEvent: func(context.Context, relayclient.Event) error {
			d.mu.Lock()
			d.notifyOnline = true
			d.mu.Unlock()
			d.signal()
			return nil
		},
	}
	backoff := time.Second
	for ctx.Err() == nil {
		started := time.Now()
		err := d.relay.Notify(ctx, d.mailbox(), opts)
		d.mu.Lock()
		d.notifyOnline = false
		d.mu.Unlock()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			d.signal()
		}
		// Nonretryable stream errors end one subscription, not daemon notification recovery.
		if time.Since(started) >= 30*time.Second {
			backoff = time.Second
		}
		if !waitNotify(ctx, backoff) {
			return
		}
		backoff = min(30*time.Second, backoff*2)
	}
}

func waitNotify(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
