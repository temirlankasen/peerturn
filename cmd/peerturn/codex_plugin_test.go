package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func checkPluginHookCommand(t *testing.T) {
	t.Helper()
	raw, err := os.ReadFile("../../adapters/codex/plugin/hooks/hooks.json")
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
				Limit   int    `json:"additionalContextLimit"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	hook := config.Hooks["UserPromptSubmit"][0].Hooks[0]
	if hook.Command != `"$PLUGIN_ROOT/scripts/inform.sh"` || hook.Limit != 256 {
		t.Fatalf("unsafe command or implicit spill limit: %+v", hook)
	}
}

func writePluginExecutable(t *testing.T, dir, name string, body []byte) {
	t.Helper()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	if _, err := file.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := file.Chmod(0o700); err != nil {
		t.Fatal(err)
	}
}

func TestCodexPluginExecutesOnlyBundledHelperInQuotedRoot(t *testing.T) {
	checkPluginHookCommand(t)
	root := filepath.Join(t.TempDir(), "plugin $HOME `printf exploit` \"quoted\"")
	for _, dir := range []string{"scripts", "bin"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	script, err := os.ReadFile("../../adapters/codex/plugin/scripts/inform.sh")
	if err != nil {
		t.Fatal(err)
	}
	writePluginExecutable(t, root, "scripts/inform.sh", script)
	bundled := filepath.Join(root, "bin/peerturn")
	writePluginExecutable(t, root, "bin/peerturn", []byte("#!/bin/sh\n[ \"$1\" = codex-hook ] || exit 1\nprintf '{\"bundled\":true}\\n'\n"))
	externalDir := t.TempDir()
	external := filepath.Join(externalDir, "peerturn")
	writePluginExecutable(t, externalDir, "peerturn", []byte("#!/bin/sh\nprintf '{\"untrusted_external\":true}\\n'\n"))
	run := func() string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "/bin/sh", "-c", `"$PLUGIN_ROOT/scripts/inform.sh"`)
		cmd.Env = []string{"PLUGIN_ROOT=" + root, "PEERTURN_BIN=" + external}
		out, runErr := cmd.CombinedOutput()
		if runErr != nil {
			t.Fatalf("plugin hook failed: %q, %v", out, runErr)
		}
		return string(out)
	}
	if out := run(); out != "{\"bundled\":true}\n" {
		t.Fatalf("bundled helper not selected: %q", out)
	}
	if err := os.Remove(bundled); err != nil {
		t.Fatal(err)
	}
	if out := run(); out != "{}\n" {
		t.Fatalf("missing helper used external override: %q", out)
	}
	if err := os.Symlink(external, bundled); err != nil {
		t.Fatal(err)
	}
	if out := run(); out != "{}\n" || strings.Contains(out, "untrusted_external") {
		t.Fatalf("symlink escaped plugin hash: %q", out)
	}
	if err := os.Remove(bundled); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "bin")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(external), filepath.Join(root, "bin")); err != nil {
		t.Fatal(err)
	}
	if out := run(); out != "{}\n" {
		t.Fatalf("directory symlink escaped plugin hash: %q", out)
	}
}
