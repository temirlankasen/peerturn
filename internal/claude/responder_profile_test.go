package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestResponderRequiresExactDaemonAuthorityRoots(t *testing.T) {
	r := responderFixture(t)
	options := r.options
	for _, invalid := range []ResponderAuthorityPaths{
		{},
		{StateDirectory: options.AuthorityPaths.StateDirectory, ConfigFile: options.AuthorityPaths.ConfigFile},
		{StateDirectory: options.AuthorityPaths.StateDirectory, RuntimeDirectory: options.AuthorityPaths.RuntimeDirectory},
		{ConfigFile: options.AuthorityPaths.ConfigFile, RuntimeDirectory: options.AuthorityPaths.RuntimeDirectory},
		{StateDirectory: "relative", ConfigFile: options.AuthorityPaths.ConfigFile, RuntimeDirectory: options.AuthorityPaths.RuntimeDirectory},
		{StateDirectory: options.AuthorityPaths.StateDirectory + "/../other", ConfigFile: options.AuthorityPaths.ConfigFile, RuntimeDirectory: options.AuthorityPaths.RuntimeDirectory},
	} {
		options.AuthorityPaths = invalid
		if _, err := NewResponder(options); err == nil {
			t.Fatal("accepted incomplete or noncanonical daemon authority roots")
		}
	}
}

