package daemon

import (
	"encoding/json"
	"testing"

	"github.com/temirlankasen/peerturn/internal/invite"
	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestCodexJoinRequiresExactNativeThreadAndOwnerLink(t *testing.T) {
	admin := routingKey(t)
	relay := newRelayFixture(t, admin)
	issuer := openRecoveryDaemon(t, relay, admin)
	runtime := &nativeFixture{thread: "owner-thread"}
	options := InitOptions{StateDir: stateDir(t), RelayURL: relay.server.URL, SessionLabel: runtime.thread, FilePermissions: true, CodexThreadID: runtime.thread, Native: runtime}
	if _, err := Initialize(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	a, err := Open(t.Context(), Options{StateDir: options.StateDir, HTTPClient: relay.server.Client(), Native: runtime})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := a.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	created, err := issuer.createInvite(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var invitation struct {
		URL string `json:"url"`
	}
	decodeResult(t, created, &invitation)
	if _, err = a.Dispatch(t.Context(), localipc.Request{Method: "hello", Session: a.state.Label}); localipc.PublicError(err).Code != "unbound" {
		t.Fatalf("label-only Codex hello bound a native session: %v", err)
	}
	result, err := a.Dispatch(t.Context(), localipc.Request{Method: "hello", Session: a.state.Label, NativeThread: a.state.Codex.ThreadID})
	if err != nil {
		t.Fatal(err)
	}
	var hello struct {
		Capability string `json:"capability"`
	}
	decodeResult(t, result, &hello)
	r := textJoinRequest(t, invitation.URL)
	r.Session, r.Capability = a.state.Label, hello.Capability
	if _, err = a.Dispatch(t.Context(), r); localipc.PublicError(err).Code != "owner_invitation_required" {
		t.Fatalf("unobserved Codex invitation reached onboarding: %v", err)
	}
	_, err = a.Dispatch(t.Context(), localipc.Request{Method: "codex_inform", Session: a.state.Label, NativeThread: a.state.Codex.ThreadID, MessageID: "owner-turn", OwnerInvites: invite.PromptHashes(invitation.URL)})
	if err != nil {
		t.Fatal(err)
	}
	result, err = a.Dispatch(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil || len(a.state.Joining) != 1 || len(raw) == 0 {
		t.Fatal("native owner invitation did not join")
	}
}
