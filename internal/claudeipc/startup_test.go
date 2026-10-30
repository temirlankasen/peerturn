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
