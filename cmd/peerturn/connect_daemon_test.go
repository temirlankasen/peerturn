package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestConnectStartsPrivateDaemonWhenAbsent(t *testing.T) {
	f := newConnectFake(t).withProfile()
	got := f.run("--json")
	if got.code != 0 {
		t.Fatalf("stdout=%s", got.stdout)
	}
	if f.count("spawn") != 1 || f.spawnPath != "/opt/peerturn/bin/peerturn" {
		t.Fatalf("spawn events=%v path=%s", f.events, f.spawnPath)
	}
	if want := []string{"/opt/peerturn/bin/peerturn", "daemon", "start"}; !slices.Equal(f.spawnArgv, want) {
		t.Fatalf("argv=%v want %v", f.spawnArgv, want)
	}
	if got.json(t)["daemon"] != "started" {
		t.Fatalf("daemon=%v", got.json(t)["daemon"])
	}
}

func TestConnectReusesRunningDaemonWithoutRestart(t *testing.T) {
	f := newConnectFake(t).withProfile()
	f.up = true
	got := f.run("--json")
	if got.code != 0 || f.count("spawn") != 0 {
		t.Fatalf("code=%d events=%v", got.code, f.events)
	}
	if got.json(t)["daemon"] != "reused" {
		t.Fatalf("daemon=%v", got.json(t)["daemon"])
	}
}

func TestConnectDaemonNeverReadyIsBoundedTimeout(t *testing.T) {
	f := newConnectFake(t).withProfile()
	f.neverUp = true
	got := f.run("--json")
	if got.code != 4 || got.errorCode(t) != "timeout" {
		t.Fatalf("code=%d stdout=%s", got.code, got.stdout)
	}
	if f.statusCalls != 0 || f.count("status") > connectReadyAttempts+2 || f.waits > connectReadyAttempts {
		t.Fatalf("readiness was not bounded: status=%d waits=%d", f.count("status"), f.waits)
	}
	if f.count("bind") != 0 || f.count("invite") != 0 {
		t.Fatalf("continued after a daemon that never became ready: %v", f.events)
	}
}

func TestConnectDaemonExitingEarlyFailsFastWithoutBinding(t *testing.T) {
	f := newConnectFake(t).withProfile()
	f.exitEarly = true
	got := f.run("--json")
	if got.code != 4 || got.errorCode(t) != "unavailable" {
		t.Fatalf("code=%d stdout=%s", got.code, got.stdout)
	}
	if f.waits >= connectReadyAttempts || f.count("bind") != 0 || f.count("invite") != 0 {
		t.Fatalf("exit was not detected promptly: waits=%d events=%v", f.waits, f.events)
	}
}

func TestConnectSpawnFailureIsUnavailable(t *testing.T) {
	f := newConnectFake(t).withProfile()
	f.spawnErr = os.ErrPermission
	got := f.run("--json")
	if got.code != 4 || got.errorCode(t) != "unavailable" || f.count("bind") != 0 {
		t.Fatalf("code=%d events=%v stdout=%s", got.code, f.events, got.stdout)
	}
}

func TestConnectRequiresAbsoluteExecutable(t *testing.T) {
	f := newConnectFake(t).withProfile()
	c := f.connector()
	c.executable = func() (string, error) { return "peerturn", nil }
	if _, err := c.run(t.Context(), connectOptions{}); err == nil || f.count("spawn") != 0 {
		t.Fatalf("a relative executable must never be spawned: err=%v events=%v", err, f.events)
	}
}

func TestConnectSpawnEnvironmentDropsNativeAndProviderCredentials(t *testing.T) {
	f := newConnectFake(t).withProfile()
	f.env["XDG_RUNTIME_DIR"] = "/run/user/1000"
	f.env["OPENAI_API_KEY"] = "synthetic-openai"
	f.env["CODEX_THREAD_ID"] = ""
	f.env["GITHUB_TOKEN"] = "synthetic-github"
	if got := f.run("--json"); got.code != 0 {
		t.Fatalf("stdout=%s", got.stdout)
	}
	joined := strings.Join(f.spawnEnv, "\n")
	for _, secret := range []string{"synthetic-native-token", "synthetic-provider-key", "synthetic-openai", "synthetic-github", "CLAUDE_CODE_", "ANTHROPIC", "OPENAI", "CODEX", "GITHUB"} {
		if strings.Contains(joined, secret) {
			t.Fatalf("daemon environment leaks %q:\n%s", secret, joined)
		}
	}
	for _, kept := range []string{"HOME=/fixture/home", "PATH=/usr/bin:/bin", "XDG_RUNTIME_DIR=/run/user/1000"} {
		if !slices.Contains(f.spawnEnv, kept) {
			t.Fatalf("daemon lost %s: %v", kept, f.spawnEnv)
		}
	}
}

