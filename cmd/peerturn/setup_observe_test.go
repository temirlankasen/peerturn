package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupObservedFixture(t *testing.T, host string) (string, string, string) {
	t.Helper()
	setupMCPHome(t)
	root := filepath.Join(t.TempDir(), "release with spaces")
	prefix, manifest := "adapters/claude", ".claude-plugin/marketplace.json"
	if host == "codex" {
		prefix, manifest = "adapters/codex/plugin", ".agents/plugins/marketplace.json"
	}
	files := map[string]string{manifest: `{"name":"peerturn"}`, prefix + "/hooks/hook": "expected hook"}
	var inventory strings.Builder
	for path, content := range files {
		destination := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&inventory, "%x  %s\n", sha256.Sum256([]byte(content)), path)
	}
	if err := os.WriteFile(filepath.Join(root, ".peerturn-files.sha256"), []byte(inventory.String()), 0600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	program := "#!/bin/sh\n[ -z \"$SETUP_CALLS\" ] || printf '%s\\n' \"$*\" >> \"$SETUP_CALLS\"\ncase \"$*\" in\n--version) printf '%s' \"$SETUP_VERSION\"; printf '%s' \"$SETUP_VERSION_STDERR\" >&2; exit \"${SETUP_VERSION_EXIT:-0}\";;\n'plugin marketplace list --json') printf '%s' \"$SETUP_MARKETPLACES\";;\n'plugin list --json') printf '%s' \"$SETUP_PLUGINS\";;\n'mcp list --json') printf '%s' \"${SETUP_MCP_LIST_OUTPUT:-[]}\"; exit \"${SETUP_MCP_LIST_EXIT:-0}\";;\n'mcp get peerturn'|'mcp get peerturn --json') printf '%s' \"$SETUP_MCP_GET_OUTPUT\"; exit \"${SETUP_MCP_GET_EXIT:-1}\";;\nesac\nif [ \"$1 $2\" = 'mcp add' ]; then exit \"${SETUP_MCP_EXIT:-0}\"; fi\n"
	directory, err := os.OpenRoot(bin)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := directory.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	file, err := directory.OpenFile(host, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.WriteString(program)
	modeErr := file.Chmod(0700)
	closeErr := file.Close()
	if writeErr != nil || modeErr != nil || closeErr != nil {
		t.Fatalf("setup host fixture: write=%v mode=%v close=%v", writeErr, modeErr, closeErr)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	marketplaces, plugins := observedSetupJSON(t, host, root, filepath.Join(root, prefix))
	t.Setenv("SETUP_MARKETPLACES", marketplaces)
	t.Setenv("SETUP_PLUGINS", plugins)
	nativeVersion := "2.1.306 (Claude Code)"
	if host == "codex" {
		nativeVersion = "codex-cli 0.163.0"
	}
	t.Setenv("SETUP_VERSION", nativeVersion)
	t.Setenv("SETUP_MCP_GET_OUTPUT", absentSetupMCPMessage(host))
	return root, fmt.Sprintf("%x", sha256.Sum256([]byte(inventory.String()))), prefix
}

func observedSetupJSON(t *testing.T, host, root, plugin string) (string, string) {
	t.Helper()
	var marketplaces, plugins any
	if host == "claude" {
		marketplaces = []any{map[string]any{"name": "peerturn", "source": "directory", "path": root, "installLocation": root}}
		plugins = []any{map[string]any{"id": "peerturn@peerturn", "installPath": plugin}}
	} else {
		marketplaceSource := map[string]string{"sourceType": "local", "source": root}
		marketplaces = map[string]any{"marketplaces": []any{map[string]any{"name": "peerturn", "root": root, "marketplaceSource": marketplaceSource}}}
		plugins = map[string]any{"installed": []any{map[string]any{"name": "peerturn", "marketplaceName": "peerturn", "installed": true, "source": map[string]string{"source": "local", "path": plugin}, "marketplaceSource": marketplaceSource}}}
	}
	encode := func(value any) string {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	return encode(marketplaces), encode(plugins)
}

func TestSetupRejectsForeignOrUnobservableInstall(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		for _, failure := range []string{"marketplace", "plugin", "malformed", "missing", "oversized"} {
			t.Run(host+"/"+failure, func(t *testing.T) {
				root, pin, prefix := setupObservedFixture(t, host)
				switch failure {
				case "marketplace":
					foreign, _ := observedSetupJSON(t, host, "/foreign", "/foreign/plugin")
					t.Setenv("SETUP_MARKETPLACES", foreign)
				case "plugin":
					_, foreign := observedSetupJSON(t, host, root, filepath.Join(t.TempDir(), prefix))
					t.Setenv("SETUP_PLUGINS", foreign)
				case "malformed":
					t.Setenv("SETUP_PLUGINS", "{")
				case "missing":
					t.Setenv("SETUP_PLUGINS", "[]")
				case "oversized":
					t.Setenv("SETUP_PLUGINS", strings.Repeat(" ", 65537))
				}
				var stdout, stderr bytes.Buffer
				if code := runSetup([]string{host, "--path", root, "--sha256", pin}, &stdout, &stderr); code == 0 {
					t.Fatal("setup accepted foreign or unobservable host installation")
				}
			})
		}
	}
}

func TestSetupObservedContentMatched(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		t.Run(host, func(t *testing.T) {
			root, pin, _ := setupObservedFixture(t, host)
			var stdout, stderr bytes.Buffer
			if code := runSetup([]string{host, "--path", root, "--sha256", pin}, &stdout, &stderr); code != 0 {
				t.Fatalf("setup failed: %s", stderr.String())
			}
			if !strings.Contains(stdout.String(), "matched") || !strings.Contains(stdout.String(), "not independent publisher authentication") {
				t.Fatal("self-consistency was presented as publisher verification")
			}
		})
	}
}

func TestSetupMatchesCachedPluginContents(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		for _, change := range []string{"unchanged", "changed", "missing", "extra", "symlink"} {
			t.Run(host+"/"+change, func(t *testing.T) {
				root, _, prefix := setupObservedFixture(t, host)
				cache := t.TempDir()
				if err := os.Mkdir(filepath.Join(cache, "hooks"), 0700); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(cache, "hooks", "hook")
				if err := os.WriteFile(path, []byte("expected hook"), 0600); err != nil {
					t.Fatal(err)
				}
				var err error
				switch change {
				case "changed":
					err = os.WriteFile(path, []byte("foreign hook"), 0600)
				case "missing":
					err = os.Remove(path)
				case "extra":
					err = os.WriteFile(filepath.Join(cache, "extra"), []byte("foreign"), 0600)
				case "symlink":
					if err = os.Remove(path); err == nil {
						err = os.Symlink(filepath.Join(root, prefix, "hooks", "hook"), path)
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				if matched := matchSetupPlugin(root, prefix, cache) == nil; matched != (change == "unchanged") {
					t.Fatalf("cache change %s: matched=%v", change, matched)
				}
			})
		}
	}
}
