package localipc

import (
	"context"
	"encoding/binary"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPrivateSocketAndIndependentLock(t *testing.T) {
	dir := runtimeFixture(t)
	server, err := Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Error(err)
		}
	})
	for path, mode := range map[string]os.FileMode{dir: 0o700, filepath.Join(dir, SocketName): 0o600} {
		info, statErr := os.Stat(path)
		if statErr != nil || info.Mode().Perm() != mode {
			t.Fatalf("private path mode: %v %v", info, statErr)
		}
	}
	if err := os.Remove(filepath.Join(dir, SocketName)); err != nil {
		t.Fatal(err)
	}
	if duplicate, listenErr := Listen(dir); listenErr == nil {
		if closeErr := duplicate.Close(); closeErr != nil {
			t.Error(closeErr)
		}
		t.Fatal("second daemon admitted after socket removal")
	}
}

func TestRejectUnsafeRuntimeAndLock(t *testing.T) {
	for _, kind := range []string{"directory", "symlink", "lock"} {
		t.Run(kind, func(t *testing.T) {
			dir := runtimeFixture(t)
			switch kind {
			case "directory":
				info, err := os.Stat(dir)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(dir, info.Mode().Perm()|0o055); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				link := filepath.Join(t.TempDir(), "link")
				if err := os.Symlink(dir, link); err != nil {
					t.Fatal(err)
				}
				dir = link
			case "lock":
				if err := os.Symlink(filepath.Join(t.TempDir(), "target"), filepath.Join(dir, "daemon.lock")); err != nil {
					t.Fatal(err)
				}
			}
			if server, err := Listen(dir); err == nil {
				if closeErr := server.Close(); closeErr != nil {
					t.Error(closeErr)
				}
				t.Fatal("unsafe runtime accepted")
			}
		})
	}
}

func TestCallAndMalformedFrames(t *testing.T) {
	dir := runtimeFixture(t)
	server, err := Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx, func(_ context.Context, request Request) (any, error) {
			return map[string]string{"session": request.Session}, nil
		})
	}()
	t.Cleanup(func() {
		cancel()
		if serveErr := <-done; serveErr != nil {
			t.Error(serveErr)
		}
		if closeErr := server.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	result, err := (Client{RuntimeDir: dir}).Call(t.Context(), Request{Method: "status", Session: "explicit"})
	if err != nil || !strings.Contains(string(result), "explicit") {
		t.Fatalf("call = %s, %v", result, err)
	}
	for _, payload := range []string{`{"method":"status","unknown":true}`, `{"method":"status","method":"stop"}`, `{"Method":"stop"}`} {
		conn, dialErr := net.Dial("unix", filepath.Join(dir, SocketName))
		if dialErr != nil {
			t.Fatal(dialErr)
		}
		if deadlineErr := conn.SetDeadline(time.Now().Add(time.Second)); deadlineErr != nil {
			t.Fatal(deadlineErr)
		}
		if writeErr := writeFrame(conn, []byte(payload)); writeErr != nil {
			t.Fatal(writeErr)
		}
		data, readErr := readFrame(conn)
		if closeErr := conn.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		if readErr != nil || !strings.Contains(string(data), "invalid_encoding") {
			t.Fatalf("malformed = %s, %v", data, readErr)
		}
	}
	var oversized [4]byte
	binary.BigEndian.PutUint32(oversized[:], MaxFrame+1)
	if _, err := readFrame(strings.NewReader(string(oversized[:]))); err == nil {
		t.Fatal("oversized frame accepted")
	}
}

func TestPeerUIDCheckRejectsDifferentUID(t *testing.T) {
	if os.Geteuid() == 0 {
		testRealOtherUIDRejected(t)
		return
	}
	dir := runtimeFixture(t)
	server, err := Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := server.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	done := make(chan error, 1)
	go func() {
		conn, acceptErr := server.listener.AcceptUnix()
		if acceptErr != nil {
			done <- acceptErr
			return
		}
		defer func() {
			if closeErr := conn.Close(); closeErr != nil {
				done <- closeErr
			}
		}()
		if verifyErr := verifyUID(conn, os.Getuid()); verifyErr != nil {
			done <- verifyErr
			return
		}
		done <- verifyUID(conn, os.Getuid()+1)
	}()
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: filepath.Join(dir, SocketName), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyUID(conn, os.Getuid()); err != nil {
		t.Fatal(err)
	}
	if err := verifyUID(conn, os.Getuid()+1); PublicError(err).Code != "uid_mismatch" {
		t.Fatalf("dial-side peer uid mismatch not rejected: %v", err)
	}
	if err := <-done; err == nil {
		t.Error("different uid accepted")
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
}

func runtimeFixture(t *testing.T) string {
	t.Helper()
	// macOS's default TMPDIR exceeds the Unix socket address limit in tests.
	t.Setenv("TMPDIR", "/tmp")
	dir := filepath.Join(t.TempDir(), "r")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}
