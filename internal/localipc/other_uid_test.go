package localipc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func testRealOtherUIDRejected(t *testing.T) {
	t.Helper()
	// A root CI process can exercise genuine cross-uid kernel credentials. The
	// isolated fixture is private to that uid, which can therefore dial it;
	// production's owner-private directory would reject it before the uid guard.
	t.Setenv("TMPDIR", "/tmp")
	dir := t.TempDir()
	for _, path := range []string{filepath.Dir(dir), dir} {
		if err := os.Chown(path, 65534, 65534); err != nil {
			t.Fatal(err)
		}
	}
	executable := copyUIDHelper(t, dir)
	path := filepath.Join(dir, "peer.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := listener.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	if err = os.Chown(path, 65534, 65534); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command, err := otherUIDFixtureProcess(ctx, executable)
	if err != nil {
		t.Fatal(err)
	}
	command.Env = append(os.Environ(), "PEERTURN_OTHER_UID_SOCKET="+path)
	command.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65534, Gid: 65534, NoSetGroups: true}}
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	if err = listener.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	conn, err := listener.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyUID(conn, 65534); err != nil {
		t.Fatalf("helper did not connect with genuine other-uid credentials: %v", err)
	}
	if err := verifyUID(conn, os.Getuid()); PublicError(err).Code != "uid_mismatch" {
		t.Fatalf("actual other uid accepted: %v", err)
	}
	called := false
	(&Server{}).handle(ctx, conn, func(context.Context, Request) (any, error) {
		called = true
		return nil, nil
	})
	if called {
		t.Fatal("other uid reached daemon dispatch")
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("other-uid helper: %v: %s", err, output.String())
	}
}

func otherUIDFixtureProcess(ctx context.Context, executable string) (*exec.Cmd, error) {
	// Only the current test image's private copy is executable, by absolute path.
	if !filepath.IsAbs(executable) || filepath.Clean(executable) != executable {
		return nil, NewError("invalid_encoding", "rejected")
	}
	return exec.CommandContext(ctx, executable, "-test.run=^TestOtherUIDClientHelper$"), nil
}

func copyUIDHelper(t *testing.T, dir string) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(filepath.Dir(executable))
	if err != nil {
		t.Fatal(err)
	}
	image, err := root.Open(filepath.Base(executable))
	if closeErr := root.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	root, err = os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	copy, err := root.OpenFile("uid-helper", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if closeErr := root.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(copy, image)
	if err = errors.Join(copyErr, copy.Chown(65534, 65534), copy.Close(), image.Close()); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "uid-helper")
}

func TestOtherUIDClientHelper(t *testing.T) {
	path := os.Getenv("PEERTURN_OTHER_UID_SOCKET")
	if path == "" {
		return
	}
	if os.Geteuid() != 65534 {
		t.Fatal("helper did not run with the requested uid")
	}
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := writeFrame(conn, []byte(`{"method":"stop"}`)); err != nil && !errors.Is(err, syscall.EPIPE) {
		t.Fatal(err)
	}
	var data [1]byte
	if _, err := conn.Read(data[:]); !errors.Is(err, io.EOF) && !errors.Is(err, syscall.ECONNRESET) {
		t.Fatalf("other uid received a daemon response: %v", err)
	}
}
