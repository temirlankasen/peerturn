package localipc

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShutdownHoldsLockUntilHandlersFinish(t *testing.T) {
	dir := runtimeFixture(t)
	server, err := Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	started, stopping, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx, func(ctx context.Context, _ Request) (any, error) {
			close(started)
			<-ctx.Done()
			close(stopping)
			<-release
			return nil, ctx.Err()
		})
	}()
	clientDone := make(chan error, 1)
	go func() {
		_, err := (Client{RuntimeDir: dir}).Call(t.Context(), Request{Method: "status"})
		clientDone <- err
	}()
	<-started
	cancel()
	<-stopping
	duplicate, duplicateErr := Listen(dir)
	if duplicateErr == nil {
		if err := duplicate.Close(); err != nil {
			t.Error(err)
		}
		t.Error("shutdown released daemon lock before the active handler completed")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if replacement, err := Listen(dir); err == nil {
		if closeErr := replacement.Close(); closeErr != nil {
			t.Error(closeErr)
		}
		t.Error("Serve released the lifetime lock before explicit Close")
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-clientDone; err == nil {
		t.Fatal("canceled call succeeded")
	}
}

func TestClientCancellationReachesHandler(t *testing.T) {
	dir := runtimeFixture(t)
	server, err := Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, shutdown := context.WithCancel(t.Context())
	done := make(chan error, 1)
	started, canceled := make(chan struct{}), make(chan struct{})
	go func() {
		done <- server.Serve(ctx, func(ctx context.Context, _ Request) (any, error) {
			close(started)
			<-ctx.Done()
			close(canceled)
			return nil, ctx.Err()
		})
	}()
	t.Cleanup(func() {
		shutdown()
		if serveErr := <-done; serveErr != nil {
			t.Error(serveErr)
		}
		if closeErr := server.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	callCtx, cancel := context.WithCancel(t.Context())
	callDone := make(chan error, 1)
	go func() { _, err := (Client{RuntimeDir: dir}).Call(callCtx, Request{Method: "status"}); callDone <- err }()
	<-started
	cancel()
	if err := <-callDone; err == nil {
		t.Fatal("canceled call succeeded")
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("client disconnect did not cancel handler")
	}
}

func TestStaleSocketRecoveredOnlyWithLock(t *testing.T) {
	dir := runtimeFixture(t)
	path := filepath.Join(dir, SocketName)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	if err = os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	server, err := Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket remains: %v", err)
	}
}

func TestOversizedHandlerResultHasTypedFailure(t *testing.T) {
	dir := runtimeFixture(t)
	server, err := Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx, func(context.Context, Request) (any, error) { return strings.Repeat("a", MaxFrame), nil })
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
	_, err = (Client{RuntimeDir: dir}).Call(t.Context(), Request{Method: "inbox"})
	if PublicError(err).Code != "too_large" {
		t.Fatalf("oversized result = %v", err)
	}
}
