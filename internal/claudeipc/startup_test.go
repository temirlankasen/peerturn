package claudeipc

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestHelperWaitsForLateDaemonWithoutRegisteringEarly(t *testing.T) {
	dir := currentDirectory(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		time.Sleep(40 * time.Millisecond)
		server, err := localipc.Listen(dir)
		if err != nil {
			done <- err
			return
		}
		err = server.Serve(ctx, func(_ context.Context, request localipc.Request) (any, error) {
			if request.Method != "status" {
				t.Error("startup readiness attempted a binding effect")
			}
			return map[string]string{"state": "active"}, nil
		})
		if closeErr := server.Close(); err == nil {
			err = closeErr
		}
		done <- err
	}()
	client, err := readyDaemonClient(t.Context(), Options{Client: localipc.Client{RuntimeDir: dir}}, func(context.Context) error { return nil })
	cancel()
	if serveErr := <-done; serveErr != nil {
		t.Fatal(serveErr)
	}
	if err != nil || client.RuntimeDir != dir {
		t.Fatalf("late daemon was not discovered: %v", err)
	}
}

func TestHelperStartupRechecksProofAfterSuccessfulStatus(t *testing.T) {
	var lost atomic.Bool
	client := currentDaemon(t, func(context.Context, localipc.Request) (any, error) {
		lost.Store(true)
		return map[string]string{"state": "active"}, nil
	})
	_, err := readyDaemonClient(t.Context(), Options{Client: client}, func(context.Context) error {
		if lost.Load() {
			return rebind()
		}
		return nil
	})
	assertCurrentRebind(t, err)
}

func TestHelperStartupRequiresNativeProofBeforeDiscovery(t *testing.T) {
	options := Options{ResolveClient: func() (localipc.Client, error) {
		t.Fatal("unproven helper attempted daemon discovery")
		return localipc.Client{}, nil
	}}
	_, err := readyDaemonClient(t.Context(), options, func(context.Context) error { return rebind() })
	assertCurrentRebind(t, err)
}

func TestHelperStartupIsBoundedAndDoesNotFollowUnsafeDiscovery(t *testing.T) {
	for _, unsafe := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "unsafe"}[unsafe], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 60*time.Millisecond)
			defer cancel()
			options := Options{Client: localipc.Client{RuntimeDir: currentDirectory(t)}}
			if unsafe {
				options.ResolveClient = func() (localipc.Client, error) {
					return localipc.Client{}, localipc.NewError("unsafe_runtime", "rejected")
				}
			}
			_, err := readyDaemonClient(ctx, options, func(context.Context) error { return nil })
			want := "unavailable"
			if unsafe {
				want = "unsafe_runtime"
			}
			if err == nil || localipc.PublicError(err).Code != want {
				t.Fatalf("startup error=%v want %s", err, want)
			}
		})
	}
}

// After a reboot nobody starts the daemon for a while. A waiting helper asks
// for it once and then slows its probes; it must not hammer the native socket.
func TestHelperAsksForAbsentDaemonOnceAndSlowsDown(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 1300*time.Millisecond)
	defer cancel()
	var probes, guards, starts atomic.Int32
	options := Options{
		Client:      localipc.Client{RuntimeDir: currentDirectory(t)},
		StartDaemon: func() { starts.Add(1) },
		ResolveClient: func() (localipc.Client, error) {
			probes.Add(1)
			return localipc.Client{}, localipc.NewError("unavailable", "unavailable")
		},
	}
	_, err := readyDaemonClient(ctx, options, func(context.Context) error {
		guards.Add(1)
		return nil
	})
	if localipc.PublicError(err).Code != "unavailable" {
		t.Fatalf("wait ended with %v", err)
	}
	if starts.Load() != 1 {
		t.Fatalf("asked for the daemon %d times, want once", starts.Load())
	}
	// 0, 200, 600 ms and the cut at 1.3 s: a fixed 200 ms tick would probe seven times.
	if probes.Load() > 4 || guards.Load() > 4 {
		t.Fatalf("%d probes and %d native checks in 1.3 s", probes.Load(), guards.Load())
	}
}

func TestHelperDoesNotStartDaemonOnTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	var starts atomic.Int32
	options := Options{StartDaemon: func() { starts.Add(1) }, ResolveClient: func() (localipc.Client, error) {
		return localipc.Client{}, localipc.NewError("timeout", "pending")
	}}
	if _, err := readyDaemonClient(ctx, options, func(context.Context) error { return nil }); err == nil {
		t.Fatal("wait succeeded without a daemon")
	}
	// A busy daemon is running; starting a second one would only fail on its lock.
	if starts.Load() != 0 {
		t.Fatal("a slow daemon triggered a start")
	}
}
