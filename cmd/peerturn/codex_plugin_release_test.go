package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

func TestCodexRepositoryPluginUsesOnlyVerifiedReleaseHelper(t *testing.T) {
	installer, err := os.ReadFile("../../scripts/install.sh")
	if err != nil {
		t.Fatal(err)
	}
	pin := regexp.MustCompile(`(?m)^version=(\S+)$`).FindSubmatch(installer)
	if pin == nil {
		t.Fatal("installer version pin missing")
	}
	script, err := os.ReadFile("../../adapters/codex/plugin/scripts/inform.sh")
	if err != nil {
		t.Fatal(err)
	}
	plugin, home := t.TempDir(), t.TempDir()
	if err = os.Mkdir(filepath.Join(plugin, "scripts"), 0o700); err != nil {
		t.Fatal(err)
	}
	writePluginExecutable(t, plugin, "scripts/inform.sh", script)
	relative := filepath.Join(".local", "share", "peerturn", string(pin[1]))
	release := filepath.Join(home, relative)
	homeRoot, err := os.OpenRoot(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := homeRoot.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	if err = homeRoot.MkdirAll(relative, 0o700); err != nil {
		t.Fatal(err)
	}
	run := func() string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "/bin/sh", "-c", `"$PLUGIN_ROOT/scripts/inform.sh"`)
		cmd.Env = []string{"PLUGIN_ROOT=" + plugin, "HOME=" + home}
		out, runErr := cmd.CombinedOutput()
		if runErr != nil {
			t.Fatalf("plugin hook failed: %q, %v", out, runErr)
		}
		return string(out)
	}
	if out := run(); out != "{}\n" {
		t.Fatalf("absent release helper did not keep the prompt ordinary: %q", out)
	}
	writePluginExecutable(t, release, "peerturn", []byte("#!/bin/sh\n[ \"$1\" = codex-hook ] || exit 1\nprintf '{\"release\":true}\\n'\n"))
	if out := run(); out != "{\"release\":true}\n" {
		t.Fatalf("repository plugin did not use the verified release helper: %q", out)
	}
	// An emptied bundled directory means a damaged package, not a repository plugin.
	if err = os.Mkdir(filepath.Join(plugin, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if out := run(); out != "{}\n" {
		t.Fatalf("damaged bundled package fell back to another helper: %q", out)
	}
	if err = os.Remove(filepath.Join(plugin, "bin")); err != nil {
		t.Fatal(err)
	}
	external := t.TempDir()
	writePluginExecutable(t, external, "peerturn", []byte("#!/bin/sh\nprintf '{\"untrusted_external\":true}\\n'\n"))
	if err = homeRoot.Remove(filepath.Join(relative, "peerturn")); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(filepath.Join(external, "peerturn"), filepath.Join(release, "peerturn")); err != nil {
		t.Fatal(err)
	}
	if out := run(); out != "{}\n" {
		t.Fatalf("symlinked release helper escaped verification: %q", out)
	}
}
