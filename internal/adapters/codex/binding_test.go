package codex

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/temirlankasen/peerturn/internal/runtimepolicy"
)

var testVersion = runtimepolicy.Evaluate(runtimepolicy.Codex, "").Minimum

const testThread = "019a0cb0-1234-7000-8000-123456789abc"
const otherThread = "019a0cb0-1234-7000-8000-123456789abd"

type rpcCall struct {
	method string
	params map[string]any
}

type fakeRPC struct {
	calls      []rpcCall
	loaded     []string
	readID     string
	status     string
	queueErr   error
	queueReply string
}

func newFakeRPC() *fakeRPC {
	return &fakeRPC{loaded: []string{testThread}, readID: testThread, status: "idle"}
}

func (f *fakeRPC) Call(_ context.Context, method string, params, result any) error {
	encoded, err := json.Marshal(params)
	if err != nil {
		return err
	}
	var p map[string]any
	if err = json.Unmarshal(encoded, &p); err != nil {
		return err
	}
	f.calls = append(f.calls, rpcCall{method: method, params: p})
	var reply any
	switch method {
	case "thread/loaded/list":
		reply = map[string]any{"data": f.loaded}
	case "thread/read":
		reply = map[string]any{"thread": map[string]any{"id": f.readID, "status": map[string]any{"type": f.status}, "cwd": "/same/display/path"}}
	case "thread/queue/list":
		reply = map[string]any{"data": []any{}}
	case "thread/queue/add":
		if f.queueErr != nil {
			return f.queueErr
		}
		if f.queueReply != "" {
			return json.Unmarshal([]byte(f.queueReply), result)
		}
		reply = map[string]any{"queuedSubmission": map[string]any{"id": "queue-item-1", "clientUserMessageId": p["clientUserMessageId"], "input": p["input"]}}
	default:
		return errors.New("unexpected RPC method")
	}
	encoded, err = json.Marshal(reply)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, result)
}

func TestBindRequiresExactLoadedThread(t *testing.T) {
	for _, tc := range []struct {
		name           string
		loaded         []string
		readID, status string
		want           string
	}{
		{"exact", []string{otherThread, testThread}, testThread, "active", ""},
		{"same-cwd-does-not-bind", []string{otherThread}, testThread, "idle", "needs_rebind"},
		{"read-mismatch", []string{testThread}, otherThread, "idle", "needs_rebind"},
		{"not-loaded", []string{testThread}, testThread, "notLoaded", "needs_rebind"},
		{"error", []string{testThread}, testThread, "systemError", "needs_rebind"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rpc := newFakeRPC()
			rpc.loaded = tc.loaded
			rpc.readID = tc.readID
			rpc.status = tc.status
			tr := NewTransport(rpc, testVersion)
			bound, err := tr.Bind(t.Context(), testThread)
			if tc.want != "" {
				if !IsCode(err, tc.want) {
					t.Fatalf("error = %v", err)
				}
				return
			}
			if err != nil || bound.ThreadID != testThread || bound.DaemonID == "" || bound.Version != testVersion {
				t.Fatalf("binding = %+v, error = %v", bound, err)
			}
			if rpc.calls[1].params["threadId"] != testThread || rpc.calls[1].params["includeTurns"] != false {
				t.Fatal("exact metadata-only read required")
			}
		})
	}
}

func TestBindRejectsUnknownVersionAndEmbeddedSession(t *testing.T) {
	rpc := newFakeRPC()
	if _, err := NewTransport(rpc, "0.162.0").Bind(t.Context(), testThread); !IsCode(err, "unsupported_version") {
		t.Fatalf("version error = %v", err)
	}
	for _, id := range []string{"", "cwd:/work", "latest", "019A0CB0-1234-7000-8000-123456789abc"} {
		if _, err := NewTransport(rpc, testVersion).Bind(t.Context(), id); !IsCode(err, "needs_rebind") {
			t.Fatalf("%q: %v", id, err)
		}
	}
	if len(rpc.calls) != 0 {
		t.Fatal("invalid binding reached RPC")
	}
}

func TestBindingCannotCrossDaemonOrBecomeStale(t *testing.T) {
	rpc := newFakeRPC()
	tr := NewTransport(rpc, testVersion)
	b, err := tr.Bind(t.Context(), testThread)
	if err != nil {
		t.Fatal(err)
	}
	if err = NewTransport(rpc, testVersion).Validate(t.Context(), b); !IsCode(err, "needs_rebind") {
		t.Fatalf("daemon error = %v", err)
	}
	rpc.loaded = []string{otherThread}
	if err = tr.Validate(t.Context(), b); !IsCode(err, "needs_rebind") {
		t.Fatalf("stale error = %v", err)
	}
}
