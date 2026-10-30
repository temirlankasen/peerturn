package claudeipc

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestBindCurrentWhenReadyWaitsOnlyBeforeNativeWrite(t *testing.T) {
	native := currentListener(t, filepath.Join(currentDirectory(t), "native.sock"))
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", native.Addr().String())
	t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", currentTestToken)
	nonce := strings.Repeat("b", 64)
	var challenges atomic.Int32
	var received atomic.Bool
	frames := make(chan error, 1)
	go func() {
		conn, err := native.AcceptUnix()
		if err != nil {
			frames <- err
			return
		}
		defer closeNative(conn)
		decoder := json.NewDecoder(conn)
		for range 2 {
			var frame map[string]any
			if err = decoder.Decode(&frame); err != nil {
				frames <- err
				return
			}
		}
		received.Store(true)
		frames <- nil
	}()
	client := currentDaemon(t, func(_ context.Context, r localipc.Request) (any, error) {
		checkCurrentRequest(t, r)
		if r.Method == "adapter.challenge" {
			if challenges.Add(1) == 1 {
				return nil, rebind()
			}
			return map[string]string{"nonce": nonce}, nil
		}
		if r.Method == "adapter.resolve" && received.Load() {
			return map[string]string{"session": "chosen"}, nil
		}
		return nil, rebind()
	})
	session, err := BindCurrentWhenReady(t.Context(), client)
	if err != nil || session != "chosen" || challenges.Load() != 2 {
		t.Fatalf("helper readiness did not recover: session=%q challenges=%d err=%v", session, challenges.Load(), err)
	}
	if err = <-frames; err != nil {
		t.Fatal(err)
	}
}

func TestBindCurrentWhenReadyTimeoutDoesNotWriteNativeFrames(t *testing.T) {
	native := currentListener(t, filepath.Join(currentDirectory(t), "native.sock"))
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", native.Addr().String())
	t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", currentTestToken)
	var challenges atomic.Int32
	client := currentDaemon(t, func(_ context.Context, r localipc.Request) (any, error) {
		checkCurrentRequest(t, r)
		challenges.Add(1)
		return nil, rebind()
	})
	ctx, cancel := context.WithTimeout(t.Context(), 80*time.Millisecond)
	defer cancel()
	_, err := BindCurrentWhenReady(ctx, client)
	assertCurrentRebind(t, err)
	if challenges.Load() != 1 {
		t.Fatalf("bounded wait made %d calls", challenges.Load())
	}
	if err = native.SetDeadline(time.Now().Add(30 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	conn, acceptErr := native.AcceptUnix()
	if conn != nil {
		closeNative(conn)
		t.Fatal("readiness waiting opened the native prompt route")
	}
	if acceptErr == nil {
		t.Fatal("native route unexpectedly accepted a connection")
	}
}
