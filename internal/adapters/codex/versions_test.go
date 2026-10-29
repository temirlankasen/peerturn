package codex

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestMinimumVersionAdmitsNewerBindingsAndPreservesActualVersion(t *testing.T) {
	for _, version := range []string{"0.163.0", "0.163.1", "0.164.0", "1.0.0", "12.0.0", "1.0.0-rc.1", "0.163.0+modified"} {
		t.Run(version, func(t *testing.T) {
			tr := NewTransport(newFakeRPC(), version)
			binding, caps, err := tr.Probe(t.Context(), testThread)
			if err != nil || binding.Version != version || caps.Version != version || !caps.AttachExisting || !caps.WakeIdle {
				t.Fatalf("binding=%+v capabilities=%+v error=%v", binding, caps, err)
			}
			changed := binding
			changed.Version = "99.0.0"
			if err = tr.Validate(t.Context(), changed); !IsCode(err, "needs_rebind") {
				t.Fatalf("version continuity was lost: %v", err)
			}
		})
	}
}

func TestVersionFailuresAreTypedBeforeNativeCalls(t *testing.T) {
	for _, tc := range []struct{ version, code string }{
		{"0.162.9", "unsupported_version"}, {"0.163.0-rc.1", "unsupported_version"},
		{"", "runtime_version_unknown"}, {"future", "runtime_version_unknown"}, {"0.0163.0", "runtime_version_unknown"},
	} {
		t.Run(tc.version, func(t *testing.T) {
			rpc := newFakeRPC()
			if _, err := NewTransport(rpc, tc.version).Bind(t.Context(), testThread); !IsCode(err, tc.code) {
				t.Fatalf("version %q: %v", tc.version, err)
			}
			if len(rpc.calls) != 0 {
				t.Fatal("rejected version contacted native runtime")
			}
		})
	}
}

func TestInitializeAcceptsNewerServerAndRetainsActualVersion(t *testing.T) {
	path, requests := nativeSocketFixture(t, "0.164.0", false)
	tr, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	closeTransport(t, tr)
	binding, caps, err := tr.Probe(t.Context(), testThread)
	if err != nil || tr.version != "0.164.0" || binding.Version != "0.164.0" || caps.Version != "0.164.0" {
		t.Fatalf("actual runtime version lost: transport=%s binding=%+v caps=%+v error=%v", tr.version, binding, caps, err)
	}
	for len(requests) > 0 {
		switch request := <-requests; request.Method {
		case "initialize", "initialized", "thread/loaded/list", "thread/read", "thread/queue/list":
		default:
			t.Fatalf("self-test attempted effect: %s", request.Method)
		}
	}
}

type queueContractRPC struct {
	*ownerTurnRPC
	queueReply string
	queueErr   error
}

func (r *queueContractRPC) Call(ctx context.Context, method string, params, result any) error {
	if method == "thread/queue/list" {
		r.calls = append(r.calls, rpcCall{method: method})
		if r.queueErr != nil {
			return r.queueErr
		}
		return json.Unmarshal([]byte(r.queueReply), result)
	}
	if method == "thread/queue/delete" {
		r.calls = append(r.calls, rpcCall{method: method})
		return json.Unmarshal([]byte(`{"deleted":true}`), result)
	}
	return r.ownerTurnRPC.Call(ctx, method, params, result)
}

func TestQueueContractFailureOnlyClosesCoauthor(t *testing.T) {
	for _, reply := range []string{`{}`, `{"data":null}`, `{"data":{}}`, `{"data":[{}]}`, `{"data":[{},{}]}`, `{"data":[{"id":"q","input":[],"clientUserMessageId":7}]}`} {
		t.Run(reply, func(t *testing.T) {
			rpc := &queueContractRPC{ownerTurnRPC: &ownerTurnRPC{fakeRPC: newFakeRPC(), thread: map[string]any{
				"id": testThread, "sessionId": testThread, "parentThreadId": nil, "source": "cli", "status": map[string]string{"type": "active"}},
				turns: []map[string]any{{"id": ownerTurnID, "rootTurnId": ownerTurnID, "status": "inProgress", "itemsView": "notLoaded", "items": []any{}}}}, queueReply: reply}
			tr := NewTransport(rpc, "0.164.0")
			binding, err := tr.Bind(t.Context(), testThread)
			if err != nil {
				t.Fatalf("queue contract destroyed valid binding: %v", err)
			}
			probed, caps, err := tr.Probe(t.Context(), testThread)
			if !IsCode(err, "runtime_contract_changed") || probed != binding || !caps.AttachExisting || caps.WakeIdle || len(caps.SupportedScopes) != 0 {
				t.Fatalf("queue contract scope: binding=%+v caps=%+v error=%v", probed, caps, err)
			}
			if err = tr.ValidateOwnerTurn(t.Context(), binding, ownerTurnID); err != nil {
				t.Fatalf("queue failure closed owner proof: %v", err)
			}
			if deleted, deleteErr := tr.Delete(t.Context(), binding, "queue-item"); !deleted || deleteErr != nil {
				t.Fatalf("queue failure closed local revoke: %v %v", deleted, deleteErr)
			}
			frame, frameErr := Frame(testMessage(), strings.Repeat("d", 64))
			if frameErr != nil {
				t.Fatal(frameErr)
			}
			if _, err = tr.Queue(t.Context(), binding, testMessage().MessageID, frame); !IsCode(err, "runtime_contract_changed") {
				t.Fatalf("coauthor escaped queue contract failure: %v", err)
			}
			for _, call := range rpc.calls {
				if call.method == "thread/queue/add" {
					t.Fatal("failed contract attempted submit")
				}
			}
		})
	}
}
