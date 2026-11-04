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

// addSetupReleaseFile extends the verified fixture and returns the new pin.
func addSetupReleaseFile(t *testing.T, root, path, content string) string {
	t.Helper()
	writeSetupTestFile(t, filepath.Join(root, path), content)
	release, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := release.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	data, err := release.ReadFile(".peerturn-files.sha256")
	if err != nil {
		t.Fatal(err)
	}
	data = fmt.Appendf(data, "%x  %s\n", sha256.Sum256([]byte(content)), path)
	if err = release.WriteFile(".peerturn-files.sha256", data, 0600); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func writeSetupTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

// publishedSetupFixture models a marketplace added by native plugin commands
// from the public repository rather than by setup from the release directory.
func publishedSetupFixture(t *testing.T, host, change string) (root, pin, calls string) {
	t.Helper()
	root, pin, prefix := setupObservedFixture(t, host)
	if host == "codex" {
		pin = addSetupReleaseFile(t, root, prefix+"/bin/peerturn", "bundled helper")
	}
	clone := t.TempDir()
	manifest := ".claude-plugin/marketplace.json"
	if host == "codex" {
		manifest = ".agents/plugins/marketplace.json"
	}
	writeSetupTestFile(t, filepath.Join(clone, manifest), `{"name":"peerturn"}`)
	writeSetupTestFile(t, filepath.Join(clone, prefix, "hooks", "hook"), "expected hook")
	plugin := filepath.Join(clone, prefix)
	if host == "claude" {
		plugin = t.TempDir()
		writeSetupTestFile(t, filepath.Join(plugin, "hooks", "hook"), "expected hook")
	}
	repo, url := "temirlankasen/peerturn", "https://github.com/temirlankasen/peerturn.git"
	switch change {
	case "manifest":
		writeSetupTestFile(t, filepath.Join(clone, manifest), `{"name":"peerturn","plugins":[]}`)
	case "repository":
		repo, url = "someone/peerturn", "https://github.com/someone/peerturn.git"
	case "hook":
		writeSetupTestFile(t, filepath.Join(plugin, "hooks", "hook"), "foreign hook")
	case "outside":
		plugin = t.TempDir()
		writeSetupTestFile(t, filepath.Join(plugin, "hooks", "hook"), "expected hook")
	case "foreign-helper":
		writeSetupTestFile(t, filepath.Join(plugin, "bin", "peerturn"), "foreign helper")
	}
	var marketplaces, plugins any
	if host == "claude" {
		marketplaces = []any{map[string]any{"name": "peerturn", "source": "github", "repo": repo, "ref": "v0.1.6-preview", "installLocation": clone}}
		plugins = []any{map[string]any{"id": "peerturn@peerturn", "installPath": plugin}}
	} else {
		source := map[string]string{"sourceType": "git", "source": url}
		marketplaces = map[string]any{"marketplaces": []any{map[string]any{"name": "peerturn", "root": clone, "marketplaceSource": source}}}
		plugins = map[string]any{"installed": []any{map[string]any{"name": "peerturn", "marketplaceName": "peerturn", "installed": true, "source": map[string]string{"source": "local", "path": plugin}, "marketplaceSource": source}}}
	}
	for name, value := range map[string]any{"SETUP_MARKETPLACES": marketplaces, "SETUP_PLUGINS": plugins} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv(name, string(data))
	}
	calls = filepath.Join(t.TempDir(), "calls")
	t.Setenv("SETUP_CALLS", calls)
	return root, pin, calls
}

func TestSetupAcceptsPublishedMarketplaceWithReleaseContents(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		t.Run(host, func(t *testing.T) {
			root, pin, calls := publishedSetupFixture(t, host, "")
			var stdout, stderr bytes.Buffer
			if code := runSetup([]string{host, "--path", root, "--sha256", pin}, &stdout, &stderr); code != 0 {
				t.Fatalf("setup refused the published release marketplace: %s %s", stdout.String(), stderr.String())
			}
			data := readSetupHostCalls(t, calls)
			if strings.Contains(string(data), "marketplace add") {
				t.Fatalf("setup replaced the owner's published marketplace:\n%s", data)
			}
			if host == "claude" && !strings.Contains(string(data), "peerturn_binary="+filepath.Join(root, "peerturn")) {
				t.Fatalf("published Claude plugin was not pointed at the verified executable:\n%s", data)
			}
		})
	}
}

func TestSetupRejectsPublishedMarketplaceThatDiffersFromRelease(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		// Other repository contents are a stale marketplace that setup replaces;
		// see TestSetupReplacesOwnStaleMarketplace.
		changes := []string{"repository", "hook"}
		if host == "codex" {
			changes = []string{"repository", "outside"}
		}
		for _, change := range changes {
			t.Run(host+"/"+change, func(t *testing.T) {
				root, pin, calls := publishedSetupFixture(t, host, change)
				var stdout, stderr bytes.Buffer
				if code := runSetup([]string{host, "--path", root, "--sha256", pin}, &stdout, &stderr); code == 0 {
					t.Fatal("setup accepted a published install that differs from the verified release")
				}
				if data := readSetupHostCalls(t, calls); strings.Contains(string(data), "marketplace add") {
					t.Fatalf("setup replaced a conflicting marketplace instead of reporting it:\n%s", data)
				}
			})
		}
	}
}
