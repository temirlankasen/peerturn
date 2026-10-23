package codex

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// The pinned legacy server linearly decodes its rollout before selecting even
// a one-turn metadata page. This synthetic history contains no real transcript.
type largeOwnerRolloutRPC struct {
	*ownerTurnRPC
	rollout string
	decoded int
}

type ownerTurnDeadlineRPC struct {
	*ownerTurnRPC
	deadlines map[string]time.Time
	cancel    context.CancelFunc
	cancelAt  string
}

func (r *ownerTurnDeadlineRPC) Call(ctx context.Context, method string, params, result any) error {
	if deadline, ok := ctx.Deadline(); ok {
		r.deadlines[method] = deadline
	}
	if method == r.cancelAt {
		r.cancel()
	}
	return r.ownerTurnRPC.Call(ctx, method, params, result)
}

func TestOwnerTurnSeparatesBoundedMetadataAndReplayBudgets(t *testing.T) {
	for _, cancelAt := range []string{"", "thread/read", "thread/turns/list"} {
		t.Run("cancel_"+cancelAt, func(t *testing.T) {
			rpc := &ownerTurnDeadlineRPC{ownerTurnRPC: &ownerTurnRPC{fakeRPC: newFakeRPC(), thread: map[string]any{
				"id": testThread, "sessionId": testThread, "parentThreadId": nil, "source": "cli",
				"status": map[string]string{"type": "active"}},
				turns: []map[string]any{{"id": ownerTurnID, "rootTurnId": ownerTurnID,
					"status": "inProgress", "itemsView": "notLoaded", "items": []any{}}}},
				deadlines: map[string]time.Time{}}
			tr := NewTransport(rpc, SupportedVersion)
			binding, err := tr.Bind(t.Context(), testThread)
			if err != nil {
				t.Fatal(err)
			}
			rpc.deadlines = map[string]time.Time{}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			rpc.cancelAt, rpc.cancel = cancelAt, cancel
			err = tr.ValidateOwnerTurn(ctx, binding, ownerTurnID)
			if (err == nil) != (cancelAt == "") {
				t.Fatalf("owner proof ignored cancelled budget: %v", err)
			}
			if cancelAt != "" {
				return
			}
			metadataDeadline := rpc.deadlines["thread/loaded/list"]
			if metadataDeadline.IsZero() || metadataDeadline != rpc.deadlines["thread/read"] {
				t.Fatal("loaded/read did not share one bounded metadata deadline")
			}
			historyDeadline := rpc.deadlines["thread/turns/list"]
			if !historyDeadline.After(metadataDeadline) || historyDeadline.After(metadataDeadline.Add(OwnerTurnTimeout-ownerMetadataTimeout)) {
				t.Fatal("rollout replay did not get a separate bounded deadline")
			}
		})
	}
}

func (r *largeOwnerRolloutRPC) Call(ctx context.Context, method string, params, result any) error {
	if method == "thread/turns/list" {
		decoder := json.NewDecoder(strings.NewReader(r.rollout))
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			var event struct {
				Type    string `json:"type"`
				Payload struct {
					Type string `json:"type"`
				} `json:"payload"`
			}
			if err := decoder.Decode(&event); err != nil {
				if err == io.EOF {
					break
				}
				return err
			}
			r.decoded++
			if r.decoded%2000 == 0 {
				// Virtual I/O latency scales with history rows. synctest keeps
				// this regression independent of host CPU load and wall time.
				time.Sleep(200 * time.Millisecond)
			}
		}
	}
	return r.ownerTurnRPC.Call(ctx, method, params, result)
}

func TestOwnerTurnLargeRolloutWithinBoundedReplayBudget(t *testing.T) {
	synctest.Test(t, testOwnerTurnLargeRollout)
}

func testOwnerTurnLargeRollout(t *testing.T) {
	const events = 14000
	const eventBytes = 3600
	const prefix = `{"type":"event_msg","payload":{"type":"turn_complete","turn_id":"synthetic-old-turn","root_turn_id":"synthetic-old-turn","status":"completed","last_agent_message":"`
	const suffix = "\"}}\n"
	line := prefix + strings.Repeat("x", eventBytes-len(prefix)-len(suffix)) + suffix
	rpc := &largeOwnerRolloutRPC{ownerTurnRPC: &ownerTurnRPC{fakeRPC: newFakeRPC(), thread: map[string]any{
		"id": testThread, "sessionId": testThread, "parentThreadId": nil, "source": "cli",
		"status": map[string]string{"type": "active"}},
		turns: []map[string]any{{"id": ownerTurnID, "rootTurnId": ownerTurnID,
			"status": "inProgress", "itemsView": "notLoaded", "items": []any{}}}},
		rollout: strings.Repeat(line, events)}
	if len(rpc.rollout) != 50400000 {
		t.Fatalf("synthetic rollout has %d bytes", len(rpc.rollout))
	}
	tr := NewTransport(rpc, SupportedVersion)
	binding, err := tr.Bind(t.Context(), testThread)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), OwnerTurnTimeout)
	defer cancel()
	if err = tr.ValidateOwnerTurn(ctx, binding, ownerTurnID); err != nil {
		t.Fatalf("large rollout rejected the exact live owner turn after %d events: %v", rpc.decoded, err)
	}
	if rpc.decoded != events {
		t.Fatalf("history replay decoded %d events", rpc.decoded)
	}
}
