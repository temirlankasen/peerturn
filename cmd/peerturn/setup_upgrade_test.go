package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/runtimepolicy"
)

// upgradeSetupRelease writes a verified release into the installer's version
// directory, where an older sibling counts as PeerTurn's own package.
func upgradeSetupRelease(t *testing.T, host string) (root, prefix string) {
	t.Helper()
	home := filepath.Dir(setupMCPHome(t))
	root = filepath.Join(home, ".local", "share", "peerturn", "0.2.0")
	prefix, manifest := "adapters/claude", ".claude-plugin/marketplace.json"
	if host == "codex" {
		prefix, manifest = "adapters/codex/plugin", ".agents/plugins/marketplace.json"
	}
	var inventory strings.Builder
	for _, file := range [][2]string{{manifest, `{"name":"peerturn"}`}, {prefix + "/hooks/hook", "expected hook"}} {
		writeSetupTestFile(t, filepath.Join(root, file[0]), file[1])
		fmt.Fprintf(&inventory, "%x  %s\n", sha256.Sum256([]byte(file[1])), file[0])
	}
	writeSetupTestFile(t, filepath.Join(root, ".peerturn-files.sha256"), inventory.String())
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return root, prefix
}

// existingSetupMarketplace is the host listing before setup runs.
func existingSetupMarketplace(t *testing.T, host, root, prefix, kind string) string {
	t.Helper()
	packages := filepath.Dir(root)
	path := map[string]string{
		"older_package":   filepath.Join(packages, "0.1.4-preview"),
		"removed_package": filepath.Join(packages, "0.1.2-preview"),
		"newer_package":   filepath.Join(packages, "0.3.0"),
		"custom_location": filepath.Join(t.TempDir(), "0.1.4-preview"),
	}[kind]
	var entry map[string]any
	switch kind {
	case "published_other_ref", "foreign_repository":
		path = t.TempDir()
		manifest := ".claude-plugin/marketplace.json"
		if host == "codex" {
			manifest = ".agents/plugins/marketplace.json"
		}
		content := `{"name":"peerturn"}`
		if kind == "published_other_ref" {
			content = `{"name":"peerturn","plugins":[]}`
		}
		writeSetupTestFile(t, filepath.Join(path, manifest), content)
		writeSetupTestFile(t, filepath.Join(path, prefix, "hooks", "hook"), "expected hook")
		repo, url := setupPublishedRepo, setupPublishedURL
		if kind == "foreign_repository" {
			repo, url = "someone/peerturn", "https://github.com/someone/peerturn.git"
		}
		entry = map[string]any{"name": "peerturn", "source": "github", "repo": repo, "installLocation": path}
		if host == "codex" {
			entry = map[string]any{"name": "peerturn", "root": path, "marketplaceSource": map[string]string{"sourceType": "git", "source": url}}
		}
	default:
		if kind != "removed_package" {
			writeSetupTestFile(t, filepath.Join(path, "peerturn"), "previous executable")
		}
		entry = map[string]any{"name": "peerturn", "source": "directory", "path": path, "installLocation": path}
		if host == "codex" {
			entry = map[string]any{"name": "peerturn", "root": path, "marketplaceSource": map[string]string{"sourceType": "local", "source": path}}
		}
	}
	var listing any = []any{entry}
	if host == "codex" {
		listing = map[string]any{"marketplaces": []any{entry}}
	}
	data, err := json.Marshal(listing)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func upgradeSetup(t *testing.T, host, kind string) (string, []string, error) {
	t.Helper()
	root, prefix := upgradeSetupRelease(t, host)
	listing := existingSetupMarketplace(t, host, root, "adapters/claude", "older_package")
	if !strings.Contains(kind, "disabled") && kind != "not_installed" {
		listing = existingSetupMarketplace(t, host, root, prefix, kind)
	}
	current, plugins := observedSetupJSON(t, host, root, filepath.Join(root, prefix))
	switch kind {
	case "disabled":
		plugins = strings.Replace(plugins, `"enabled":true`, `"enabled":false`, 1)
	case "not_installed":
		plugins = "[]"
	case "user_disabled", "cowork_disabled":
		name := map[string]string{"user_disabled": "settings.json", "cowork_disabled": "cowork_settings.json"}[kind]
		home := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(root))))
		writeSetupTestFile(t, filepath.Join(home, ".claude", name), `{"enabledPlugins":{"peerturn@peerturn":false}}`)
	case "project_disabled":
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		writeSetupTestFile(t, filepath.Join(cwd, ".claude", "settings.json"), `{"enabledPlugins":{"peerturn@peerturn":false}}`)
		writeSetupTestFile(t, filepath.Join(cwd, ".claude", "settings.local.json"), `{"enabledPlugins":{"peerturn@peerturn":true}}`)
	}
	empty := "[]"
	if host == "codex" {
		empty = `{"marketplaces":[]}`
	}
	var calls []string
	invoke := func(_ context.Context, _ string, args []string, output io.Writer) error {
		call := strings.Join(args, " ")
		calls = append(calls, call)
		var err error
		switch call {
		case "--version":
			_, err = io.WriteString(output, runtimepolicy.Evaluate(host, "").Minimum)
		case "plugin marketplace list --json":
			_, err = io.WriteString(output, listing)
		case "plugin marketplace remove peerturn":
			listing = empty
		case "plugin marketplace add " + root:
			listing = current
		case "plugin list --json":
			_, err = io.WriteString(output, plugins)
		case "mcp list --json":
			_, err = io.WriteString(output, "[]")
		case "mcp get peerturn", "mcp get peerturn --json":
			err = absentSetupMCP(host, output)
		}
		return err
	}
	err := installSetup(t.Context(), host, root, io.Discard, invoke, false)
	return root, calls, err
}

