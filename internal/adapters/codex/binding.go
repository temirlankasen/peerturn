package codex

import (
	"context"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
)

var injectedDaemon atomic.Uint64

// Transport never starts a daemon, attaches a neighbor, steers or raw-injects.
type Transport struct {
	rpc        RPC
	version    string
	daemonID   string
	socketPath string
}

// NewTransport installs an explicitly supplied RPC implementation.
// Its process-local identity cannot qualify a binding on another Transport.
func NewTransport(rpc RPC, version string) *Transport {
	return &Transport{rpc: rpc, version: version, daemonID: fmt.Sprintf("injected:%d", injectedDaemon.Add(1))}
}

// Bind verifies the UUID obtained from CODEX_THREAD_ID against the same daemon.
// The caller must obtain that value from its current tool child environment.
func (t *Transport) Bind(ctx context.Context, threadID string) (Binding, error) {
	if t == nil || t.rpc == nil {
		return Binding{}, failure("unsupported")
	}
	if t.version != SupportedVersion {
		return Binding{}, failure("unsupported_version")
	}
	if !validThreadID(threadID) {
		return Binding{}, failure("needs_rebind")
	}
	b := Binding{ThreadID: threadID, DaemonID: t.daemonID, Version: t.version}
	if err := t.Validate(ctx, b); err != nil {
		return Binding{}, err
	}
	return b, nil
}

// Validate fails closed when a persisted binding is stale or no longer loaded.
func (t *Transport) Validate(ctx context.Context, b Binding) error {
	if err := t.validateIdentity(b); err != nil {
		return err
	}
	return t.validateThread(ctx, b.ThreadID)
}

func (t *Transport) validateIdentity(b Binding) error {
	if t == nil || t.rpc == nil || !validThreadID(b.ThreadID) || b.DaemonID == "" ||
		b.DaemonID != t.daemonID || b.Version != t.version {
		return failure("needs_rebind")
	}
	if t.version != SupportedVersion {
		return failure("unsupported_version")
	}
	if t.socketPath != "" {
		_, id, err := probeSocket(t.socketPath)
		if err != nil || id != t.daemonID {
			return failure("needs_rebind")
		}
	}
	return nil
}

func (t *Transport) validateThread(ctx context.Context, threadID string) error {
	if err := t.requireLoaded(ctx, threadID); err != nil {
		return err
	}
	var read struct {
		Thread struct {
			ID     string `json:"id"`
			Status struct {
				Type string `json:"type"`
			} `json:"status"`
		} `json:"thread"`
	}
	params := struct {
		ThreadID     string `json:"threadId"`
		IncludeTurns bool   `json:"includeTurns"`
	}{ThreadID: threadID}
	if err := t.rpc.Call(ctx, "thread/read", params, &read); err != nil {
		return failure("needs_rebind")
	}
	if read.Thread.ID != threadID || (read.Thread.Status.Type != "idle" && read.Thread.Status.Type != "active") {
		return failure("needs_rebind")
	}
	return nil
}

func (t *Transport) requireLoaded(ctx context.Context, id string) error {
	cursor := ""
	seen := map[string]bool{}
	for range 100 {
		var page struct {
			Data       []string `json:"data"`
			NextCursor string   `json:"nextCursor"`
		}
		params := struct {
			Cursor string `json:"cursor,omitempty"`
			Limit  int    `json:"limit"`
		}{Cursor: cursor, Limit: 100}
		if err := t.rpc.Call(ctx, "thread/loaded/list", params, &page); err != nil {
			return failure("unavailable")
		}
		if len(page.Data) > 100 || len(page.NextCursor) > 1024 {
			return failure("needs_rebind")
		}
		if slices.Contains(page.Data, id) {
			return nil
		}
		if page.NextCursor == "" || seen[page.NextCursor] {
			return failure("needs_rebind")
		}
		seen[page.NextCursor] = true
		cursor = page.NextCursor
	}
	return failure("needs_rebind")
}

func validThreadID(id string) bool {
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' || strings.ToLower(id) != id {
		return false
	}
	compact := strings.ReplaceAll(id, "-", "")
	if len(compact) != 32 {
		return false
	}
	_, err := hex.DecodeString(compact)
	return err == nil
}
