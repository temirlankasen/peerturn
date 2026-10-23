package daemon

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

// The platform-derived socket path must fit Unix socket length limits.
func shortRecoveryRuntime() (string, error) { return os.MkdirTemp("/tmp", "ph1-") }

func TestRuntimePointerDaemonRecoversDeletedTarget(t *testing.T) {
	state, stale, _ := runtimePointerFixture(t)
	if err := os.Remove(stale); err != nil {
		t.Fatal(err)
	}
	if _, err := DefaultRuntimeDir(); localipc.PublicError(err).Code != "unsafe_runtime" {
		t.Fatal("client must reject the missing runtime target")
	}
	base, err := shortRecoveryRuntime()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if removeErr := os.RemoveAll(base); removeErr != nil {
			t.Error(removeErr)
		}
	})
	base, err = filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(base, "peerturn-"+strconv.Itoa(os.Geteuid()))
	if runtime.GOOS == "darwin" {
		t.Setenv("TMPDIR", base)
	} else {
		t.Setenv("XDG_RUNTIME_DIR", base)
		want = filepath.Join(base, "peerturn", strconv.Itoa(os.Geteuid()))
	}
	admin := routingKey(t)
	relay := newRelayFixture(t, admin)
	if _, err = Initialize(t.Context(), InitOptions{StateDir: state, RelayURL: relay.server.URL,
		SessionLabel: "recovery", FilePermissions: true, RoutingKey: admin}); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "darwin" {
		t.Setenv("TMPDIR", base)
	}
	root, err := os.OpenRoot(state)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, Options{StateDir: state, HTTPClient: relay.server.Client()}) }()
	t.Cleanup(func() {
		cancel()
		if runErr := <-done; runErr != nil {
			t.Error(runErr)
		}
	})
	eventually(t, func() bool {
		select {
		case runErr := <-done:
			done <- runErr
			t.Fatalf("daemon self-locked on stale pointer: %v", runErr)
		default:
		}
		raw, readErr := root.ReadFile(runtimePointerName)
		return readErr == nil && string(raw) == want+"\n"
	})
	t.Setenv("XDG_RUNTIME_DIR", "")
	got, err := DefaultRuntimeDir()
	if err != nil || got != want {
		t.Fatalf("client did not discover republished runtime: %q %v", got, err)
	}
	if _, err = (localipc.Client{RuntimeDir: got}).Call(t.Context(), localipc.Request{Method: "status", Session: "recovery"}); err != nil {
		t.Fatal(err)
	}
}
