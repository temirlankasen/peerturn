package claudeipc

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

// After a daemon restart the helper's registration is gone. The helper must
// register again and keep serving the mod, so the owner never sees
// needs_rebind or has to reload the session.
func TestBridgeReopensAfterDaemonRestart(t *testing.T) {
	var opens atomic.Int32
	var live atomic.Value
	live.Store("gone")
	b := testBridge(t, func(_ context.Context, r localipc.Request) (any, error) {
		switch r.Method {
		case "adapter.open":
			if r.NativeSession == "" {
				return nil, rebind()
			}
			instance := fmt.Sprintf("instance-%d", opens.Add(1))
			live.Store(instance)
			return map[string]string{"instance": instance, "pinned": "false"}, nil
		case "adapter.poll":
			if current, ok := live.Load().(string); !ok || r.Instance != current {
				return nil, rebind()
			}
			return map[string]string{"type": "status"}, nil
		}
		return nil, rebind()
	})
	b.options.NativeSession = "0123abcd-0123-0123-0123-0123456789ab"
	closed := make(chan struct{}, 1)
	b.shutdown = func() { closed <- struct{}{} }
	w := bridgeRequest(b, http.MethodGet, "/poll", "")
	if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "needs_rebind") {
		t.Fatalf("stale registration surfaced to the mod: %d %s", w.Code, w.Body.String())
	}
	if w = bridgeRequest(b, http.MethodGet, "/poll", ""); w.Code != http.StatusOK {
		t.Fatalf("poll after reopen: %d %s", w.Code, w.Body.String())
	}
	select {
	case <-closed:
		t.Fatal("helper stopped although it reopened")
	case <-time.After(300 * time.Millisecond):
	}
	if opens.Load() != 1 {
		t.Fatalf("registered %d times, want once", opens.Load())
	}
}

// While the daemon is away or still holds the old lease, the helper waits
// instead of dying: the mod keeps polling and the next attempt registers.
func TestBridgeKeepsServingWhileDaemonIsAway(t *testing.T) {
	for _, code := range []string{"unavailable", "already_running"} {
		t.Run(code, func(t *testing.T) {
			b := testBridge(t, func(_ context.Context, r localipc.Request) (any, error) {
				if r.Method == "adapter.open" {
					return nil, localipc.NewError(code, "unavailable")
				}
				return nil, rebind()
			})
			b.options.NativeSession = "0123abcd-0123-0123-0123-0123456789ab"
			closed := make(chan struct{}, 1)
			b.shutdown = func() { closed <- struct{}{} }
			if w := bridgeRequest(b, http.MethodGet, "/poll", ""); w.Code != http.StatusServiceUnavailable {
				t.Fatalf("got %d %s", w.Code, w.Body.String())
			}
			select {
			case <-closed:
				t.Fatal("helper stopped during a daemon outage")
			case <-time.After(200 * time.Millisecond):
			}
		})
	}
}

// A refused registration is final for this helper: another session owns the
// daemon, or the native parent changed. Then it stops as it always did.
func TestBridgeStopsWhenReopenIsRefused(t *testing.T) {
	b := testBridge(t, func(context.Context, localipc.Request) (any, error) { return nil, rebind() })
	closed := make(chan struct{}, 1)
	b.shutdown = func() { closed <- struct{}{} }
	if w := bridgeRequest(b, http.MethodGet, "/poll", ""); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "needs_rebind") {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("refused helper kept running")
	}
}

func TestOpenParsesPinned(t *testing.T) {
	b := testBridge(t, func(_ context.Context, r localipc.Request) (any, error) {
		return map[string]string{"instance": "i", "pinned": "true"}, nil
	})
	reg, err := b.open(t.Context())
	if err != nil || !reg.pinned || reg.instance != "i" {
		t.Fatalf("registration %+v: %v", reg, err)
	}
}
