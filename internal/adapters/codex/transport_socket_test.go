package codex

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type socketRequest struct {
	ID     uint64          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func nativeSocketFixture(t *testing.T, version string, loseQueueAck bool) (string, <-chan socketRequest) {
	t.Helper()
	return nativeSocketWithHook(t, version, loseQueueAck, nil, nil)
}

func nativeSocketWithHook(t *testing.T, version string, loseQueueAck bool,
	beforeReply func(context.Context, *websocket.Conn, socketRequest) bool, onClose func()) (string, <-chan socketRequest) {
	t.Helper()
	// A short path fits macOS sockaddr_un even when the test name is long.
	t.Setenv("TMPDIR", "/tmp")
	dir := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "codex.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	requests := make(chan socketRequest, 64)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, acceptErr := websocket.Accept(w, r, nil)
		if acceptErr != nil {
			t.Error(acceptErr)
			return
		}
		defer func() {
			if closeErr := conn.CloseNow(); closeErr != nil {
				t.Error(closeErr)
			}
			if onClose != nil {
				onClose()
			}
		}()
		conn.SetReadLimit(1024 * 1024)
		for {
			_, data, readErr := conn.Read(ctx)
			if readErr != nil {
				return
			}
			var request socketRequest
			if decodeErr := json.Unmarshal(data, &request); decodeErr != nil {
				t.Error(decodeErr)
				return
			}
			requests <- request
			if request.Method == "initialized" {
				continue
			}
			if request.Method == "thread/queue/add" && loseQueueAck {
				return
			}
			result := nativeResult(t, request, version)
			if beforeReply != nil && !beforeReply(ctx, conn, request) {
				return
			}
			reply, encodeErr := json.Marshal(map[string]any{"id": request.ID, "result": result})
			if encodeErr != nil {
				t.Error(encodeErr)
				return
			}
			if writeErr := conn.Write(ctx, websocket.MessageText, reply); writeErr != nil {
				return
			}
		}
	})}
	t.Cleanup(func() {
		if closeErr := server.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			t.Error(serveErr)
		}
	}()
	return path, requests
}

func nativeResult(t *testing.T, request socketRequest, version string) any {
	t.Helper()
	switch request.Method {
	case "initialize":
		return map[string]string{"userAgent": "codex-tui/" + version + " (Linux 6.12; x86_64) terminal/1"}
	case "thread/loaded/list":
		return map[string]any{"data": []string{testThread}, "nextCursor": nil}
	case "thread/read":
		return map[string]any{"thread": map[string]any{"id": testThread, "status": map[string]string{"type": "idle"}}}
	case "thread/queue/list":
		return map[string]any{"data": []any{}, "nextCursor": nil}
	case "thread/queue/add":
		var params queueParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			t.Error(err)
		}
		return map[string]any{"queuedSubmission": queuedSubmission{ID: "native-queue-item", Input: params.Input, ClientUserMessageID: params.ClientUserMessageID}}
	case "thread/queue/delete":
		return map[string]bool{"deleted": true}
	default:
		t.Errorf("unexpected native method %s", request.Method)
		return map[string]any{}
	}
}

func closeTransport(t *testing.T, tr *Transport) {
	t.Helper()
	t.Cleanup(func() {
		if err := tr.Close(); err != nil {
			t.Error(err)
		}
	})
}

func TestUnixSocketProtocol(t *testing.T) {
	path, requests := nativeSocketFixture(t, SupportedVersion, false)
	tr, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	closeTransport(t, tr)
	b, caps, err := tr.Probe(t.Context(), testThread)
	if err != nil || !caps.AttachExisting || !caps.WakeIdle || caps.ForkResponder || strings.Join(caps.SupportedScopes, ",") != "coauthor" || strings.Join(caps.ReceiptStages, ",") != "adapter_accepted" {
		t.Fatalf("probe = %+v, error = %v", caps, err)
	}
	frame, err := Frame(testMessage(), strings.Repeat("d", 64))
	if err != nil {
		t.Fatal(err)
	}
	result, err := tr.Queue(t.Context(), b, testMessage().MessageID, frame)
	if err != nil || len(result.ItemIDs) != 1 || result.ItemIDs[0] != "native-queue-item" {
		t.Fatalf("queue = %+v, error = %v", result, err)
	}
	first := <-requests
	var init struct {
		ClientInfo   struct{ Name string } `json:"clientInfo"`
		Capabilities struct {
			ExperimentalAPI bool `json:"experimentalApi"`
		} `json:"capabilities"`
	}
	if err = json.Unmarshal(first.Params, &init); err != nil || first.Method != "initialize" || !init.Capabilities.ExperimentalAPI || init.ClientInfo.Name != "peerturn" {
		t.Fatalf("initialize = %s, error = %v", first.Params, err)
	}
	// Reconnection to the same socket can verify the persisted exact binding.
	reconnected, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	closeTransport(t, reconnected)
	if err = reconnected.Validate(t.Context(), b); err != nil {
		t.Fatal(err)
	}
}

func TestUnixSocketLossAfterSubmitIsInDoubt(t *testing.T) {
	path, requests := nativeSocketFixture(t, SupportedVersion, true)
	tr, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	closeTransport(t, tr)
	b, err := tr.Bind(t.Context(), testThread)
	if err != nil {
		t.Fatal(err)
	}
	f, err := Frame(testMessage(), strings.Repeat("d", 64))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tr.Queue(t.Context(), b, testMessage().MessageID, f); !IsCode(err, "in_doubt") {
		t.Fatal(err)
	}
	adds := 0
	for len(requests) > 0 {
		if (<-requests).Method == "thread/queue/add" {
			adds++
		}
	}
	if adds != 1 {
		t.Fatalf("native add count = %d", adds)
	}
	if err = tr.Validate(t.Context(), b); err != nil {
		t.Fatalf("same binding did not recover after server drop: %v", err)
	}
}

func TestUnixSocketRefusesUnknownVersionAndNoDaemon(t *testing.T) {
	path, _ := nativeSocketFixture(t, "0.163.1", false)
	if tr, err := Open(t.Context(), path); tr != nil || !IsCode(err, "unsupported_version") {
		t.Fatalf("unknown version: transport=%v error=%v", tr, err)
	}
	if tr, err := Open(t.Context(), filepath.Join(t.TempDir(), "absent.sock")); tr != nil || !IsCode(err, "unsupported") {
		t.Fatalf("no daemon: transport=%v error=%v", tr, err)
	}
}

func TestUnixSocketRejectsUnsafePermissionsAndReplacement(t *testing.T) {
	path, _ := nativeSocketFixture(t, SupportedVersion, false)
	tr, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	closeTransport(t, tr)
	b, err := tr.Bind(t.Context(), testThread)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(path, 0000); err != nil {
		t.Fatal(err)
	}
	if _, err = Open(t.Context(), path); !IsCode(err, "unsupported") {
		t.Fatal(err)
	}
	if err = tr.Validate(t.Context(), b); !IsCode(err, "needs_rebind") {
		t.Fatal(err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("not a daemon"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = tr.Validate(t.Context(), b); !IsCode(err, "needs_rebind") {
		t.Fatal(err)
	}
}
