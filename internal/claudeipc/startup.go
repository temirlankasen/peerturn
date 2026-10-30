package claudeipc

import (
	"context"
	"time"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

// Unregistered waiting belongs to the exact native parent's lifetime, not a
// deadline that could expire immediately before connect. Every probe is bounded;
// loss of parent proof or cancellation ends it without native prompt frames.
func readyDaemonClient(ctx context.Context, options Options, guard func(context.Context) error) (localipc.Client, error) {
	readyCtx := ctx
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if readyCtx.Err() != nil {
			return localipc.Client{}, localipc.NewError("unavailable", "unavailable")
		}
		if err := guard(readyCtx); err != nil {
			return localipc.Client{}, err
		}
		client := options.Client
		var err error
		if options.ResolveClient != nil {
			client, err = options.ResolveClient()
		}
		if err == nil {
			probeCtx, cancel := context.WithTimeout(readyCtx, time.Second)
			_, err = client.Call(probeCtx, localipc.Request{Method: "status"})
			cancel()
		}
		if err == nil {
			if err = guard(readyCtx); err != nil {
				return localipc.Client{}, err
			}
			return client, nil
		}
		code := localipc.PublicError(err).Code
		if readyCtx.Err() == nil && code != "unavailable" && code != "runtime_unavailable" && code != "timeout" {
			return localipc.Client{}, err
		}
		select {
		case <-readyCtx.Done():
			return localipc.Client{}, localipc.NewError("unavailable", "unavailable")
		case <-ticker.C:
		}
	}
}
