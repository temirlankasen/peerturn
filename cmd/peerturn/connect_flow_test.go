package main

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/daemon"
	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestConnectReusesProvenBindingWithoutRebinding(t *testing.T) {
	f := newConnectFake(t).withProfile()
	f.up, f.ready = true, true
	got := f.run("--json")
	if got.code != 0 || f.count("bind") != 0 {
		t.Fatalf("code=%d events=%v", got.code, f.events)
	}
	if got.json(t)["binding_action"] != "reused" {
		t.Fatalf("stdout=%s", got.stdout)
	}
}

func TestConnectBindsExplicitlyOnlyWhenUnboundThenVerifies(t *testing.T) {
	f := newConnectFake(t).withProfile()
	f.up = true
	got := f.run("--json")
	if got.code != 0 || f.count("bind") != 1 {
		t.Fatalf("code=%d events=%v stdout=%s", got.code, f.events, got.stdout)
	}
	last := -1
	for i, event := range f.events {
		if event == "resolve" {
			last = i
		}
	}
	if bind := indexOf(f.events, "bind"); last < bind {
		t.Fatalf("bound binding was not re-verified before the link: %v", f.events)
	}
	if got.json(t)["binding_action"] != "bound" || got.json(t)["binding"] != "native_claude" {
		t.Fatalf("stdout=%s", got.stdout)
	}
}

func TestConnectNeverReplacesAnotherActiveNativeSession(t *testing.T) {
	f := newConnectFake(t).withProfile()
	f.up, f.foreign = true, true
	got := f.run("--json")
	if got.code != 3 || got.errorCode(t) != "needs_rebind" {
		t.Fatalf("code=%d stdout=%s", got.code, got.stdout)
	}
	if f.count("bind") != 0 || f.count("invite") != 0 || f.count("spawn") != 0 {
		t.Fatalf("foreign binding was disturbed: %v", f.events)
	}
	if strings.Contains(got.stdout, fakeLink) {
		t.Fatal("link leaked on refusal")
	}
}

func TestConnectMissingHelperHasActionableError(t *testing.T) {
	f := newConnectFake(t).withProfile()
	f.up, f.bindFails = true, 1
	got := f.run("--json")
	if got.code != 3 || got.errorCode(t) != "needs_rebind" || f.count("bind") != 1 || f.count("invite") != 0 {
		t.Fatalf("code=%d events=%v stdout=%s", got.code, f.events, got.stdout)
	}
	if !strings.Contains(got.stdout, "reload") && !strings.Contains(got.stdout, "resume") {
		t.Fatalf("the failure must say how to recover: %s", got.stdout)
	}
}

func TestConnectExplainsProfilePinnedToAnotherSession(t *testing.T) {
	f := newConnectFake(t).withProfile()
	f.up, f.pinned, f.bindFails = true, true, 1<<20
	got := f.run("--json")
	if got.code != 3 || got.errorCode(t) != "needs_rebind" || !strings.Contains(got.stdout, "session unbind") || f.count("invite") != 0 {
		t.Fatalf("code=%d stdout=%s", got.code, got.stdout)
	}
}

func TestConnectUsageListsTheCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runInput([]string{"help"}, strings.NewReader(""), &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "connect [--relay URL] [--no-invite] [--json]") {
		t.Fatalf("code=%d usage=%s", code, stdout.String())
	}
}

func TestConnectUncertainBindIsNotRetried(t *testing.T) {
	f := newConnectFake(t).withProfile()
	f.up, f.bindFails = true, 1
	got := f.run("--json")
	if got.code != 3 || f.count("bind") != 1 || f.count("invite") != 0 {
		t.Fatalf("uncertain native bind was retried: code=%d events=%v", got.code, f.events)
	}
}

func TestConnectStopsOnNonRebindBindFailure(t *testing.T) {
	f := newConnectFake(t).withProfile()
	f.up, f.bindErr = true, localipc.NewError("unavailable", "unavailable")
	if got := f.run("--json"); got.code != 4 || f.count("bind") != 1 || f.waits != 0 {
		t.Fatalf("code=%d events=%v waits=%d", got.code, f.events, f.waits)
	}
}

