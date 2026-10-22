package claude

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestResponderRefusesBroadOrCredentialProjectRoots(t *testing.T) {
	r := responderFixture(t)
	home := r.options.HomeDirectory
	alias := filepath.Join(t.TempDir(), "home-alias")
	if err := os.Symlink(home, alias); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", home, filepath.Dir(home), alias, "/tmp", "/var", "/usr", "/etc", "/opt", "/Users", "/Volumes"} {
		options := r.options
		options.WorkingDirectory = path
		if err := validateResponderOptions(options); err == nil {
			t.Errorf("broad project root accepted: %s", path)
		}
	}
	for _, name := range []string{".ssh", ".aws", ".codex", ".claude", ".local/share/keyrings", ".config/gcloud"} {
		options := r.options
		options.WorkingDirectory = filepath.Join(t.TempDir(), name)
		if err := os.MkdirAll(options.WorkingDirectory, 0700); err != nil {
			t.Fatal(err)
		}
		if err := validateResponderOptions(options); err == nil {
			t.Errorf("credential directory accepted as a project: %s", name)
		}
	}
}

func TestResponderProjectRootCanonicalQualification(t *testing.T) {
	project, home := t.TempDir(), t.TempDir()
	alias := filepath.Join(t.TempDir(), "project-alias")
	if err := os.Symlink(project, alias); err != nil {
		t.Fatal(err)
	}
	expected, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	root, err := QualifyResponderProjectRoot(alias, home)
	if err != nil || root != expected {
		t.Fatal("safe root alias did not become canonical", err)
	}
	for _, path := range []string{filepath.Join(project, "missing"), "/etc/ssh", "/usr/bin", "/proc/self", "/var/log"} {
		_, qualificationErr := QualifyResponderProjectRoot(path, home)
		var failure *ResponderError
		if !errors.As(qualificationErr, &failure) || failure.Code != ProjectRootUnqualified {
			t.Errorf("unsafe root was not refused with a stable code: %s: %v", path, qualificationErr)
		}
	}
}

func TestResponderRefusesHomeCredentialAncestorsAndAliases(t *testing.T) {
	home := t.TempDir()
	for _, name := range []string{".config", ".local/share", "Library"} {
		if err := os.MkdirAll(filepath.Join(home, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(t.TempDir(), "project")
	if err := os.Symlink(filepath.Join(home, ".config"), alias); err != nil {
		t.Fatal(err)
	}
	external := t.TempDir()
	if err := os.Mkdir(filepath.Join(external, "credentials"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(external, "credentials"), filepath.Join(home, ".config", "gcloud")); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{
		filepath.Join(home, ".config"), filepath.Join(home, ".local"), filepath.Join(home, ".local", "share"),
		filepath.Join(home, "Library"), alias, external,
	} {
		if _, err := QualifyResponderProjectRoot(root, home); err == nil {
			t.Errorf("home credential ancestor or canonical alias accepted: %s", root)
		}
	}
}

func TestResponderDeniesAbsoluteHomeCredentialAliases(t *testing.T) {
	r := responderFixture(t)
	alias := filepath.Join(t.TempDir(), "selected-home")
	if err := os.Symlink(r.options.HomeDirectory, alias); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(r.options.HomeDirectory, ".config"), 0700); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "credentials")
	if err := os.Mkdir(external, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(r.options.HomeDirectory, ".config", "gcloud")); err != nil {
		t.Fatal(err)
	}
	r.options.HomeDirectory = alias
	directory, path, err := responderSettings(r.options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cleanupErr := os.RemoveAll(directory); cleanupErr != nil {
			t.Error(cleanupErr)
		}
	})
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	var settings struct{ Permissions struct{ Deny []string } }
	if err = json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	for _, credential := range append(append([]string(nil), responderCredentialPaths...), "keyrings", "Library/Keychains") {
		declared := filepath.Join(alias, credential)
		resolved, resolveErr := responderResolvedPath(declared)
		if resolveErr != nil {
			t.Fatal(resolveErr)
		}
		for _, target := range []string{declared, resolved} {
			for _, suffix := range []string{"", "/**"} {
				if !slices.Contains(settings.Permissions.Deny, "Read(/"+filepath.ToSlash(target)+suffix+")") {
					t.Errorf("home credential alias missing an absolute deny: %s%s", target, suffix)
				}
			}
		}
	}
}

func TestResponderDeniesNestedCredentialAndHistoryPaths(t *testing.T) {
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
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	var settings struct{ Permissions struct{ Deny []string } }
	if err = json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	root := "/" + filepath.ToSlash(r.options.WorkingDirectory)
	for _, name := range []string{
		".ssh", ".aws", ".kube", ".docker", ".netrc", ".git-credentials", ".codex", ".claude",
		".config/gh", ".config/gcloud", ".gnupg", ".azure", ".npmrc", ".pypirc",
		".local/share/keyrings", "keyrings", "Library/Keychains", ".mozilla", ".config/google-chrome", ".config/chromium", ".peerturn",
	} {
		for _, prefix := range []string{root + "/**/"} {
			for _, suffix := range []string{"", "/**"} {
				if !slices.Contains(settings.Permissions.Deny, "Read("+prefix+name+suffix+")") {
					t.Errorf("missing nested credential deny for %s%s", prefix, name+suffix)
				}
			}
		}
	}
	for _, name := range []string{".env*", ".*_history", ".zsh_sessions", ".bash_sessions", ".history", "fish_history", ".config/fish/fish_history"} {
		if !slices.Contains(settings.Permissions.Deny, "Read("+root+"/**/"+name+")") {
			t.Errorf("missing nested secret/history deny for %s", name)
		}
	}
}

func TestResponderDisablesBackgroundTrafficAndUpdates(t *testing.T) {
	r := responderFixture(t)
	environment := responderEnvironment(r.options)
	for _, key := range []string{"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "DISABLE_AUTOUPDATER", "DISABLE_TELEMETRY", "DISABLE_ERROR_REPORTING"} {
		if !slices.Contains(environment, key+"=1") {
			t.Errorf("missing fork hardening variable %s", key)
		}
	}
}
