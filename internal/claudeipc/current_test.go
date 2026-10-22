package claudeipc

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

const currentTestToken = "native-fixture-secret-value"

func TestBindCurrentRejectsUnsafeNativeSocket(t *testing.T) {
	for _, mode := range []string{"public", "symlink", "regular", "relative", "unclean"} {
		t.Run(mode, func(t *testing.T) {
			dir := currentDirectory(t)
			socket := filepath.Join(dir, "native.sock")
			if mode == "regular" {
				if err := os.WriteFile(socket, nil, 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				currentListener(t, socket)
			}
			switch mode {
			case "public":
				info, err := os.Lstat(socket)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.Chmod(socket, info.Mode().Perm()|0066); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				link := filepath.Join(dir, "link.sock")
				if err := os.Symlink(socket, link); err != nil {
					t.Fatal(err)
				}
				socket = link
			case "relative":
				socket = "native.sock"
			case "unclean":
				socket = dir + "/./native.sock"
			}
			t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", socket)
			t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", currentTestToken)
			_, err := BindCurrent(t.Context(), localipc.Client{})
			assertCurrentRebind(t, err)
		})
	}
}

func TestBindCurrentFullNativeHandshake(t *testing.T) {
	native := currentListener(t, filepath.Join(currentDirectory(t), "native.sock"))
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", native.Addr().String())
	t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", currentTestToken)
	nonce := strings.Repeat("a", 64)
	var authenticated atomic.Bool
	frames := make(chan error, 1)
	go func() {
		conn, err := native.AcceptUnix()
		if err != nil {
			frames <- err
			return
		}
		defer closeNative(conn)
		decoder := json.NewDecoder(conn)
		var auth struct{ Type, Token string }
		var frame struct {
			Type, Priority string
			Message        struct{ Role, Content string }
		}
		if err = decoder.Decode(&auth); err == nil {
			err = decoder.Decode(&frame)
		}
		if err == nil && (auth.Type != "auth" || auth.Token != currentTestToken || frame.Type != "user" || frame.Priority != "next" || frame.Message.Role != "user" || frame.Message.Content != "<peerturn-bind>"+nonce+"</peerturn-bind>") {
			err = errors.New("unexpected native fixture frame")
		}
		authenticated.Store(err == nil)
		frames <- err
	}()
	client := currentDaemon(t, func(_ context.Context, r localipc.Request) (any, error) {
		checkCurrentRequest(t, r)
		switch r.Method {
		case "adapter.challenge":
			return map[string]string{"nonce": nonce}, nil
		case "adapter.resolve":
			if r.Nonce != nonce || r.NativeSocket != native.Addr().String() {
				return nil, rebind()
			}
			if authenticated.Load() {
				return map[string]string{"session": "native-fixture-session"}, nil
			}
		}
		return nil, rebind()
	})
	session, err := BindCurrent(t.Context(), client)
	if err != nil || session != "native-fixture-session" {
		t.Fatalf("native binding failed: session=%q error=%v", session, err)
	}
	if err = <-frames; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(session, currentTestToken) {
		t.Fatal("native token reached binding output")
	}
}

func TestBindCurrentSanitizesChallengeFailure(t *testing.T) {
	native := currentListener(t, filepath.Join(currentDirectory(t), "native.sock"))
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", native.Addr().String())
	t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", currentTestToken)
	client := currentDaemon(t, func(context.Context, localipc.Request) (any, error) {
		return nil, &localipc.Error{Code: "needs_rebind", Message: currentTestToken, State: "unbound"}
	})
	_, err := BindCurrent(t.Context(), client)
	assertCurrentRebind(t, err)
	public, marshalErr := json.Marshal(localipc.PublicError(err))
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if strings.Contains(err.Error(), currentTestToken) || strings.Contains(string(public), currentTestToken) {
		t.Fatal("native token reached binding error")
	}
}

func TestBindCurrentResolveTimeout(t *testing.T) {
	native := currentListener(t, filepath.Join(currentDirectory(t), "native.sock"))
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", native.Addr().String())
	t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", currentTestToken)
	var resolves atomic.Int32
	client := currentDaemon(t, func(_ context.Context, r localipc.Request) (any, error) {
		checkCurrentRequest(t, r)
		if r.Method == "adapter.challenge" {
			return map[string]string{"nonce": strings.Repeat("b", 64)}, nil
		}
		resolves.Add(1)
		return nil, rebind()
	})
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := BindCurrent(ctx, client)
	assertCurrentRebind(t, err)
	if resolves.Load() == 0 || time.Since(started) > time.Second {
		t.Fatal("resolve was not bounded by the caller deadline")
	}
}

func currentDirectory(t *testing.T) string {
	t.Helper()
	dir, err := shortRuntimeDirectory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if removeErr := os.RemoveAll(dir); removeErr != nil {
			t.Error(removeErr)
		}
	})
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func currentListener(t *testing.T, path string) *net.UnixListener {
	t.Helper()
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := listener.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	if err = os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	return listener
}

func currentDaemon(t *testing.T, handler localipc.Handler) localipc.Client {
	t.Helper()
	dir := currentDirectory(t)
	s, err := localipc.Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, handler) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return localipc.Client{RuntimeDir: dir}
}

func checkCurrentRequest(t *testing.T, r localipc.Request) {
	t.Helper()
	data, err := json.Marshal(r)
	if err != nil {
		t.Error(err)
	}
	if strings.Contains(string(data), currentTestToken) {
		t.Error("native token escaped into daemon request")
	}
}

func assertCurrentRebind(t *testing.T, err error) {
	t.Helper()
	if err == nil || localipc.PublicError(err).Code != "needs_rebind" {
		t.Fatalf("expected needs_rebind, got %v", err)
	}
}