func TestResponderFixedProfileAndPrivateSettings(t *testing.T) {
	t.Setenv("PEERTURN_TOKEN", "daemon-fixture-not-for-child")
	t.Setenv("NODE_OPTIONS", "--this-must-not-be-inherited")
	t.Setenv("ENABLE_TOOL_SEARCH", "true")
	r := responderFixture(t)
	protected := filepath.Join(r.options.WorkingDirectory, "authority-state")
	r.options.ProtectedPaths = []string{protected}
	result, err := r.Run(t.Context(), AskRequest{Snapshot: responderSnapshotFixture(t, r), NativeSessionID: responderParent, FramedQuestion: "capture"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Answer != "captured" {
		t.Fatal("native fixture did not capture the fixed profile")
	}
	captureData, err := os.ReadFile(filepath.Join(r.options.HomeDirectory, "responder-capture.json"))
	if err != nil {
		t.Fatal(err)
	}
	var capture struct {
		Args     []string `json:"args"`
		Settings string   `json:"settings"`
		Mode     uint32   `json:"mode"`
		Env      []string `json:"env"`
	}
	if err := json.Unmarshal(captureData, &capture); err != nil {
		t.Fatal(err)
	}
	index := slices.Index(capture.Args, "--settings")
	if index < 0 || index+1 >= len(capture.Args) {
		t.Fatal("missing private settings")
	}
	settingsPath := capture.Args[index+1]
	expected := append(responderArgs(responderParent, settingsPath), "--resume-session-at", responderLeaf,
		"--model", "fixture-model", "--effort", "high", "--session-id", result.NativeSessionID)
	if !reflect.DeepEqual(capture.Args, expected) || capture.Mode != 0600 {
		t.Fatal("fixed argv or private settings permissions changed")
	}
	if _, err := os.Stat(filepath.Dir(settingsPath)); !os.IsNotExist(err) {
		t.Fatal("private settings survived completion")
	}
	var settings struct {
		Permissions struct {
			Deny []string `json:"deny"`
		} `json:"permissions"`
		DisableAllHooks bool            `json:"disableAllHooks"`
		EnabledPlugins  map[string]bool `json:"enabledPlugins"`
	}
	if err := json.Unmarshal([]byte(capture.Settings), &settings); err != nil {
		t.Fatal(err)
	}
	if !settings.DisableAllHooks || len(settings.EnabledPlugins) != len(responderBuiltinPlugins) {
		t.Fatal("inherited automation was enabled")
	}
	for _, path := range []string{
		filepath.Join(r.options.HomeDirectory, ".ssh"), filepath.Join(r.options.HomeDirectory, ".aws"),
		filepath.Join(r.options.HomeDirectory, ".config", "gh"), protected,
		r.options.AuthorityPaths.StateDirectory, filepath.Dir(r.options.AuthorityPaths.ConfigFile),
		r.options.AuthorityPaths.RuntimeDirectory,
	} {
		if !slices.Contains(settings.Permissions.Deny, "Read(/"+filepath.ToSlash(path)+")") ||
			!slices.Contains(settings.Permissions.Deny, "Read(/"+filepath.ToSlash(path)+"/**)") {
			t.Fatal("protected root is missing an absolute Read deny")
		}
	}
	if !slices.Contains(settings.Permissions.Deny, "Read(/"+r.options.WorkingDirectory+"/**/.env*)") {
		t.Fatal("nested project .env files are unprotected")
	}
	for _, entry := range capture.Env {
		if strings.HasPrefix(entry, "PEERTURN_") || strings.HasPrefix(entry, "NODE_OPTIONS=") {
			t.Fatal("daemon environment entered child")
		}
	}
	if !slices.Contains(capture.Env, "ENABLE_TOOL_SEARCH=false") || slices.Contains(capture.Env, "ENABLE_TOOL_SEARCH=true") {
		t.Fatal("native deferred tool loading was not disabled in the fork")
	}
}

func TestResponderProtectsRealDaemonRootsThroughExistingAncestor(t *testing.T) {
	r := responderFixture(t)
	realRoot := t.TempDir()
	alias := filepath.Join(t.TempDir(), "selected")
	if err := os.Symlink(realRoot, alias); err != nil {
		t.Fatal(err)
	}
	r.options.AuthorityPaths = ResponderAuthorityPaths{
		StateDirectory: filepath.Join(alias, "state"), ConfigFile: filepath.Join(alias, "config", "config.toml"),
		RuntimeDirectory: filepath.Join(alias, "runtime"),
	}
	directory, settingsPath, err := responderSettings(r.options)
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
	data, err := root.ReadFile(filepath.Base(settingsPath))
	if closeErr := root.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Permissions struct {
			Deny []string `json:"deny"`
		} `json:"permissions"`
	}
	if err = json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(realRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, base := range []string{alias, resolved} {
		for _, name := range []string{"state", "config", "runtime"} {
			path := "/" + filepath.ToSlash(filepath.Join(base, name))
			if !slices.Contains(settings.Permissions.Deny, "Read("+path+")") ||
				!slices.Contains(settings.Permissions.Deny, "Read("+path+"/**)") {
				t.Fatal("missing declared or real daemon authority directory")
			}
		}
	}
}

func TestResponderFinalAnswerIsRedacted(t *testing.T) {
	r := responderFixture(t)
	r.options.KnownSecrets = []string{"core-fixture-only"}
	result, err := r.Run(t.Context(), AskRequest{Snapshot: responderSnapshotFixture(t, r), NativeSessionID: responderParent, FramedQuestion: "redact"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Redactions != 2 || result.Answer != "[REDACTED:known] and [REDACTED:github]" {
		t.Fatal("whole-answer outbound policy was bypassed")
	}
}

func TestResponderCapacityAndSlotRelease(t *testing.T) {
	r := responderFixture(t)
	r.options.MaxConcurrent = 1
	// t.Fatal ends only the calling goroutine, so a fixture failure inside the
	// worker would strand the wait below; prepare it on the test goroutine.
	first := responderSnapshotFixture(t, r)
	started, release := make(chan struct{}), make(chan struct{})
	releaseRun := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseRun)
	var calls atomic.Int32
	r.options.AuthorityCheck = func(context.Context) error {
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		return nil
	}
	done := make(chan error, 1)
	go func() {
		_, err := r.Run(t.Context(), AskRequest{Snapshot: first, NativeSessionID: responderParent, FramedQuestion: "success"})
		done <- err
	}()
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("first run ended before holding the slot: %v", err)
	}
	other := "cccccccc-dddd-4eee-8fff-aaaaaaaaaaaa"
	result, err := r.Run(t.Context(), AskRequest{Snapshot: responderSnapshotFixture(t, r, other), NativeSessionID: other, FramedQuestion: "success"})
	requireResponderFailure(t, result, err, ResponderBusy)
	releaseRun()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(t.Context(), AskRequest{Snapshot: responderSnapshotFixture(t, r, other), NativeSessionID: other, FramedQuestion: "success"}); err != nil {
		t.Fatal("completed run retained its slot")
	}
}

func TestResponderNativeResultRejectsAmbiguousJSON(t *testing.T) {
	base := `{"type":"result","subtype":"success","is_error":false,"session_id":"bbbbbbbb-cccc-4ddd-8eee-ffffffffffff","result":"answer"}`
	for _, data := range []string{
		strings.Replace(base, `"is_error":false,`, "", 1),
		strings.Replace(base, `"is_error":false`, `"is_error":null`, 1),
		strings.Replace(base, `"type":"result"`, `"TYPE":"result"`, 1),
		strings.Replace(base, `"type":"result"`, `"type":"assistant","type":"result"`, 1),
		strings.Replace(base, `"answer"`, `"\ud800"`, 1),
		strings.Replace(base, `"answer"`, `{}`, 1),
		base + base,
	} {
		if answer, err := decodeResponderAnswer([]byte(data), responderParent, 4096); err == nil || answer != "" {
			t.Fatal("ambiguous native output was accepted")
		}
	}
}
