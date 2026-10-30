package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/claudeipc"
	"github.com/temirlankasen/peerturn/internal/daemon"
	"github.com/temirlankasen/peerturn/internal/localipc"
)

const (
	fakeRelay = "https://relay.example"
	fakeLink  = "https://relay.example/i/ROUTE#secret-fragment-for-tests"
)

type connectFake struct {
	t   *testing.T
	env map[string]string

	config         *daemon.Config
	configErr      error
	up             bool
	neverUp        bool
	exitEarly      bool
	spawnErr       error
	initErr        error
	ready          bool
	foreign        bool
	pinned         bool
	bindFails      int
	bindErr        error
	bindSession    string
	resolveSession string
	inviteBinding  string
	inviteErr      error
	peers          []daemon.Peer
	peersErr       error

	events      []string
	spawnPath   string
	spawnArgv   []string
	spawnEnv    []string
	statusCalls int
	waits       int
	session     string
}

func newConnectFake(t *testing.T) *connectFake {
	t.Helper()
	return &connectFake{t: t, inviteBinding: "native_claude", session: "claude-test0001", env: map[string]string{
		"CLAUDE_CODE_MESSAGING_SOCKET": "/tmp/native-test.sock",
		"CLAUDE_CODE_MESSAGING_TOKEN":  "synthetic-native-token",
		"ANTHROPIC_API_KEY":            "synthetic-provider-key",
		"HOME":                         "/fixture/home",
		"PATH":                         "/usr/bin:/bin",
	}}
}

func (f *connectFake) withProfile() *connectFake {
	f.config = &daemon.Config{Version: 1, StateDir: "/state", Relay: fakeRelay, StorageMode: "file_permissions"}
	return f
}

func (f *connectFake) mutations() []string {
	var out []string
	for _, event := range f.events {
		if name, _, _ := strings.Cut(event, " "); slices.Contains([]string{"init", "spawn", "bind", "invite"}, name) {
			out = append(out, event)
		}
	}
	return out
}

func (f *connectFake) has(event string) bool { return slices.Contains(f.events, event) }

func (f *connectFake) count(name string) int {
	n := 0
	for _, event := range f.events {
		if head, _, _ := strings.Cut(event, " "); head == name {
			n++
		}
	}
	return n
}

func (f *connectFake) connector() connector {
	return connector{
		getenv: func(key string) string { return f.env[key] },
		environ: func() []string {
			var list []string
			for key, value := range f.env {
				list = append(list, key+"="+value)
			}
			slices.Sort(list)
			return list
		},
		executable: func() (string, error) { return "/opt/peerturn/bin/peerturn", nil },
		readConfig: func() (daemon.Config, error) {
			if f.configErr != nil {
				return daemon.Config{}, f.configErr
			}
			if f.config == nil {
				return daemon.Config{}, os.ErrNotExist
			}
			return *f.config, nil
		},
		label:      func() (string, error) { return f.session, nil },
		initialize: f.initialize,
		call:       f.call,
		spawn:      f.start,
		resolve:    f.resolveCurrent,
		bind:       f.bindCurrent,
		wait: func(ctx context.Context, _ time.Duration) error {
			f.waits++
			return ctx.Err()
		},
	}
}

func (f *connectFake) initialize(_ context.Context, relay, label string) error {
	f.events = append(f.events, "init "+relay+" "+label)
	if f.initErr != nil {
		return f.initErr
	}
	f.config = &daemon.Config{Version: 1, StateDir: "/state", Relay: relay, StorageMode: "file_permissions"}
	f.session = label
	return nil
}

func (f *connectFake) start(path string, argv, env []string) (<-chan struct{}, error) {
	f.events = append(f.events, "spawn")
	f.spawnPath, f.spawnArgv, f.spawnEnv = path, argv, env
	if f.spawnErr != nil {
		return nil, f.spawnErr
	}
	exited := make(chan struct{})
	switch {
	case f.exitEarly:
		close(exited)
	case !f.neverUp:
		f.up = true
	}
	return exited, nil
}

func (f *connectFake) call(_ context.Context, request localipc.Request) (json.RawMessage, error) {
	f.events = append(f.events, request.Method)
	if !f.up {
		return nil, localipc.NewError("unavailable", "unavailable")
	}
	switch request.Method {
	case "status":
		f.statusCalls++
		binding := "test_session"
		if f.ready || f.foreign {
			binding = "native_claude"
		} else if f.pinned {
			binding = "needs_rebind"
		}
		profileID := ""
		if f.config != nil {
			profileID = daemon.ProfileID(f.config.StateDir, f.config.Relay)
		}
		return f.encode(daemon.Status{State: "active", Binding: binding, Session: f.session, ProfileID: profileID})
	case "invite":
		if f.inviteErr != nil {
			return nil, f.inviteErr
		}
		if request.Session != f.session || request.Scope != "coauthor" {
			return nil, localipc.NewError("unbound", "unbound")
		}
		return f.encode(map[string]any{"url": fakeLink, "expires_at": 1893456000, "scope": "coauthor", "binding": f.inviteBinding})
	case "peers":
		if f.peersErr != nil {
			return nil, f.peersErr
		}
		return f.encode(append([]daemon.Peer{}, f.peers...))
	}
	return nil, localipc.NewError("unsupported", "unsupported")
}

func (f *connectFake) encode(value any) (json.RawMessage, error) {
	data, err := json.Marshal(value)
	if err != nil {
		f.t.Fatal(err)
	}
	return data, nil
}

func (f *connectFake) resolveCurrent(context.Context) (claudeipc.CurrentEvidence, error) {
	f.events = append(f.events, "resolve")
	if !f.ready || f.foreign {
		return claudeipc.CurrentEvidence{}, localipc.NewError("needs_rebind", "needs_rebind")
	}
	session := f.session
	if f.resolveSession != "" {
		session = f.resolveSession
	}
	return claudeipc.CurrentEvidence{Session: session, BindingEpoch: strings.Repeat("ab", 16)}, nil
}

func (f *connectFake) bindCurrent(context.Context) (string, error) {
	f.events = append(f.events, "bind")
	if f.bindErr != nil {
		return "", f.bindErr
	}
	if f.bindFails > 0 {
		f.bindFails--
		return "", localipc.NewError("needs_rebind", "needs_rebind")
	}
	f.ready = true
	if f.bindSession != "" {
		return f.bindSession, nil
	}
	return f.session, nil
}

type connectRun struct {
	code           int
	stdout, stderr string
}

func (f *connectFake) run(args ...string) connectRun {
	f.t.Helper()
	command, err := parseCommand(append([]string{"connect"}, args...))
	if err != nil {
		f.t.Fatalf("parse %v: %v", args, err)
	}
	var stdout, stderr bytes.Buffer
	code := runConnectWith(context.Background(), command, &stdout, &stderr, f.connector())
	return connectRun{code, stdout.String(), stderr.String()}
}

func (r connectRun) json(t *testing.T) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, r.stdout)
	}
	return out
}

func (r connectRun) errorCode(t *testing.T) string {
	t.Helper()
	failure, ok := r.json(t)["error"].(map[string]any)
	if !ok {
		t.Fatalf("no error object in %s", r.stdout)
	}
	code, ok := failure["code"].(string)
	if !ok {
		t.Fatalf("error without a code: %s", r.stdout)
	}
	return code
}
