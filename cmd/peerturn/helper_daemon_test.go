package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/daemon"
)

type recordedSpawn struct {
	calls [][]string
	env   []string
}

func (r *recordedSpawn) spawn(_ string, argv, env []string) (<-chan struct{}, error) {
	r.calls, r.env = append(r.calls, argv), env
	return make(chan struct{}), nil
}

func helperConfig(t *testing.T, write bool) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(base, "state")
	if err = os.Mkdir(state, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, "config", "config.toml")
	if write {
		if err = daemon.WriteConfig(path, state, "https://relay.example", ""); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// The helper restores an existing profile's daemon and nothing else: a
// session that has never connected must not get an identity behind its back.
func TestHelperStartsDaemonOnlyForExistingProfile(t *testing.T) {
	t.Parallel()
	environ := func() []string {
		return []string{"HOME=/var/empty/owner", "ANTHROPIC_API_KEY=provider-secret", "CLAUDE_CODE_MESSAGING_TOKEN=native-secret"}
	}
	executable := func() (string, error) { return "/opt/peerturn/peerturn", nil }
	for _, profile := range []bool{false, true} {
		recorder := &recordedSpawn{}
		path := helperConfig(t, profile)
		helperDaemonStarter(func() (string, error) { return path, nil }, executable, environ, recorder.spawn)()
		if !profile {
			if len(recorder.calls) != 0 {
				t.Fatalf("started a daemon without a profile: %v", recorder.calls)
			}
			continue
		}
		if len(recorder.calls) != 1 || !slices.Equal(recorder.calls[0], []string{"/opt/peerturn/peerturn", "daemon", "start"}) {
			t.Fatalf("daemon start argv %v", recorder.calls)
		}
		if joined := strings.Join(recorder.env, "\n"); strings.Contains(joined, "secret") || !strings.Contains(joined, "HOME=/var/empty/owner") {
			t.Fatalf("daemon environment %q", recorder.env)
		}
	}
	recorder := &recordedSpawn{}
	path := helperConfig(t, true)
	helperDaemonStarter(func() (string, error) { return path, nil }, func() (string, error) { return "relative/peerturn", nil }, environ, recorder.spawn)()
	if len(recorder.calls) != 0 {
		t.Fatal("started a daemon from a relative executable path")
	}
}
