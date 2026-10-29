package codex

import (
	"context"
	"encoding/json"
	"time"
)

// OwnerTurnTimeout bounds the complete native proof, including rollout replay.
// 83086188 has no proven read-only live-turn query that avoids legacy replay.
const OwnerTurnTimeout = 9 * time.Second

const (
	ownerMetadataTimeout    = 2 * time.Second
	ownerTurnHistoryTimeout = 8 * time.Second
)

// ValidateOwnerTurn uses native metadata, not rollout filenames or prompt text.
// In 83086188, hook_runtime.rs:685-687 shares session_id with internal Review,
// but turn_id belongs to the delegate. thread/turns/list scopes it to its thread.
func (t *Transport) ValidateOwnerTurn(ctx context.Context, b Binding, turnID string) error {
	if err := t.validateIdentity(b); err != nil {
		return err
	}
	if !validThreadID(turnID) {
		return failure("needs_rebind")
	}
	ctx, cancel := context.WithTimeout(ctx, OwnerTurnTimeout)
	defer cancel()
	metadataCtx, metadataCancel := context.WithTimeout(ctx, ownerMetadataTimeout)
	err := t.validateOwnerThread(metadataCtx, b)
	metadataCancel()
	if err != nil {
		return err
	}
	return t.validateOwnerTurnHistory(ctx, b, turnID)
}

func (t *Transport) validateOwnerThread(ctx context.Context, b Binding) error {
	if err := t.requireLoaded(ctx, b.ThreadID); err != nil {
		return err
	}
	var read struct {
		Thread struct {
			ID      string          `json:"id"`
			Session string          `json:"sessionId"`
			Parent  json.RawMessage `json:"parentThreadId"`
			Source  string          `json:"source"`
			Status  struct {
				Type string `json:"type"`
			} `json:"status"`
		} `json:"thread"`
	}
	params := struct {
		ThreadID     string `json:"threadId"`
		IncludeTurns bool   `json:"includeTurns"`
	}{ThreadID: b.ThreadID}
	if err := t.rpc.Call(ctx, "thread/read", params, &read); err != nil {
		return t.contractCallError(err, "codex.owner_thread", "hook", "owner_invitation", "inform_boundary")
	}
	if read.Thread.ID != b.ThreadID || read.Thread.Session != b.ThreadID || string(read.Thread.Parent) != "null" || read.Thread.Status.Type != "active" {
		return failure("needs_rebind")
	}
	switch read.Thread.Source {
	case "cli", "vscode", "exec", "appServer":
	default:
		return failure("needs_rebind")
	}
	if ctx.Err() != nil {
		return failure("needs_rebind")
	}
	return nil
}

func (t *Transport) validateOwnerTurnHistory(ctx context.Context, b Binding, turnID string) error {
	ctx, cancel := context.WithTimeout(ctx, ownerTurnHistoryTimeout)
	defer cancel()
	var page struct {
		Data []struct {
			ID        string            `json:"id"`
			RootID    string            `json:"rootTurnId"`
			Status    string            `json:"status"`
			ItemsView string            `json:"itemsView"`
			Items     []json.RawMessage `json:"items"`
		} `json:"data"`
	}
	// notLoaded bounds the returned data, but legacy history still replays in
	// full (83086188 thread_processor.rs:3236-3243), so allow a separate budget.
	query := struct {
		ThreadID      string `json:"threadId"`
		Limit         int    `json:"limit"`
		SortDirection string `json:"sortDirection"`
		ItemsView     string `json:"itemsView"`
	}{b.ThreadID, 1, "desc", "notLoaded"}
	if err := t.rpc.Call(ctx, "thread/turns/list", query, &page); err != nil {
		return t.contractCallError(err, "codex.owner_turn", "hook", "owner_invitation", "inform_boundary")
	}
	// Review forwards its TurnStarted into root history. Its inherited root ID
	// still names the parent, so membership alone would misclassify the child.
	if ctx.Err() != nil || len(page.Data) != 1 || page.Data[0].ID != turnID || page.Data[0].RootID != turnID || page.Data[0].Status != "inProgress" || page.Data[0].ItemsView != "notLoaded" || len(page.Data[0].Items) != 0 {
		return failure("needs_rebind")
	}
	return t.validateIdentity(b)
}
