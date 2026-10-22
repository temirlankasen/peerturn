package codex

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestSocketRPCRejectsMethodsOutsideAllowlist(t *testing.T) {
	closed := make(chan struct{}, 1)
	path, requests := nativeSocketWithHook(t, SupportedVersion, false, nil, func() { closed <- struct{}{} })
	tr, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	closeTransport(t, tr)
	// Finish observing the allowed initialization before checking for new I/O.
	// The server may read its final initialized notification after Open returns.
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("initialization connection did not close")
	}
	for len(requests) > 0 {
		<-requests
	}
	for _, method := range []string{"turn/start", "turn/steer", "thread/inject_items", "initialized", ""} {
		var response any
		if err = tr.rpc.Call(t.Context(), method, struct{}{}, &response); !IsCode(err, "unsupported") {
			t.Fatalf("method %q was not rejected before I/O: %v", method, err)
		}
	}
	if len(requests) != 0 {
		t.Fatal("disallowed method reached control socket")
	}
}

func TestUnixSocketFloodAndServerRequestBeforeQueueResponse(t *testing.T) {
	hook := func(ctx context.Context, conn *websocket.Conn, request socketRequest) bool {
		if request.Method != "thread/queue/add" {
			return true
		}
		for range 1500 {
			if err := conn.Write(ctx, websocket.MessageText, []byte(`{"method":"turn/output","params":{}}`)); err != nil {
				return false
			}
		}
		if err := conn.Write(ctx, websocket.MessageText, []byte(`{"id":"approval-request","method":"item/commandExecution/requestApproval","params":{}}`)); err != nil {
			return false
		}
		readCtx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		_, data, err := conn.Read(readCtx)
		if err != nil {
			return false
		}
		var reply struct {
			ID    string    `json:"id"`
			Error *RPCError `json:"error"`
		}
		if err = json.Unmarshal(data, &reply); err != nil || reply.ID != "approval-request" || reply.Error == nil || reply.Error.Code != -32601 {
			t.Errorf("unsafe server request reply: %s, error %v", data, err)
			return false
		}
		return true
	}
	path, _ := nativeSocketWithHook(t, SupportedVersion, false, hook, nil)
	tr, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	closeTransport(t, tr)
	b, err := tr.Bind(t.Context(), testThread)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := Frame(testMessage(), strings.Repeat("d", 64))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tr.Queue(t.Context(), b, testMessage().MessageID, frame); err != nil {
		t.Fatalf("notification flood or server request lost queue ACK: %v", err)
	}
}

func TestUnixSocketDoesNotRetainIdleSubscriptions(t *testing.T) {
	closed := make(chan struct{}, 16)
	path, _ := nativeSocketWithHook(t, SupportedVersion, false, nil, func() { closed <- struct{}{} })
	tr, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	closeTransport(t, tr)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("initialized socket retained an idle subscription")
	}
	if _, err = tr.Bind(t.Context(), testThread); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case <-closed:
		case <-time.After(time.Second):
			t.Fatal("RPC retained an idle subscription")
		}
	}
}

func TestUnixSocketConcurrentCallHonorsShortDeadline(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	hook := func(ctx context.Context, _ *websocket.Conn, request socketRequest) bool {
		if request.Method != "thread/queue/list" {
			return true
		}
		close(started)
		select {
		case <-release:
			return true
		case <-ctx.Done():
			return false
		}
	}
	path, _ := nativeSocketWithHook(t, SupportedVersion, false, hook, nil)
	tr, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	closeTransport(t, tr)
	slowDone := make(chan error, 1)
	go func() {
		var response any
		slowDone <- tr.rpc.Call(t.Context(), "thread/queue/list", map[string]string{"threadId": testThread}, &response)
	}()
	<-started
	fastCtx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	fastDone := make(chan struct{})
	go func() {
		var response any
		if callErr := tr.rpc.Call(fastCtx, "thread/read", map[string]string{"threadId": testThread}, &response); callErr != nil && fastCtx.Err() == nil {
			t.Errorf("short RPC: %v", callErr)
		}
		close(fastDone)
	}()
	blocked := false
	select {
	case <-fastDone:
	case <-time.After(250 * time.Millisecond):
		blocked = true
	}
	close(release)
	if err = <-slowDone; err != nil {
		t.Fatal(err)
	}
	if blocked {
		<-fastDone
		t.Fatal("short-deadline RPC waited for unrelated network operation")
	}
}
