package daemon

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/adapters/codex"
	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestPeerJoinParsesAndDeduplicatesPastedTextInDaemon(t *testing.T) {
	parallelDaemonTest(t)
	admin := routingKey(t)
	relay := newRelayFixture(t, admin)
	issuer := openRecoveryDaemon(t, relay, admin)
	joiner := openRecoveryDaemon(t, relay, routingKey(t))
	created, err := issuer.createInvite(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var invitation struct {
		URL string `json:"url"`
	}
	decodeResult(t, created, &invitation)
	capability := provenJoinCapability(t, joiner)
	ownerPastedInvite(t, joiner, invitation.URL)
	link := invitation.URL
	for _, text := range []string{
		"private-prose-marker\n<" + link + ">",
		"private-prose-marker\n" + link + "\n" + link,
		"private-prose-marker\n[" + link + "](" + link + ")",
		"private-prose-marker\n[Join](" + link + ").\n<" + link + ">",
	} {
		ownerPastedInvite(t, joiner, invitation.URL)
		request := textJoinRequest(t, text)
		request.Session, request.Capability = joiner.state.Label, capability
		result, callErr := joiner.Dispatch(t.Context(), request)
		if callErr != nil {
			t.Fatalf("bounded pasted invitation rejected: %v", callErr)
		}
		raw, encodeErr := json.Marshal(result)
		if encodeErr != nil || bytes.Contains(raw, []byte("private-prose-marker")) || bytes.Contains(raw, []byte("#v=1")) {
			t.Fatal("join result exposed pasted input")
		}
		if len(joiner.state.Joining) != 1 || len(joiner.state.Peers) != 1 {
			t.Fatal("repeated invitation duplicated onboarding")
		}
		state, encodeErr := codec.Canonical(joiner.state)
		if encodeErr != nil || bytes.Contains(state, []byte("private-prose-marker")) {
			t.Fatal("raw invitation prose entered persisted state")
		}
	}
	entries, err := os.ReadDir(joiner.options.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		raw, readErr := os.ReadFile(filepath.Join(joiner.options.StateDir, entry.Name()))
		if readErr != nil || bytes.Contains(raw, []byte("private-prose-marker")) || bytes.Contains(raw, []byte(link)) {
			t.Fatal("private invitation input appeared in on-disk plaintext")
		}
	}
	for range 3 {
		if err = joiner.Sync(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err = issuer.Sync(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if peers := joiner.peers(); len(peers) != 1 || peers[0].State != "connected" {
		t.Fatal("join did not preserve signed-hello onboarding")
	}
}

func TestPeerJoinRejectsAmbiguousInvalidAndForeignTextWithoutEffects(t *testing.T) {
	parallelDaemonTest(t)
	d, _ := pairedStores(t)
	capability := provenJoinCapability(t, d)
	link := syntheticJoinLink("relay.example", "A")
	for _, test := range []struct {
		text, code string
	}{
		{"", "invalid_arguments"},
		{"private-prose-marker", "malformed_invitation"},
		{link + "\n" + syntheticJoinLink("relay.example", "B"), "malformed_invitation"},
		{"[" + link + "](" + syntheticJoinLink("relay.example", "B") + ")", "malformed_invitation"},
		{link + "\x00private-prose-marker", "invalid_encoding"},
		{strings.Repeat("a", 65537), "too_large"},
		{syntheticJoinLink("foreign.invalid", "A"), "audience_mismatch"},
	} {
		ownerPastedInvite(t, d, test.text)
		before, err := codec.Canonical(d.state)
		if err != nil {
			t.Fatal(err)
		}
		request := textJoinRequest(t, test.text)
		request.Session, request.Capability = d.state.Label, capability
		result, callErr := d.Dispatch(t.Context(), request)
		if result != nil || callErr == nil || localipc.PublicError(callErr).Code != test.code {
			t.Fatalf("invalid invitation result/code: %v", callErr)
		}
		public, err := json.Marshal(localipc.PublicError(callErr))
		if err != nil || bytes.Contains(public, []byte("private-prose-marker")) || bytes.Contains(public, []byte("#v=1")) {
			t.Fatal("invalid invitation leaked through public error")
		}
		after, err := codec.Canonical(d.state)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("rejected invitation changed session state")
		}
	}
}

func TestPeerJoinCannotEscapeSessionCapabilityOrStaleNativeBinding(t *testing.T) {
	parallelDaemonTest(t)
	d, _ := pairedStores(t)
	capability := joinCapability(t, d)
	for _, binding := range []struct{ session, capability string }{
		{"", capability}, {"other", capability}, {d.state.Label, "unknown"},
	} {
		request := textJoinRequest(t, syntheticJoinLink("foreign.invalid", "A"))
		request.Session, request.Capability = binding.session, binding.capability
		if _, err := d.Dispatch(t.Context(), request); localipc.PublicError(err).Code != "unbound" {
			t.Fatalf("join bypassed capability binding: %v", err)
		}
	}
	runtime := &nativeFixture{thread: "chosen", failure: "stale"}
	d.native, d.state.Codex = runtime, &codex.Binding{ThreadID: "chosen", DaemonID: "test-daemon", Version: nativeTestVersion}
	request := textJoinRequest(t, syntheticJoinLink("foreign.invalid", "A"))
	request.Session, request.Capability = d.state.Label, capability
	if _, err := d.Dispatch(t.Context(), request); localipc.PublicError(err).Code != "needs_rebind" {
		t.Fatalf("join bypassed live native binding check: %v", err)
	}
}

func textJoinRequest(t *testing.T, text string) localipc.Request {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"method": "join.text", "scope": "coauthor", "invitation_text": text})
	if err != nil {
		t.Fatal(err)
	}
	var request localipc.Request
	if err = json.Unmarshal(raw, &request); err != nil {
		t.Fatal(err)
	}
	return request
}

func joinCapability(t *testing.T, d *Daemon) string {
	t.Helper()
	result, err := d.Dispatch(t.Context(), localipc.Request{Method: "hello", Session: d.state.Label})
	if err != nil {
		t.Fatal(err)
	}
	var hello struct {
		Capability string `json:"capability"`
	}
	decodeResult(t, result, &hello)
	return hello.Capability
}

func syntheticJoinLink(host, id string) string {
	return "https://" + host + "/i/" + strings.Repeat(id, 21) + "A#v=1&ik=" + strings.Repeat("A", 43) + "&ek=" + strings.Repeat("A", 43) + "&fp=" + strings.Repeat("a", 64) + "&mh=" + strings.Repeat("b", 64)
}
