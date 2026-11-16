package claudeipc

import (
	"context"
	"time"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

// Unregistered waiting belongs to the exact native parent's lifetime, not a
// deadline that could expire immediately before connect. Every probe is bounded;
// loss of parent proof or cancellation ends it without native prompt frames.
// Probes slow down to maxReadyDelay: a daemon that stays away (after a reboot)
// must not cost each waiting session several native socket checks a second.
func readyDaemonClient(ctx context.Context, options Options, guard func(context.Context) error) (localipc.Client, error) {
	readyCtx := ctx
	delay := firstReadyDelay
	asked := false
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
		if !asked && options.StartDaemon != nil && code != "timeout" {
			// One request per helper: a daemon that cannot start is reported
			// by connect or daemon start, where the owner can read why.
			asked = true
			options.StartDaemon()
		}
		timer := time.NewTimer(delay)
		select {
		case <-readyCtx.Done():
			timer.Stop()
			return localipc.Client{}, localipc.NewError("unavailable", "unavailable")
		case <-timer.C:
		}
		delay = min(2*delay, maxReadyDelay)
	}
}

const (
	firstReadyDelay = 200 * time.Millisecond
	// connect waits 20 seconds for the helper, so two seconds never miss it.
	maxReadyDelay = 2 * time.Second
)
