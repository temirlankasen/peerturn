// Package claudeipc connects the Claude mod to private same-uid daemon IPC.
package claudeipc

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

// BindCurrent is the explicit owner-requested native challenge operation.
// Startup and diagnostics use ResolveCurrentEvidence, which never writes frames.
// The exported token is used only on its native socket, never returned or logged.
func BindCurrent(ctx context.Context, client localipc.Client) (string, error) {
	evidence, err := bindCurrent(ctx, client, false)
	return evidence.Session, err
}

// CurrentEvidence retains the proven local session and opaque native incarnation.
type CurrentEvidence struct {
	Session      string `json:"session"`
	BindingEpoch string `json:"binding_epoch,omitempty"`
}

func bindCurrent(ctx context.Context, client localipc.Client, waitForHelper bool) (CurrentEvidence, error) {
	socket, token := os.Getenv("CLAUDE_CODE_MESSAGING_SOCKET"), os.Getenv("CLAUDE_CODE_MESSAGING_TOKEN")
	if token == "" || len(token) > 4096 {
		return CurrentEvidence{}, rebind()
	}
	if _, err := nativeSocketInfo(socket); err != nil {
		return CurrentEvidence{}, rebind()
	}
	raw, err := currentChallenge(ctx, client, socket, waitForHelper)
	if err != nil {
		return CurrentEvidence{}, rebind()
	}
	var challenge struct {
		Nonce string `json:"nonce"`
	}
	if json.Unmarshal(raw, &challenge) != nil || len(challenge.Nonce) != 64 {
		return CurrentEvidence{}, rebind()
	}
	conn, peer, err := connectNative(ctx, socket)
	if err != nil {
		return CurrentEvidence{}, rebind()
	}
	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			return
		}
	}()
	if err = conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return CurrentEvidence{}, rebind()
	}
	stop := context.AfterFunc(ctx, func() { closeNative(conn) })
	defer stop()
	encoder := json.NewEncoder(conn)
	if err = encoder.Encode(map[string]string{"type": "auth", "token": token}); err != nil {
		return CurrentEvidence{}, rebind()
	}
	frame := map[string]any{"type": "user", "message": map[string]string{"role": "user", "content": "<peerturn-bind>" + challenge.Nonce + "</peerturn-bind>"}, "priority": "next"}
	if err = encoder.Encode(frame); err != nil {
		return CurrentEvidence{}, bindingInDoubt()
	}
	resolveCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		raw, err = client.Call(resolveCtx, localipc.Request{Method: "adapter.resolve", NativeSocket: socket, Nonce: challenge.Nonce})
		if err == nil {
			var resolved CurrentEvidence
			current, peerErr := processIdentity(peer.pid)
			if peerErr != nil || current != peer {
				return CurrentEvidence{}, bindingInDoubt()
			}
			if json.Unmarshal(raw, &resolved) == nil && resolved.Session != "" {
				return resolved, nil
			}
		}
		select {
		case <-resolveCtx.Done():
			return CurrentEvidence{}, bindingInDoubt()
		case <-ticker.C:
		}
	}
}

func rebind() error { return localipc.NewError("needs_rebind", "needs_rebind") }