func TestDaemonEnvironmentIsAnAllowlist(t *testing.T) {
	got := daemonEnvironment([]string{
		"HOME=/fixture/home", "USER=u", "PATH=/bin", "TMPDIR=/var/tmp", "LANG=C.UTF-8", "LC_ALL=C", "TZ=UTC",
		"XDG_RUNTIME_DIR=/run/u", "XDG_CONFIG_HOME=/c", "XDG_STATE_HOME=/s", "XDG_DATA_HOME=/d", "XDG_CACHE_HOME=/k",
		"HTTPS_PROXY=http://proxy:3128", "NO_PROXY=localhost", "SSL_CERT_FILE=/etc/ssl/ca.pem",
		"CLAUDE_CODE_MESSAGING_SOCKET=/tmp/s", "CLAUDE_CODE_MESSAGING_TOKEN=t", "CLAUDECODE=1", "ANTHROPIC_API_KEY=k", "ANTHROPIC_AUTH_TOKEN=a",
		"OPENAI_API_KEY=o", "CODEX_THREAD_ID=x", "CODEX_HOME=/h", "AWS_SECRET_ACCESS_KEY=a", "GITHUB_TOKEN=g", "SSH_AUTH_SOCK=/s",
		"PATH=/dup", "MALFORMED", "=novalue", "HOME_EXTRA=1", "LC_=x\nINJECTED=1",
	})
	want := []string{
		"HOME=/fixture/home", "USER=u", "PATH=/bin", "TMPDIR=/var/tmp", "LANG=C.UTF-8", "LC_ALL=C", "TZ=UTC",
		"XDG_RUNTIME_DIR=/run/u", "XDG_CONFIG_HOME=/c", "XDG_STATE_HOME=/s", "XDG_DATA_HOME=/d", "XDG_CACHE_HOME=/k",
		"HTTPS_PROXY=http://proxy:3128", "NO_PROXY=localhost", "SSL_CERT_FILE=/etc/ssl/ca.pem",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("environment\n got %v\nwant %v", got, want)
	}
}

func TestSpawnDetachedRunsInOwnSessionWithoutInheritedStreamsOrSecrets(t *testing.T) {
	dir := t.TempDir()
	pidFile, envFile, readFile := filepath.Join(dir, "pid"), filepath.Join(dir, "env"), filepath.Join(dir, "stdin")
	// Publish the PID only after the fixture output is complete.
	script := `env > "$2"; read line; echo "status=$?" > "$3"; echo $$ > "$1.tmp" && mv "$1.tmp" "$1"; exec sleep 30`
	env := daemonEnvironment([]string{"PATH=/usr/bin:/bin", "CLAUDE_CODE_MESSAGING_TOKEN=synthetic-native-token"})
	exited, err := spawnDetached("/bin/sh", []string{"sh", "-c", script, "sh", pidFile, envFile, readFile}, env)
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	deadline := time.Now().Add(10 * time.Second)
	for pid == 0 && time.Now().Before(deadline) {
		if data, readErr := readInside(pidFile); readErr == nil {
			pid = atoi(strings.TrimSpace(string(data)))
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("detached child never started")
	}
	t.Cleanup(func() {
		if killErr := syscall.Kill(pid, syscall.SIGKILL); killErr != nil {
			t.Logf("child already gone: %v", killErr)
		}
		<-exited
	})
	sid, err := unix.Getsid(pid)
	if err != nil || sid != pid {
		t.Fatalf("child must lead its own session: pid=%d sid=%d err=%v", pid, sid, err)
	}
	if mine, mineErr := unix.Getsid(0); mineErr != nil || sid == mine {
		t.Fatalf("child shares the caller's session: %d", sid)
	}
	data, err := readInside(envFile)
	if err != nil || strings.Contains(string(data), "synthetic-native-token") || !strings.Contains(string(data), "PATH=/usr/bin:/bin") {
		t.Fatalf("child environment wrong: err=%v\n%s", err, data)
	}
	if got, readErr := readInside(readFile); readErr != nil || strings.TrimSpace(string(got)) != "status=1" {
		t.Fatalf("stdin must be /dev/null: %q err=%v", got, readErr)
	}
}

func TestSpawnDetachedWithNoEnvironmentNeverInheritsTheCallers(t *testing.T) {
	t.Setenv("PEERTURN_TEST_SECRET", "synthetic-inherited-secret")
	if env := daemonEnvironment([]string{"ANTHROPIC_API_KEY=k"}); env == nil || len(env) != 0 {
		t.Fatalf("an empty allowlist result must be a non-nil empty slice, got %#v", env)
	}
	envFile := filepath.Join(t.TempDir(), "env")
	exited, err := spawnDetached("/bin/sh", []string{"sh", "-c", `env > "$1"`, "sh", envFile}, nil)
	if err != nil {
		t.Fatal(err)
	}
	<-exited
	data, err := readInside(envFile)
	if err != nil || strings.Contains(string(data), "synthetic-inherited-secret") {
		t.Fatalf("child inherited the caller's environment: err=%v\n%s", err, data)
	}
}

func readInside(path string) ([]byte, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	data, readErr := root.ReadFile(filepath.Base(path))
	return data, errors.Join(readErr, root.Close())
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}
