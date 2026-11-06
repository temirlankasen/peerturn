package claudeipc

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

// A daemon restart must not cost the owner a reload or a bind command. The
// helper reopens its registration and, for a session the owner bound before,
// proves itself again through the native socket. Only the daemon's answer
// that this session is pinned enables that proof: a first binding stays an
// explicit owner action, and no other session is ever attached.
type registration struct {
	instance string
	pinned   bool
}

func (b *bridge) open(ctx context.Context) (registration, error) {
	raw, err := b.options.Client.Call(ctx, localipc.Request{Method: "adapter.open", NativeSession: b.options.NativeSession, NativeSocket: os.Getenv("CLAUDE_CODE_MESSAGING_SOCKET"), RuntimeVersion: b.options.RuntimeVersion, Provider: b.options.Provider, WorkingDirectory: b.workingDirectory, NativeConfiguration: b.configuration})
	if err != nil {
		return registration{}, err
	}
	var registered struct {
		Instance string `json:"instance"`
		Pinned   string `json:"pinned"`
	}
	if err = json.Unmarshal(raw, &registered); err != nil || registered.Instance == "" {
		return registration{}, rebind()
	}
	return registration{instance: registered.Instance, pinned: registered.Pinned == "true"}, nil
}

func (b *bridge) currentInstance() string {
	b.instMu.Lock()
	defer b.instMu.Unlock()
	return b.instance
}

// reopen answers whether the helper may keep serving after the daemon said
// needs_rebind. It tries at most once per two seconds, and only while the
// native parent is still this helper's; otherwise the helper stops as before.
func (b *bridge) reopen(ctx context.Context) bool {
	b.instMu.Lock()
	defer b.instMu.Unlock()
	if time.Since(b.reopenedAt) < 2*time.Second {
		return b.reopenOK
	}
	b.reopenedAt, b.reopenOK = time.Now(), false
	if b.nativeGuard != nil && b.nativeGuard(ctx) != nil {
		return false
	}
	reg, err := b.open(ctx)
	if err != nil {
		switch localipc.PublicError(err).Code {
		case "unavailable", "runtime_unavailable", "timeout", "already_running":
			// The daemon is away, or still holds the previous lease for a few
			// seconds; the mod keeps polling and the next attempt registers.
			b.reopenOK = true
		}
		return b.reopenOK
	}
	b.instance, b.reopenOK = reg.instance, true
	if reg.pinned {
		go b.selfBind()
	}
	return true
}

// selfBind runs the same challenge an owner runs with session bind: the mod
// confirms the nonce only for its own session. A failure is left to the next
// owner prompt or tool call; a frame that reached the host is never repeated.
func (b *bridge) selfBind() {
	ctx, cancel := context.WithTimeout(b.serveCtx, 15*time.Second)
	defer cancel()
	if _, err := bindCurrent(ctx, b.options.Client, false); err != nil {
		return
	}
}
