package claudeipc

import (
	"context"
	"encoding/json"
	"time"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

// BindCurrentWhenReady waits for the helper only before writing native frames.
// Once a challenge can reach the model, an uncertain outcome is never retried.
func BindCurrentWhenReady(ctx context.Context, client localipc.Client) (string, error) {
	evidence, err := bindCurrent(ctx, client, true)
	return evidence.Session, err
}

func bindingInDoubt() error {
	problem := localipc.NewError("in_doubt", "in_doubt")
	problem.Message = "A native binding prompt may have reached the host without confirmation. Inspect current-session status and the native turn outcome; do not automatically repeat connect or bind."
	return problem
}

func currentChallenge(ctx context.Context, client localipc.Client, socket string, wait bool) (json.RawMessage, error) {
	if !wait {
		return client.Call(ctx, localipc.Request{Method: "adapter.challenge", NativeSocket: socket})
	}
	readyCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		raw, err := client.Call(readyCtx, localipc.Request{Method: "adapter.challenge", NativeSocket: socket})
		if err == nil || localipc.PublicError(err).Code != "needs_rebind" {
			return raw, err
		}
		select {
		case <-readyCtx.Done():
			return nil, rebind()
		case <-ticker.C:
		}
	}
}
