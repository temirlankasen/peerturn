package daemon

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestAskPlatformQualification(t *testing.T) {
	for platform, want := range map[string]string{"linux": "", "darwin": "macos_unqualified", "windows": "platform_unqualified"} {
		if got := responderPlatformFailure(platform); got != want {
			t.Fatalf("platform %s: got %s, want %s", platform, got, want)
		}
	}
}

func TestResponderStartupReportsFileAuthorityWithoutSecrets(t *testing.T) {
	dir := stateDir(t)
	_, err := Initialize(t.Context(), InitOptions{StateDir: dir, RelayURL: "https://relay.example", SessionLabel: "fixture", FilePermissions: true})
	if err != nil {
		t.Fatal(err)
	}
	d, err := Open(t.Context(), Options{StateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := d.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	value, err := d.Dispatch(t.Context(), localipc.Request{Method: "status", Session: "fixture"})
	status, ok := value.(Status)
	if err != nil || !ok {
		t.Fatal("status unavailable", err)
	}
	if status.Responder.CheckedAt == 0 || status.Responder.StorageMode != "file_permissions" ||
		status.Responder.Available || status.Capabilities.AuthoritySecretsIsolated || status.Capabilities.ForkResponder ||
		status.ResponderReason != "wrapping_key_not_isolated" {
		t.Fatalf("unqualified authority advertised: %+v", status.Responder)
	}
	if len(status.Responder.Failures) == 0 || status.Responder.Failures[0] != "wrapping_key_not_isolated" {
		t.Fatal("missing actual failure", status.Responder)
	}
	raw, err := json.Marshal(status.Responder)
	if err != nil || strings.Contains(string(raw), dir) || strings.Contains(string(raw), "seal.key") {
		t.Fatal("preflight exposes local paths", err)
	}
}

func TestResponderDirectoriesFollowActualXDGAndCustomState(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", filepath.Join(base, "xdg-state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(base, "xdg-config"))
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(base, "xdg-runtime"))
	d := &Daemon{options: Options{StateDir: filepath.Join(base, "custom-state"), RuntimeDir: filepath.Join(base, "custom-runtime")}}
	paths, extra, err := d.responderDirectories()
	if err != nil {
		t.Fatal(err)
	}
	if paths.StateDirectory != d.options.StateDir || paths.RuntimeDirectory != d.options.RuntimeDir ||
		paths.ConfigFile != filepath.Join(base, "xdg-config", "peerturn", "config.toml") {
		t.Fatal("actual directories lost", paths)
	}
	for _, want := range []string{filepath.Join(base, "xdg-state", "peerturn"), filepath.Join(base, "xdg-runtime", "peerturn")} {
		found := slices.ContainsFunc(extra, func(path string) bool {
			return path == want || strings.HasPrefix(path, want+string(filepath.Separator))
		})
		if !found {
			t.Fatal("default authority root omitted")
		}
	}
}
