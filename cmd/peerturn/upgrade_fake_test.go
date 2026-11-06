package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/daemon"
	"github.com/temirlankasen/peerturn/internal/localipc"
)

const (
	upgradeOld       = "0.1.9-preview"
	upgradeNew       = "0.2.0-preview"
	upgradeTag       = "v0.2.0-preview"
	upgradeOtherHash = "dd1efe4a3f40aa3b0a6485a010442e83ba91fc9ecb24385f25c193bde3eaf007"
	upgradeSh        = "#!/bin/sh\necho installing\n"
)

// upgradeFake stands in for GitHub, cosign, the installer, the host CLIs and the daemon.
// A single event log (also written by the cosign stub) proves the order of the steps.
type upgradeFake struct {
	t       *testing.T
	home    string
	current string // version of the running binary
	binDir  string
	temp    string
	logPath string
	server  *httptest.Server

	mu        sync.Mutex
	downloads []string
	latestTag string
	assets    map[string]string

	hosts         map[string]bool
	setupFails    map[string]bool
	installFails  bool
	daemonUp      bool
	daemonVersion string
	lingering     int  // status answers still given after "stop" before the daemon is gone
	stopHangs     bool // the daemon never goes away
	stopped       bool // "stop" was requested and the daemon is going away
	spawnExits    bool // the new daemon process exits at once
	waits         int
	spawnedArgv   []string
	spawnedEnv    []string
	scripts       []string
}

func newUpgradeFake(t *testing.T) *upgradeFake {
	t.Helper()
	root := t.TempDir()
	f := &upgradeFake{
		t: t, home: filepath.Join(root, "home"), binDir: filepath.Join(root, "stubs"), temp: filepath.Join(root, "tmp"),
		logPath: filepath.Join(root, "events.log"), latestTag: upgradeTag, current: upgradeOld,
		hosts: map[string]bool{"claude": true}, setupFails: map[string]bool{},
		daemonUp: true, daemonVersion: upgradeOld,
	}
	for _, dir := range []string{f.home, f.binDir, f.temp} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	sum := sha256.Sum256([]byte(upgradeSh))
	f.assets = map[string]string{
		"install.sh": upgradeSh,
		"peerturn_" + upgradeNew + "_checksums.txt":               upgradeOtherHash + "  install.ps1\n" + hex.EncodeToString(sum[:]) + "  install.sh\n",
		"peerturn_" + upgradeNew + "_checksums.txt.sigstore.json": `{"bundle":true}`,
	}
	f.server = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	f.installCosign("v3.1.3", 0, "")
	t.Setenv("PATH", f.binDir)
	t.Setenv("ANTHROPIC_API_KEY", "synthetic-provider-key")
	f.writeBinary(f.versionDir(upgradeOld), upgradeOld)
	f.writeBinary(f.binPath(), upgradeOld)
	return f
}

func (f *upgradeFake) versionDir(version string) string {
	return filepath.Join(f.home, ".local", "share", "peerturn", version)
}

func (f *upgradeFake) binPath() string { return filepath.Join(f.home, ".local", "bin", "peerturn") }

// writeBinary makes <target>/peerturn (or the file itself when target ends in "peerturn") report version.
func (f *upgradeFake) writeBinary(target, version string) {
	f.t.Helper()
	path := target
	if filepath.Base(target) != "peerturn" {
		path = filepath.Join(target, "peerturn")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		f.t.Fatal(err)
	}
	f.writeExecutable(path, "version:"+version)
}

func (f *upgradeFake) installCosign(gitVersion string, verifyExit int, stderr string) {
	f.t.Helper()
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  version) printf '{\"gitVersion\":\"" + gitVersion + "\"}\\n' ;;\n" +
		"  verify-blob) echo \"verify $*\" >> '" + f.logPath + "'; printf '%s' '" + stderr + "' >&2; exit " + strconv.Itoa(verifyExit) + " ;;\n" +
		"esac\n"
	f.writeExecutable(filepath.Join(f.binDir, "cosign"), script)
}

// writeExecutable replaces a file through a root handle, which also drops the old file's mode.
func (f *upgradeFake) writeExecutable(path, content string) {
	f.t.Helper()
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		f.t.Fatal(err)
	}
	if err = root.Remove(filepath.Base(path)); err != nil && !errors.Is(err, os.ErrNotExist) {
		f.t.Fatal(err)
	}
	file, err := root.OpenFile(filepath.Base(path), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err = io.WriteString(file, content); err != nil {
		f.t.Fatal(err)
	}
	if err = errors.Join(file.Close(), root.Close()); err != nil {
		f.t.Fatal(err)
	}
}

func (f *upgradeFake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, status := "", http.StatusOK
	switch name, isAsset := strings.CutPrefix(r.URL.Path, "/dl/"+f.latestTag+"/"); {
	case r.URL.Path == "/repos/temirlankasen/peerturn/releases/latest":
		body = `{"tag_name":"` + f.latestTag + `"}`
	case isAsset && f.assets[name] != "":
		f.downloads = append(f.downloads, name)
		body = f.assets[name]
	default:
		status = http.StatusNotFound
	}
	w.WriteHeader(status)
	if _, err := io.WriteString(w, body); err != nil {
		return // the client hung up; its own assertions report the outcome
	}
}

