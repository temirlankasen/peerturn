package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestClaudeCurrentRejectsSameUIDOutsideNativeTree(t *testing.T) {
	d, _ := pairedStores(t)
	bindClaude(t, d)
	dir := currentIPCDirectory(t)
	server, err := localipc.Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, d.Dispatch) }()
	t.Cleanup(func() {
		cancel()
		if serveErr := <-done; serveErr != nil {
			t.Error(serveErr)
		}
		if closeErr := server.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	_, err = (localipc.Client{RuntimeDir: dir}).Call(t.Context(), localipc.Request{Method: "adapter.current", NativeSocket: d.claudeBinding.Socket})
	if err == nil || localipc.PublicError(err).Code != "needs_rebind" {
		t.Fatal("same-uid caller without native ancestry obtained the binding epoch", err)
	}
}

func currentIPCDirectory(t *testing.T) string {
	t.Helper()
	dir, err := shortCurrentIPCDirectory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	return dir
}

// Native Unix sockets have a short path limit independent of the test name.
func shortCurrentIPCDirectory() (string, error) { return os.MkdirTemp("/tmp", "pt-current-") }

// The child runs only IPC against the disposable test daemon, never an agent.
func TestClaudeCurrentIPCChild(t *testing.T) {
	dir := os.Getenv("PEERTURN_TEST_CURRENT_RUNTIME")
	if dir == "" {
		return
	}
	raw, err := (localipc.Client{RuntimeDir: dir}).Call(t.Context(), localipc.Request{
		Method: "adapter.current", NativeSocket: os.Getenv("PEERTURN_TEST_CURRENT_SOCKET"),
	})
	var result map[string]string
	if err != nil || json.Unmarshal(raw, &result) != nil || result["binding_epoch"] == "" {
		t.Fatal("native descendant did not resolve the confirmed epoch", string(raw), err)
	}
}

func TestClaudeCurrentAdmitsOnlyStableNativeDescendant(t *testing.T) {
	d, _ := pairedStores(t)
	dir := currentIPCDirectory(t)
	path := filepath.Join(dir, "native.sock")
	native, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, acceptErr := native.AcceptUnix()
			if acceptErr != nil {
				return
			}
			// Closing before the credential-only client races Darwin peer credentials.
			_, readErr := io.Copy(io.Discard, conn)
			if closeErr := conn.Close(); readErr != nil || closeErr != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		if closeErr := native.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	r := localipc.Request{Method: "adapter.open", NativeSession: "11111111-1111-4111-8111-111111111111", NativeSocket: path, RuntimeVersion: "2.1.308", Provider: "gateway"}
	value, err := d.Dispatch(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	opened, ok := value.(map[string]string)
	if !ok {
		t.Fatal("invalid open result", value)
	}
	instance := opened["instance"]
	value, err = d.Dispatch(t.Context(), localipc.Request{Method: "adapter.challenge", NativeSocket: path})
	if err != nil {
		t.Fatal(err)
	}
	challenged, ok := value.(map[string]string)
	if !ok {
		t.Fatal("invalid challenge result", value)
	}
	_, err = d.Dispatch(t.Context(), localipc.Request{Method: "adapter.confirm", NativeSession: r.NativeSession, Instance: instance, Nonce: challenged["nonce"]})
	if err != nil {
		t.Fatal(err)
	}
	if d.claudeCurrentProof == nil {
		t.Fatal("native fixture lost process proof before descendant admission")
	}
	server, err := localipc.Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, d.Dispatch) }()
	t.Cleanup(func() {
		cancel()
		if serveErr := <-done; serveErr != nil {
			t.Error(serveErr)
		}
		if closeErr := server.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	childCtx, cancelChild := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancelChild()
	child, err := currentFixtureProcess(childCtx, executable)
	if err != nil {
		t.Fatal(err)
	}
	child.Env = append(os.Environ(), "PEERTURN_TEST_CURRENT_RUNTIME="+dir, "PEERTURN_TEST_CURRENT_SOCKET="+path)
	if output, childErr := child.CombinedOutput(); childErr != nil {
		t.Fatal("descendant IPC failed", string(output), childErr)
	}
	// The socket owner itself is not its own descendant.
	_, err = (localipc.Client{RuntimeDir: dir}).Call(t.Context(), localipc.Request{Method: "adapter.current", NativeSocket: path})
	if err == nil || localipc.PublicError(err).Code != "needs_rebind" {
		t.Fatal("native self caller bypassed descendant check", err)
	}
}

func currentFixtureProcess(ctx context.Context, executable string) (*exec.Cmd, error) {
	// Only the clean absolute os.Executable test image can act as this child.
	if !filepath.IsAbs(executable) || filepath.Clean(executable) != executable {
		return nil, localipc.NewError("invalid_encoding", "rejected")
	}
	return exec.CommandContext(ctx, executable, "-test.run=^TestClaudeCurrentIPCChild$"), nil
}