func TestSetupReplacesOwnStaleMarketplace(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		for _, kind := range []string{"older_package", "removed_package", "published_other_ref"} {
			t.Run(host+"/"+kind, func(t *testing.T) {
				root, calls, err := upgradeSetup(t, host, kind)
				if err != nil {
					t.Fatalf("setup did not upgrade its own marketplace: %v %v", err, calls)
				}
				want := []string{"plugin marketplace list --json", "plugin marketplace remove peerturn", "plugin marketplace add " + root}
				if host == "claude" {
					want = slices.Insert(want, 1, "plugin list --json")
				}
				if len(calls) < len(want)+1 || !slices.Equal(calls[1:len(want)+1], want) {
					t.Fatalf("stale marketplace was not replaced before adding this release: %v", calls)
				}
			})
		}
	}
}

// Claude's marketplace remove deletes enabledPlugins entries, so an owner's
// disable must stop the replacement before any change.
func TestSetupKeepsDisabledClaudePluginUntouched(t *testing.T) {
	for _, kind := range []string{"disabled", "not_installed", "user_disabled", "cowork_disabled", "project_disabled"} {
		t.Run(kind, func(t *testing.T) {
			_, calls, err := upgradeSetup(t, "claude", kind)
			problem, typed := errors.AsType[*localipc.Error](err)
			if !typed || problem.Code != "plugin_disabled" {
				t.Fatalf("disabled plugin did not stop the upgrade: %v", err)
			}
			for _, call := range calls {
				if strings.HasPrefix(call, "plugin marketplace remove") || strings.HasPrefix(call, "plugin marketplace add") || strings.HasPrefix(call, "plugin install") {
					t.Fatalf("setup changed a disabled plugin's marketplace: %v", calls)
				}
			}
		})
	}
}

func TestSetupKeepsForeignMarketplaceConflict(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		for _, kind := range []string{"newer_package", "custom_location", "foreign_repository"} {
			t.Run(host+"/"+kind, func(t *testing.T) {
				_, calls, err := upgradeSetup(t, host, kind)
				problem, typed := errors.AsType[*localipc.Error](err)
				if !typed || problem.Code != "marketplace_conflict" {
					t.Fatalf("foreign marketplace was not a conflict: %v", err)
				}
				for _, call := range calls {
					if strings.HasPrefix(call, "plugin marketplace remove") || strings.HasPrefix(call, "plugin marketplace add") {
						t.Fatalf("setup changed a foreign marketplace: %v", calls)
					}
				}
			})
		}
	}
}
