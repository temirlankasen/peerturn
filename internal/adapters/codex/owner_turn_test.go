package codex

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

const ownerTurnID = "019edaa1-3333-7333-8333-333333333333"

type ownerTurnRPC struct {
	*fakeRPC
	thread map[string]any
	turns  []map[string]any
	fail   string
}

func (r *ownerTurnRPC) Call(ctx context.Context, method string, params, result any) error {
	if method == r.fail {
		return errors.New("fixture unavailable")
	}
	var reply any
	switch method {
	case "thread/read":
		reply = map[string]any{"thread": r.thread}
	case "thread/turns/list":
		reply = map[string]any{"data": r.turns, "nextCursor": nil}
	default:
		return r.fakeRPC.Call(ctx, method, params, result)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	var p map[string]any
	if err = json.Unmarshal(raw, &p); err != nil {
		return err
	}
	r.calls = append(r.calls, rpcCall{method: method, params: p})
	raw, err = json.Marshal(reply)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, result)
}

func TestOwnerTurnRequiresExactActiveRootMetadata(t *testing.T) {
	for _, change := range []string{"exact", "internal_review_turn", "forwarded_review_event", "missing_root_turn", "null_root_turn", "completed", "missing_turn", "extra_turn", "wrong_thread", "wrong_session", "parent", "subagent", "unknown_source", "missing_parent", "missing_session", "missing_source", "idle", "unloaded", "read_failure", "turn_failure", "item_body", "unknown_view"} {
		t.Run(change, func(t *testing.T) {
			rpc := &ownerTurnRPC{fakeRPC: newFakeRPC(), thread: map[string]any{
				"id": testThread, "sessionId": testThread, "parentThreadId": nil, "source": "cli", "status": map[string]string{"type": "active"}},
				turns: []map[string]any{{"id": ownerTurnID, "rootTurnId": ownerTurnID, "status": "inProgress", "itemsView": "notLoaded", "items": []any{}}}}
			tr := NewTransport(rpc, testVersion)
			binding, err := tr.Bind(t.Context(), testThread)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "internal_review_turn":
				rpc.turns[0]["id"] = "019edaa1-4444-7444-8444-444444444444"
			case "forwarded_review_event":
				// Review forwards TurnStarted into root history with the child's ID
				// but the parent's inherited rootTurnId (83086188 review.rs:180).
				rpc.turns[0]["rootTurnId"] = "019edaa1-4444-7444-8444-444444444444"
			case "missing_root_turn":
				delete(rpc.turns[0], "rootTurnId")
			case "null_root_turn":
				rpc.turns[0]["rootTurnId"] = nil
			case "completed":
				rpc.turns[0]["status"] = "completed"
			case "missing_turn":
				rpc.turns = nil
			case "extra_turn":
				rpc.turns = append(rpc.turns, rpc.turns[0])
			case "wrong_thread":
				rpc.thread["id"] = otherThread
			case "wrong_session":
				rpc.thread["sessionId"] = otherThread
			case "parent":
				rpc.thread["parentThreadId"] = otherThread
			case "subagent":
				rpc.thread["source"] = map[string]string{"subAgent": "review"}
			case "unknown_source":
				rpc.thread["source"] = "unknown"
			case "missing_parent":
				delete(rpc.thread, "parentThreadId")
			case "missing_session":
				delete(rpc.thread, "sessionId")
			case "missing_source":
				delete(rpc.thread, "source")
			case "idle":
				rpc.thread["status"] = map[string]string{"type": "idle"}
			case "unloaded":
				rpc.loaded = nil
			case "read_failure":
				rpc.fail = "thread/read"
			case "turn_failure":
				rpc.fail = "thread/turns/list"
			case "item_body":
				rpc.turns[0]["items"] = []any{"unexpected body"}
			case "unknown_view":
				rpc.turns[0]["itemsView"] = "full"
			}
			verifier, ok := any(tr).(interface {
				ValidateOwnerTurn(context.Context, Binding, string) error
			})
			if !ok {
				t.Fatal("transport has no owner-turn verifier")
			}
			err = verifier.ValidateOwnerTurn(t.Context(), binding, ownerTurnID)
			if (err == nil) != (change == "exact") {
				t.Fatalf("root turn proof: %v", err)
			}
			for _, call := range rpc.calls {
				if call.method == "thread/turns/list" && (call.params["threadId"] != testThread || call.params["limit"] != float64(1) || call.params["itemsView"] != "notLoaded" || call.params["sortDirection"] != "desc") {
					t.Fatal("turn validation fetched unbounded history or message bodies")
				}
			}
		})
	}
}