func TestConnectRefusesWhenBoundSessionDiffersFromVerifiedEvidence(t *testing.T) {
	f := newConnectFake(t).withProfile()
	f.up, f.resolveSession = true, "claude-other"
	got := f.run("--json")
	if got.code != 3 || got.errorCode(t) != "binding_mismatch" || f.count("invite") != 0 {
		t.Fatalf("code=%d events=%v stdout=%s", got.code, f.events, got.stdout)
	}
}

func TestConnectCreatesCoauthorLinkAndKeepsStrongTrustWarning(t *testing.T) {
	f := newConnectFake(t).withProfile()
	f.up, f.ready = true, true
	got := f.run("--json")
	if got.code != 0 || f.count("invite") != 1 {
		t.Fatalf("code=%d events=%v stdout=%s", got.code, f.events, got.stdout)
	}
	out := got.json(t)
	invitation, ok := out["invitation"].(map[string]any)
	if !ok {
		t.Fatalf("missing invitation object: %s", got.stdout)
	}
	if out["state"] != "link_ready" || invitation["url"] != fakeLink || invitation["scope"] != "coauthor" || invitation["expires_at"] != float64(1893456000) {
		t.Fatalf("stdout=%s", got.stdout)
	}
	warning, ok := out["warning"].(string)
	if !ok {
		t.Fatalf("missing warning string: %s", got.stdout)
	}
	if !strings.Contains(warning, "strong trust") || !strings.Contains(warning, "not a sandbox") || !strings.Contains(warning, "later owner turns") {
		t.Fatalf("warning=%q", warning)
	}
	if got.stderr != "" {
		t.Fatalf("stderr must stay empty on success: %q", got.stderr)
	}
}

func TestConnectLinkReadyIsNotConnectedEvidence(t *testing.T) {
	f := newConnectFake(t).withProfile()
	f.up, f.ready = true, true
	f.peers = []daemon.Peer{{ID: "p1", State: "waiting_inviter"}}
	got := f.run("--json")
	if out := got.json(t); out["state"] != "link_ready" || out["connected_peers"] != float64(0) {
		t.Fatalf("a created link is not a connected peer: %s", got.stdout)
	}
	f = newConnectFake(t).withProfile()
	f.up, f.ready = true, true
	f.peers = []daemon.Peer{{ID: "p1", State: "connected"}, {ID: "p2", State: "revoked"}, {ID: "p3", State: "waiting_inviter"}}
	got = f.run("--json")
	if out := got.json(t); out["state"] != "link_ready" || out["connected_peers"] != float64(1) {
		t.Fatalf("connected evidence must come from the daemon peer list only: %s", got.stdout)
	}
	f = newConnectFake(t).withProfile()
	f.up, f.ready, f.peersErr = true, true, localipc.NewError("unavailable", "unavailable")
	got = f.run("--json")
	if _, present := got.json(t)["connected_peers"]; got.code != 0 || present {
		t.Fatalf("unknown peer evidence must be omitted, not guessed: %s", got.stdout)
	}
}

func TestConnectReportsExistingStoragePolicy(t *testing.T) {
	f := newConnectFake(t).withProfile()
	f.config.StorageMode = "os_keystore"
	got := f.run("--no-invite", "--json")
	if got.code != 0 || got.json(t)["storage"] != "os_keystore" || f.count("init") != 0 {
		t.Fatalf("existing storage was replaced or misreported: %s", got.stdout)
	}
}

func TestConnectRelayRefusalExplainsOperatorAction(t *testing.T) {
	f := newConnectFake(t).withProfile()
	f.up, f.ready, f.inviteErr = true, true, localipc.NewError("scope_denied", "rejected")
	got := f.run("--json")
	if got.code != 2 || got.errorCode(t) != "scope_denied" || !strings.Contains(got.stdout, "relay operator") || strings.Contains(got.stdout, fakeLink) {
		t.Fatalf("relay refusal was not actionable: %s", got.stdout)
	}
}

