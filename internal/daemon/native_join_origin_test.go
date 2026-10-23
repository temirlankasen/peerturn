package daemon

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/invite"
	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestCodexJoinRequiresExactNativeThreadAndOwnerLink(t *testing.T) {
	parallelDaemonTest(t)
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
	r := pendingJoinRequest(t, a, hello.Capability, "")
	r.Session, r.Capability = a.state.Label, hello.Capability
	assertCodexOwnerPasteRequired(t, a, r)
	// A later successful hook with no link cannot attest an earlier paste.
	nativeCall(t, a, localipc.Request{Method: "codex_inform", MessageID: "later-owner-turn"})
	if a.hookVerified == 0 {
		t.Fatal("fixture did not observe the later active hook")
	}
	assertCodexOwnerPasteRequired(t, a, r)
	_, err = a.Dispatch(t.Context(), localipc.Request{Method: "codex_inform", Session: a.state.Label, NativeThread: a.state.Codex.ThreadID, MessageID: "owner-turn", OwnerInvites: invite.PromptLinks(invitation.URL)})
	if err != nil {
		t.Fatal(err)
	}
	hash, _, err := invite.OriginHashes(invitation.URL)
	if err != nil {
		t.Fatal(err)
	}
	proof := a.joinOrigins.owner[hash]
	proof.expires = time.Now().Add(-time.Second)
	a.joinOrigins.owner[hash] = proof
	assertCodexOwnerExpired(t, a, r)
	nativeCall(t, a, localipc.Request{Method: "codex_inform", MessageID: "owner-repaste", OwnerInvites: invite.PromptLinks(invitation.URL)})
	// mac4's first tool call changed two ek characters despite a verbatim paste.
	// A syntactically valid replacement secret must not inherit owner intent.
	prefix, secret, ok := strings.Cut(invitation.URL, "&ek=")
	if !ok || len(secret) < 2 {
		t.Fatal("fixture lacks an encryption key")
	}
	changed := []byte(secret)
	for i := range 2 {
		changed[i] = 'A'
		if secret[i] == 'A' {
			changed[i] = 'B'
		}
	}
	altered := prefix + "&ek=" + string(changed)
	if _, err = invite.ParseLink(altered); err != nil {
		t.Fatal("fixture must change a valid secret without changing link syntax")
	}
	mutated := textJoinRequest(t, altered)
	mutated.Session, mutated.Capability = r.Session, r.Capability
	assertCodexOwnerPasteRequired(t, a, mutated)
	result, err = a.Dispatch(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil || len(a.state.Joining) != 1 || len(raw) == 0 {
		t.Fatal("native owner invitation did not join")
	}
}

func assertCodexOwnerPasteRequired(t *testing.T, d *Daemon, r localipc.Request) {
	t.Helper()
	_, err := d.Dispatch(t.Context(), r)
	problem := localipc.PublicError(err)
	if problem.Code != "no_owner_invitation" || problem.State != "rejected" || problem.Retryable {
		t.Fatalf("unobserved or expired owner invitation acquired authority: %+v", problem)
	}
	for _, step := range []string{"unchanged", "daemon", "/hooks", "UserPromptSubmit", "trust", "paste", "same", "10 minutes"} {
		if !strings.Contains(problem.Message, step) {
			t.Errorf("owner refusal does not explain recovery step %q: %s", step, problem.Message)
		}
	}
	if r.InvitationText != "" && strings.Contains(problem.Message, r.InvitationText) || len(d.state.Joining) != 0 || len(d.state.Peers) != 0 {
		t.Fatal("refused join exposed invitation text or changed peer authority")
	}
}

func assertCodexOwnerExpired(t *testing.T, d *Daemon, r localipc.Request) {
	t.Helper()
	_, err := d.Dispatch(t.Context(), r)
	if localipc.PublicError(err).Code != "owner_invitation_expired" {
		t.Fatal("expired owner invitation accepted")
	}
}
