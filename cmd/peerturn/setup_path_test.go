package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/runtimepolicy"
)

func TestSetupCanonicalRootBeforeHostMutation(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		t.Run(host, func(t *testing.T) {
			root, pin, prefix := setupObservedFixture(t, host)
			canonical, err := filepath.EvalSymlinks(root)
			if err != nil {
				t.Fatal(err)
			}
			alias := filepath.Join(t.TempDir(), "release-alias")
			if err = os.Symlink(canonical, alias); err != nil {
				t.Fatal(err)
			}
			marketplaces, plugins := observedSetupJSON(t, host, canonical, filepath.Join(canonical, prefix))
			t.Setenv("SETUP_MARKETPLACES", marketplaces)
			t.Setenv("SETUP_PLUGINS", plugins)
			calls := filepath.Join(t.TempDir(), "calls")
			t.Setenv("SETUP_CALLS", calls)
			var stdout, stderr bytes.Buffer
			code := runSetup([]string{host, "--path", alias, "--sha256", pin}, &stdout, &stderr)
			if code != 0 {
				t.Fatalf("canonical package alias refused: code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
			data := readSetupHostCalls(t, calls)
			if strings.Contains(string(data), alias) || !strings.Contains(string(data), "plugin marketplace add "+canonical+"\n") || !strings.Contains(string(data), filepath.Join(canonical, "peerturn")) {
				t.Fatalf("host mutation used noncanonical root: %s", data)
			}
			if !sameSetupPath(alias, canonical) || !sameSetupPath(canonical, alias) {
				t.Fatal("host path observations compare aliases lexically")
			}
		})
	}
}

func TestSetupPathRefusalBeforeHostEffects(t *testing.T) {
	for _, mode := range []string{"missing", "loop", "file"} {
		t.Run(mode, func(t *testing.T) {
			root, pin, _ := setupObservedFixture(t, "codex")
			path := filepath.Join(t.TempDir(), "unusable-package")
			var err error
			switch mode {
			case "loop":
				err = os.Symlink(path, path)
			case "file":
				err = os.WriteFile(path, nil, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			calls := filepath.Join(t.TempDir(), "calls")
			t.Setenv("SETUP_CALLS", calls)
			var stdout, stderr bytes.Buffer
			code := runSetup([]string{"codex", "--path", path, "--sha256", pin}, &stdout, &stderr)
			var result struct{ Error *localipc.Error }
			if code == 0 || json.Unmarshal(stdout.Bytes(), &result) != nil || result.Error == nil || result.Error.Code != "setup_path_invalid" || result.Error.State != "needs_configuration" || !strings.Contains(result.Error.Message, "--path") {
				t.Fatalf("unactionable path refusal: code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
			if _, err = os.Stat(calls); !os.IsNotExist(err) {
				t.Fatalf("invalid package path invoked host: %v", err)
			}
			if sameSetupPath(path, root) || sameSetupPath(path, path) || sameSetupPath("relative", root) {
				t.Fatal("invalid reported path matched a package")
			}
		})
	}
}

func TestSetupMarketplaceConflictBeforeHostMutation(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		for _, mode := range []string{"foreign", "malformed", "null", "missing_listing", "null_entry", "empty_entry"} {
			t.Run(host+"/"+mode, func(t *testing.T) {
				root, pin, prefix := setupObservedFixture(t, host)
				foreign, _ := observedSetupJSON(t, host, t.TempDir(), filepath.Join(root, prefix))
				switch mode {
				case "malformed":
					foreign = "{"
				case "null":
					foreign = "null"
				case "missing_listing":
					foreign = "{}"
				case "null_entry", "empty_entry":
					entry := "null"
					if mode == "empty_entry" {
						entry = "{}"
					}
					foreign = "[" + entry + "]"
					if host == "codex" {
						foreign = `{"marketplaces":` + foreign + "}"
					}
				}
				t.Setenv("SETUP_MARKETPLACES", foreign)
				calls := filepath.Join(t.TempDir(), "calls")
				t.Setenv("SETUP_CALLS", calls)
				var stdout, stderr bytes.Buffer
				code := runSetup([]string{host, "--path", root, "--sha256", pin}, &stdout, &stderr)
				var result struct{ Error *localipc.Error }
				if code == 0 || json.Unmarshal(stdout.Bytes(), &result) != nil || result.Error == nil || result.Error.Code != "marketplace_conflict" || result.Error.State != "needs_configuration" || !strings.Contains(result.Error.Message, "plugin marketplace list --json") {
					t.Fatalf("unactionable marketplace refusal: code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
				}
				data := readSetupHostCalls(t, calls)
				if string(data) != "--version\nplugin marketplace list --json\n" {
					t.Fatalf("marketplace refusal changed host: calls=%s", data)
				}
			})
		}
	}
}

func readSetupHostCalls(t *testing.T, calls string) []byte {
	t.Helper()
	root, err := os.OpenRoot(filepath.Dir(calls))
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := root.ReadFile("calls")
	closeErr := root.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read setup fixture calls: read=%v close=%v", readErr, closeErr)
	}
	return data
}

func TestSetupMarketplaceAbsenceAllowsRegistration(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		t.Run(host, func(t *testing.T) {
			root, _, prefix := setupObservedFixture(t, host)
			marketplaces, plugins := observedSetupJSON(t, host, root, filepath.Join(root, prefix))
			var calls []string
			added := false
			invoke := func(_ context.Context, _ string, args []string, output io.Writer) error {
				call := strings.Join(args, " ")
				calls = append(calls, call)
				switch call {
				case "--version":
					_, err := io.WriteString(output, runtimepolicy.Evaluate(host, "").Minimum)
					return err
				case "plugin marketplace list --json":
					listing := marketplaces
					if !added {
						listing = "[]"
						if host == "codex" {
							listing = `{"marketplaces":[]}`
						}
					}
					_, err := io.WriteString(output, listing)
					return err
				case "plugin marketplace add " + root:
					added = true
				case "plugin list --json":
					_, err := io.WriteString(output, plugins)
					return err
				case "mcp list --json":
					_, err := io.WriteString(output, "[]")
					return err
				case "mcp get peerturn", "mcp get peerturn --json":
					return absentSetupMCP(host, output)
				}
				return nil
			}
			var stdout bytes.Buffer
			if err := installSetup(t.Context(), host, root, &stdout, invoke); err != nil {
				t.Fatal(err)
			}
			if !added || len(calls) < 3 || calls[1] != "plugin marketplace list --json" || calls[2] != "plugin marketplace add "+root || !strings.Contains(stdout.String(), "matched") {
				t.Fatalf("observable absence did not allow setup: %v", calls)
			}
		})
	}
}