func TestConnectNoInviteReturnsReadyWithoutLink(t *testing.T) {
	f := newConnectFake(t).withProfile()
	got := f.run("--no-invite", "--json")
	out := got.json(t)
	if got.code != 0 || out["state"] != "ready" || f.count("invite") != 0 {
		t.Fatalf("code=%d events=%v stdout=%s", got.code, f.events, got.stdout)
	}
	if _, present := out["invitation"]; present || strings.Contains(got.stdout, "secret-fragment") {
		t.Fatalf("recipient mode must not create or print a link: %s", got.stdout)
	}
	warning, ok := out["warning"].(string)
	if !ok || !strings.Contains(warning, "strong trust") {
		t.Fatalf("warning missing: %s", got.stdout)
	}
}

func TestConnectRefusesLinkIssuedUnderPreviewLabel(t *testing.T) {
	f := newConnectFake(t).withProfile()
	f.up, f.ready, f.inviteBinding = true, true, "test_session"
	got := f.run("--json")
	if got.code != 3 || got.errorCode(t) != "binding_mismatch" || strings.Contains(got.stdout+got.stderr, "secret-fragment") {
		t.Fatalf("a preview label is not native proof: code=%d stdout=%s", got.code, got.stdout)
	}
}

func TestConnectRelayRefusalSurfacesWithoutLink(t *testing.T) {
	f := newConnectFake(t).withProfile()
	f.up, f.ready, f.inviteErr = true, true, localipc.NewError("scope_denied", "rejected")
	got := f.run("--json")
	if got.code != 2 || got.errorCode(t) != "scope_denied" || strings.Contains(got.stdout+got.stderr, "secret-fragment") {
		t.Fatalf("code=%d stdout=%s", got.code, got.stdout)
	}
}

func TestConnectLinkAppearsOnlyInTheIntendedResult(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR", "TMPDIR"} {
		t.Setenv(name, filepath.Join(root, strings.ToLower(name)))
		if err := os.MkdirAll(filepath.Join(root, strings.ToLower(name)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	f := newConnectFake(t)
	got := f.run("--relay", fakeRelay)
	if got.code != 0 || strings.Count(got.stdout, fakeLink) != 1 || got.stderr != "" {
		t.Fatalf("text result must carry the link exactly once: code=%d stdout=%s stderr=%q", got.code, got.stdout, got.stderr)
	}
	for _, event := range f.events {
		if strings.Contains(event, "secret-fragment") {
			t.Fatalf("link in operation log: %s", event)
		}
	}
	for _, part := range append(f.spawnArgv, f.spawnEnv...) {
		if strings.Contains(part, "secret-fragment") {
			t.Fatalf("link in spawn arguments: %s", part)
		}
	}
	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		if data, readErr := readInside(path); readErr != nil || strings.Contains(string(data), "secret-fragment") {
			t.Fatalf("link persisted in %s (err=%v)", path, readErr)
		}
		return nil
	})
	if walkErr != nil {
		t.Fatal(walkErr)
	}
}

func TestConnectArgumentsAndRoutingStayStrict(t *testing.T) {
	mcpRuntimeState(t)
	for _, args := range [][]string{{"connect", "--bogus"}, {"connect", "--relay"}, {"connect", "extra"}, {"connect", "--json", "--json"}, {"connect", "--current"}, {"connect", "--link", "https://x"}} {
		if _, err := parseCommand(args); err == nil {
			t.Fatalf("%v must be rejected", args)
		}
	}
	configPath, err := daemon.DefaultConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if got := runForTest([]string{"connect", "--relay", fakeRelay}); got != 3 {
		t.Fatalf("outside a native session the real command must refuse with unbound, got %d", got)
	}
	if _, statErr := os.Stat(configPath); !os.IsNotExist(statErr) {
		t.Fatalf("refusal created a profile: %v", statErr)
	}
	if got := runForTest([]string{"connect", "--bogus"}); got != 2 {
		t.Fatalf("invalid flags exit %d", got)
	}
}

func indexOf(list []string, want string) int {
	for i, item := range list {
		if item == want {
			return i
		}
	}
	return -1
}

func runForTest(args []string) int {
	var stdout, stderr bytes.Buffer
	return runInput(args, strings.NewReader(""), &stdout, &stderr)
}
