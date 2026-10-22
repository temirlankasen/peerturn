package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type responderInputCapture struct{ bytes.Buffer }

func (*responderInputCapture) Close() error { return nil }

func TestResponderSyntheticAssistantIsNotModelInput(t *testing.T) {
	r := responderFixture(t)
	result := AskResult{NativeSessionID: "bbbbbbbb-cccc-4ddd-8eee-ffffffffffff"}
	stream := responderStream{ctx: t.Context(), request: AskRequest{Snapshot: responderSnapshotFixture(t, r)},
		responder: r, result: &result, initialized: true, peerInput: true, input: &responderInputCapture{}}
	line := `{"type":"assistant","session_id":"bbbbbbbb-cccc-4ddd-8eee-ffffffffffff","error":"authentication_failed","message":{"role":"assistant","model":"fixture-model","content":[{"type":"text","text":"error"}]}}`
	if err := stream.event([]byte(line)); err == nil || len(result.Stages) != 0 {
		t.Fatal("native synthetic error was claimed as model input")
	}
}

func TestResponderRequiresFreshSerializedPeerAdmission(t *testing.T) {
	for _, admit := range []func(context.Context, string, func() error) error{nil,
		func(context.Context, string, func() error) error { return context.Canceled }} {
		r := responderFixture(t)
		r.options.AdmitPeerInput = admit
		result, err := r.Run(t.Context(), AskRequest{Snapshot: responderSnapshotFixture(t, r),
			NativeSessionID: responderParent, FramedQuestion: "success"})
		if err == nil || len(result.Stages) != 1 || result.Stages[0] != ResponderStarted || result.Answer != "" {
			t.Fatal("peer input passed missing or failed admission")
		}
	}
}

func TestResponderManagedDocumentsFailBeforeLaunch(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"managed-settings.json", "managed-mcp.json", "remote-settings.json"} {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte(`{"hooks":{"SessionStart":[]}}`), 0600); err != nil {
			t.Fatal(err)
		}
		if err := checkManagedDocument(path); err == nil || !strings.Contains(err.Error(), "managed_policy_present") {
			t.Fatal("managed policy did not fail closed")
		}
		if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
			t.Fatal(err)
		}
		if err := checkManagedDocument(path); err != nil {
			t.Fatal("empty policy was rejected")
		}
	}
	for _, raw := range []string{`null`, `[]`, `{"hooks":{},"hooks":{}}`, `{`} {
		path := filepath.Join(root, "invalid.json")
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if err := checkManagedDocument(path); err == nil {
			t.Fatal("unverified managed document accepted")
		}
	}
}

func TestResponderInitInventoryIsExact(t *testing.T) {
	valid := `{"type":"system","subtype":"init","session_id":"bbbbbbbb-cccc-4ddd-8eee-ffffffffffff","model":"fixture-model","permissionMode":"default","tools":["Read","Grep","Glob"],"mcp_servers":[],"plugins":[],"skills":[]}`
	if _, err := verifyResponderInit([]byte(valid), "fixture-model", responderParent); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		strings.Replace(valid, `"Read","Grep","Glob"`, `"Read","Grep","Glob","Bash"`, 1),
		strings.Replace(valid, `"Read","Grep","Glob"`, `"Read","Read","Glob"`, 1),
		strings.Replace(valid, `"mcp_servers":[]`, `"mcp_servers":[{}]`, 1),
		strings.Replace(valid, `"plugins":[]`, `"plugins":[{"name":"managed"}]`, 1),
		strings.Replace(valid, `"skills":[]`, `"skills":null`, 1),
		strings.Replace(valid, `"permissionMode":"default"`, `"permissionMode":"bypassPermissions"`, 1),
		strings.Replace(valid, `"fixture-model"`, `"different-model"`, 1),
		strings.Replace(valid, `"model":"fixture-model",`, ``, 1),
	} {
		if _, err := verifyResponderInit([]byte(raw), "fixture-model", responderParent); err == nil {
			t.Fatal("unexpected native inventory accepted")
		}
	}
}

func TestResponderProfileScopesAndDisablesBuiltins(t *testing.T) {
	r := responderFixture(t)
	directory, path, err := responderSettings(r.options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cleanupErr := os.RemoveAll(directory); cleanupErr != nil {
			t.Error(cleanupErr)
		}
	})
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	data, err := root.ReadFile(filepath.Base(path))
	if closeErr := root.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Permissions struct{ Allow []string } `json:"permissions"`
		Plugins     map[string]bool          `json:"enabledPlugins"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(settings.Permissions.Allow, "Read(/"+filepath.ToSlash(r.options.WorkingDirectory)+"/**)") {
		t.Fatal("root-scoped Read allow missing")
	}
	for _, name := range responderBuiltinPlugins {
		enabled, found := settings.Plugins[name]
		if !found || enabled {
			t.Fatal("builtin plugin not explicitly disabled")
		}
	}
	args := responderArgs(responderParent, path)
	for _, flag := range []string{"--restricted", "--permission-prompts", "--input-format", "--verbose"} {
		if !slices.Contains(args, flag) {
			t.Fatal("required restrictive profile flag absent")
		}
	}
}

func TestResponderRedactionAndAnswerCapIs16KiB(t *testing.T) {
	r := responderFixture(t)
	options := r.options
	options.MaxOutputBytes = (16 << 10) + 1
	if _, err := NewResponder(options); err == nil {
		t.Fatal("accepted final output above hard cap")
	}
	if _, _, err := redactAnswer(strings.Repeat("x", (16<<10)+1), nil); err == nil {
		t.Fatal("redaction scans oversized native answer")
	}
	result, err := r.Run(context.Background(), AskRequest{NativeSessionID: responderParent, FramedQuestion: "success"})
	requireResponderFailure(t, result, err, SnapshotUnavailable)
}