func (f *upgradeFake) record(event string) {
	f.t.Helper()
	file, err := os.OpenFile(f.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err = io.WriteString(file, event+"\n"); err != nil {
		f.t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		f.t.Fatal(err)
	}
}

// sequence returns the recorded steps by name, with cosign's arguments dropped.
func (f *upgradeFake) sequence() []string {
	f.t.Helper()
	data, err := readInstalledFile(f.logPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		f.t.Fatal(err)
	}
	var steps []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		if name, _, _ := strings.Cut(line, " "); name == "verify" {
			steps = append(steps, "verify")
		} else {
			steps = append(steps, line)
		}
	}
	return steps
}

func (f *upgradeFake) say(out io.Writer, text string) {
	f.t.Helper()
	if _, err := io.WriteString(out, text); err != nil {
		f.t.Error(err)
	}
}

func (f *upgradeFake) lookPath(name string) (string, error) {
	switch {
	case name == "cosign":
		return exec.LookPath(name)
	case f.hosts[name]:
		return "/fake/bin/" + name, nil
	default:
		return "", errors.New("not found")
	}
}

func (f *upgradeFake) runScript(_ context.Context, script string, out io.Writer) error {
	f.t.Helper()
	f.record("install")
	if info, err := os.Stat(filepath.Dir(script)); err != nil || info.Mode().Perm() != 0o700 {
		f.t.Errorf("installer work directory = %v, %v; want a private 0700 directory", info, err)
	}
	data, err := readInstalledFile(script)
	if err != nil {
		f.t.Fatal(err)
	}
	f.scripts = append(f.scripts, string(data))
	f.say(out, "peerturn installer: Release source: https://example.invalid\n")
	if f.installFails {
		f.say(out, "peerturn installer: archive checksum mismatch\n")
		return errors.New("exit status 1")
	}
	f.writeBinary(f.versionDir(upgradeNew), upgradeNew)
	f.writeBinary(f.binPath(), upgradeNew)
	return nil
}

func (f *upgradeFake) runBinary(_ context.Context, binary string, args []string, out io.Writer) error {
	f.t.Helper()
	data, err := readInstalledFile(binary)
	if err != nil {
		return err
	}
	have := strings.TrimPrefix(string(data), "version:")
	switch {
	case slices.Equal(args, []string{"version"}):
		f.say(out, "peerturn "+have+" (commit c0ffee, built 2026-11-06T00:00:00Z)\n")
		return nil
	case len(args) == 2 && args[0] == "setup":
		if binary != f.binPath() || have != upgradeNew {
			f.t.Errorf("setup ran %s (%s), want the new binary %s", binary, have, f.binPath())
		}
		f.record("setup " + args[1])
		f.say(out, `{"state":"installed","host":"`+args[1]+`"}`+"\n")
		if f.setupFails[args[1]] {
			return errors.New("exit status 1")
		}
		return nil
	}
	f.t.Errorf("unexpected binary call %v", args)
	return errors.New("unexpected call")
}

func (f *upgradeFake) call(_ context.Context, request localipc.Request) (json.RawMessage, error) {
	f.t.Helper()
	switch request.Method {
	case "status":
		if f.daemonUp && f.stopped && !f.stopHangs {
			if f.lingering == 0 {
				f.daemonUp, f.stopped = false, false
			} else {
				f.lingering--
			}
		}
		if !f.daemonUp {
			return nil, localipc.NewError("unavailable", "unavailable")
		}
		return json.Marshal(daemon.Status{PeerturnVersion: f.daemonVersion, ProfileID: "profile"})
	case "stop":
		f.record("stop")
		f.stopped = true
		return json.RawMessage(`{"state":"stopping"}`), nil
	}
	f.t.Errorf("unexpected daemon request %q", request.Method)
	return nil, localipc.NewError("unsupported", "unsupported")
}

func (f *upgradeFake) spawn(executable string, argv, env []string) (<-chan struct{}, error) {
	f.t.Helper()
	f.record("start")
	f.spawnedArgv, f.spawnedEnv = argv, env
	if executable != f.binPath() {
		f.t.Errorf("daemon started from %s, want %s", executable, f.binPath())
	}
	data, err := readInstalledFile(executable)
	if err != nil {
		return nil, err
	}
	exited := make(chan struct{})
	if f.spawnExits {
		close(exited)
		return exited, nil
	}
	f.daemonUp, f.daemonVersion = true, strings.TrimPrefix(string(data), "version:")
	return exited, nil
}

func (f *upgradeFake) upgrader() upgrader {
	return upgrader{
		version: f.current, client: f.server.Client(), apiBase: f.server.URL, downloadBase: f.server.URL + "/dl",
		tempRoot: f.temp, home: func() (string, error) { return f.home, nil }, lookPath: f.lookPath,
		runScript: f.runScript, runBinary: f.runBinary,
		daemon: connector{
			environ: os.Environ, call: f.call, spawn: f.spawn,
			wait: func(context.Context, time.Duration) error { f.waits++; return nil },
		},
	}
}

type upgradeRun struct {
	code           int
	stdout, stderr string
}

func (f *upgradeFake) run(args ...string) upgradeRun {
	f.t.Helper()
	var stdout, stderr bytes.Buffer
	code := runUpgradeWith(f.t.Context(), args, &stdout, &stderr, f.upgrader())
	return upgradeRun{code: code, stdout: stdout.String(), stderr: stderr.String()}
}
