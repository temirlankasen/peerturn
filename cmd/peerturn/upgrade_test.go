package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestUpgradeCheckReportsNewerRelease(t *testing.T) {
	f := newUpgradeFake(t)
	got := f.run("--check")
	if got.code != 0 || got.stderr != "" {
		t.Fatalf("code=%d stderr=%q", got.code, got.stderr)
	}
	if want := "PeerTurn 0.1.9-preview; latest 0.2.0-preview (run peerturn upgrade)\n"; got.stdout != want {
		t.Fatalf("stdout = %q, want %q", got.stdout, want)
	}
	if steps := f.sequence(); len(steps) != 0 || len(f.downloads) != 0 {
		t.Fatalf("--check changed something: steps=%v downloads=%v", steps, f.downloads)
	}
}

func TestUpgradeCheckWhenAlreadyLatest(t *testing.T) {
	f := newUpgradeFake(t)
	f.latestTag = "v0.1.9-preview"
	got := f.run("--check")
	if want := "PeerTurn 0.1.9-preview is the latest release.\n"; got.code != 0 || got.stdout != want {
		t.Fatalf("code=%d stdout=%q, want %q", got.code, got.stdout, want)
	}
}

func TestUpgradeCheckDoesNotOfferADowngrade(t *testing.T) {
	f := newUpgradeFake(t)
	f.current = "0.3.0-preview"
	got := f.run("--check")
	if want := "PeerTurn 0.3.0-preview is the latest release.\n"; got.code != 0 || got.stdout != want {
		t.Fatalf("code=%d stdout=%q, want %q", got.code, got.stdout, want)
	}
}

func TestUpgradeCheckJSON(t *testing.T) {
	f := newUpgradeFake(t)
	got := f.run("--check", "--json")
	want := `{"current":"0.1.9-preview","latest":"0.2.0-preview","update_available":true}` + "\n"
	if got.code != 0 || got.stdout != want || got.stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q, want %q", got.code, got.stdout, got.stderr, want)
	}
}

func TestUpgradeRejectsDevelopmentBuild(t *testing.T) {
	f := newUpgradeFake(t)
	f.current = "dev"
	for _, args := range [][]string{{"--check"}, {}} {
		got := f.run(args...)
		if got.code != 1 || got.stdout != "" || !strings.HasPrefix(got.stderr, "peerturn: upgrade failed: ") ||
			!strings.Contains(got.stderr, "development build") || len(f.sequence()) != 0 {
			t.Fatalf("args=%v: code=%d stdout=%q stderr=%q steps=%v", args, got.code, got.stdout, got.stderr, f.sequence())
		}
	}
}

func TestUpgradeCheckReportsUnreachableGitHub(t *testing.T) {
	f := newUpgradeFake(t)
	f.server.Close()
	got := f.run("--check")
	if got.code != 1 || !strings.HasPrefix(got.stderr, "peerturn: upgrade failed: could not check the latest release") ||
		!strings.Contains(got.stderr, "rerun peerturn upgrade") {
		t.Fatalf("code=%d stderr=%q", got.code, got.stderr)
	}
}

func TestUpgradeErrorsAreJSONOnStdout(t *testing.T) {
	f := newUpgradeFake(t)
	f.current = "dev"
	got := f.run("--json")
	if got.code != 1 || got.stderr != "" {
		t.Fatalf("code=%d stderr=%q", got.code, got.stderr)
	}
	want := `{"error":{"code":"upgrade_failed","message":"` + upgradeDevMessage + `"}}` + "\n"
	if got.stdout != want {
		t.Fatalf("stdout = %q, want %q", got.stdout, want)
	}
}

func TestUpgradeRejectsUnknownArguments(t *testing.T) {
	for _, args := range [][]string{{"--bogus"}, {"extra"}, {"--check=maybe"}, {"--json", "--"}} {
		f := newUpgradeFake(t)
		got := f.run(args...)
		if got.code != 2 || got.stdout != "" || !strings.Contains(got.stderr, "--check, --verbose and --json") || len(f.sequence()) != 0 {
			t.Errorf("args=%v: code=%d stdout=%q stderr=%q", args, got.code, got.stdout, got.stderr)
		}
	}
}

func TestUpgradeIsRoutedBeforeLocalCommands(t *testing.T) {
	if localCommand("upgrade") {
		t.Fatal("upgrade must not be a localipc command")
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"upgrade", "--bogus"}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "peerturn: upgrade accepts only") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	stdout.Reset()
	if code := run([]string{"help"}, &stdout, &stderr); code != 0 ||
		!strings.Contains(stdout.String(), "  upgrade [--check] [--verbose] [--json]  Install the latest signed release, update plugins and restart the daemon\n") {
		t.Fatalf("usage lacks the upgrade line: %q", stdout.String())
	}
}
