package codex

import (
	"context"
	"encoding/json"
	"os"
	"sync/atomic"
	"testing"

	"github.com/coder/websocket"
)

func TestUnixSocketDeleteTargetsExactQueueItem(t *testing.T) {
	path, requests := nativeSocketFixture(t, SupportedVersion, false)
	tr, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	closeTransport(t, tr)
	b, err := tr.Bind(t.Context(), testThread)
	if err != nil {
		t.Fatal(err)
	}
	if deleted, deleteErr := tr.Delete(t.Context(), b, "native-queue-item"); deleteErr != nil || !deleted {
		t.Fatalf("deleted=%v, error=%v", deleted, deleteErr)
	}
	found := false
	for len(requests) > 0 {
		request := <-requests
		if request.Method != "thread/queue/delete" {
			continue
		}
		found = true
		var params map[string]string
		if err = json.Unmarshal(request.Params, &params); err != nil || len(params) != 2 || params["threadId"] != b.ThreadID || params["queuedSubmissionId"] != "native-queue-item" {
			t.Fatalf("delete params=%s, error=%v", request.Params, err)
		}
	}
	if !found {
		t.Fatal("native delete was not called")
	}
}

func TestUnixSocketDeleteRefusesReplacedSocket(t *testing.T) {
	path, requests := nativeSocketFixture(t, SupportedVersion, false)
	tr, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	closeTransport(t, tr)
	b, err := tr.Bind(t.Context(), testThread)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("not the bound daemon"), 0600); err != nil {
		t.Fatal(err)
	}
	if deleted, deleteErr := tr.Delete(t.Context(), b, "native-queue-item"); deleted || !IsCode(deleteErr, "needs_rebind") {
		t.Fatalf("deleted=%v, error=%v", deleted, deleteErr)
	}
	for len(requests) > 0 {
		if (<-requests).Method == "thread/queue/delete" {
			t.Fatal("replacement socket accepted delete")
		}
	}
}

func TestUnixSocketRechecksVersionBeforeEveryEffect(t *testing.T) {
	var versionChanged atomic.Bool
	hook := func(ctx context.Context, conn *websocket.Conn, request socketRequest) bool {
		if request.Method != "initialize" || !versionChanged.Load() {
			return true
		}
		response, err := json.Marshal(map[string]any{"id": request.ID,
			"result": map[string]string{"userAgent": "codex-tui/0.163.1 (Linux)"}})
		if err != nil {
			t.Error(err)
			return false
		}
		if err = conn.Write(ctx, websocket.MessageText, response); err != nil {
			t.Error(err)
		}
		return false
	}
	path, requests := nativeSocketWithHook(t, SupportedVersion, false, hook, nil)
	tr, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	closeTransport(t, tr)
	b, err := tr.Bind(t.Context(), testThread)
	if err != nil {
		t.Fatal(err)
	}
	versionChanged.Store(true)
	if deleted, deleteErr := tr.Delete(t.Context(), b, "native-queue-item"); deleted || deleteErr == nil {
		t.Fatalf("changed version: deleted=%v, error=%v", deleted, deleteErr)
	}
	for len(requests) > 0 {
		if (<-requests).Method == "thread/queue/delete" {
			t.Fatal("changed native version accepted delete")
		}
	}
}
