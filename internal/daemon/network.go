package daemon

import (
	"context"
	"time"
)

type operationLockKey struct{}

// Mutations remain serialized by opMu while transport runs without mu. Owner
// inspection and stop can then acquire mu even when the relay never responds.
func networkCall[T any](ctx context.Context, d *Daemon, call func() (T, error)) (T, error) {
	if ctx.Value(operationLockKey{}) == d {
		d.mu.Unlock()
		defer d.mu.Lock()
	}
	return call()
}

func networkDo(ctx context.Context, d *Daemon, call func() error) error {
	_, err := networkCall(ctx, d, func() (struct{}, error) { return struct{}{}, call() })
	return err
}

func needsOperationLock(method string) bool {
	switch method {
	case "status", "doctor", "stop", "peers", "receipt", "grants", "hello", "bye", "codex_inform", "codex_peer_started":
		return false
	default:
		return true
	}
}

func (d *Daemon) lockOperation(ctx context.Context) error {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.opMu.TryLock() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
