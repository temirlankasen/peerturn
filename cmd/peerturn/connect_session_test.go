package main

import (
	"maps"
	"os"
	"strings"
	"testing"
)

func TestConnectRefusesBeforeAnyMutation(t *testing.T) {
	for _, test := range []struct {
		name string
		env  map[string]string
		args []string
		exit int
	}{
		{"no native session", map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": "", "CLAUDE_CODE_MESSAGING_TOKEN": ""}, []string{"--relay", fakeRelay}, 3},
		{"token without socket", map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": ""}, []string{"--relay", fakeRelay}, 3},
		{"relative native socket", map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": "native.sock"}, []string{"--relay", fakeRelay}, 3},
		{"mixed Codex and Claude host", map[string]string{"CODEX_THREAD_ID": "019a0000-0000-7000-8000-000000000001"}, []string{"--relay", fakeRelay}, 3},
		{"Codex only is not bindable here", map[string]string{"CODEX_THREAD_ID": "019a0000-0000-7000-8000-000000000001", "CLAUDE_CODE_MESSAGING_SOCKET": "", "CLAUDE_CODE_MESSAGING_TOKEN": ""}, []string{"--relay", fakeRelay}, 6},
		{"first use needs a relay", nil, nil, 2},
		{"relay must be https", nil, []string{"--relay", "http://relay.example"}, 2},
		{"relay must not carry a path", nil, []string{"--relay", "https://relay.example/v1"}, 2},
		{"relay must not carry userinfo", nil, []string{"--relay", "https://user" + "@" + "relay.example"}, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newConnectFake(t)
			maps.Copy(f.env, test.env)
			got := f.run(test.args...)
			if got.code != test.exit {
				t.Fatalf("exit=%d want %d stdout=%s", got.code, test.exit, got.stdout)
			}
			if mutations := f.mutations(); len(mutations) != 0 || f.config != nil {
				t.Fatalf("refusal mutated state: %v config=%v", mutations, f.config)
			}
			if !strings.Contains(got.stdout, `"error"`) || strings.Contains(got.stdout, fakeLink) {
				t.Fatalf("unexpected output %s", got.stdout)
			}
		})
	}
}

func TestConnectFreshProfileInitializesOnceThenRepeatDoesNot(t *testing.T) {
	f := newConnectFake(t)
	first := f.run("--relay", fakeRelay+"/", "--json")
	if first.code != 0 {
		t.Fatalf("first run failed: %s", first.stdout)
	}
	if f.count("init") != 1 || !f.has("init "+fakeRelay+" claude-test0001") {
		t.Fatalf("expected one init with the normalized relay and generated label: %v", f.events)
	}
	if got := first.json(t)["profile"]; got != "initialized" {
		t.Fatalf("profile=%v", got)
	}
	second := f.run("--json")
	if second.code != 0 || f.count("init") != 1 {
		t.Fatalf("repeat run initialized again or failed: code=%d events=%v", second.code, f.events)
	}
	if got := second.json(t)["profile"]; got != "existing" {
		t.Fatalf("profile=%v", got)
	}
}

func TestConnectRepeatUsesStoredRelayAndAcceptsItsSpelling(t *testing.T) {
	for _, args := range [][]string{{"--json"}, {"--relay", fakeRelay, "--json"}, {"--relay", fakeRelay + "/", "--json"}} {
		f := newConnectFake(t).withProfile()
		got := f.run(args...)
		if got.code != 0 || f.count("init") != 0 {
			t.Fatalf("args=%v code=%d events=%v stdout=%s", args, got.code, f.events, got.stdout)
		}
		if relay := got.json(t)["relay"]; relay != fakeRelay {
			t.Fatalf("relay=%v", relay)
		}
	}
}

func TestConnectConflictingRelayRefusesWithoutWrites(t *testing.T) {
	f := newConnectFake(t).withProfile()
	got := f.run("--relay", "https://other.example", "--json")
	if got.code != 2 || got.errorCode(t) != "conflict" {
		t.Fatalf("code=%d stdout=%s", got.code, got.stdout)
	}
	if mutations := f.mutations(); len(mutations) != 0 {
		t.Fatalf("conflict mutated: %v", mutations)
	}
	if f.config.Relay != fakeRelay {
		t.Fatalf("stored relay changed to %s", f.config.Relay)
	}
}

func TestConnectUnreadableProfileRefusesWithoutInitializing(t *testing.T) {
	f := newConnectFake(t)
	f.configErr = os.ErrPermission
	got := f.run("--relay", fakeRelay)
	if got.code == 0 {
		t.Fatalf("an unreadable profile must not be treated as absent: %s", got.stdout)
	}
	if mutations := f.mutations(); len(mutations) != 0 {
		t.Fatalf("mutated: %v", mutations)
	}
}

func TestConnectForeignRunningDaemonWithoutProfileRefuses(t *testing.T) {
	f := newConnectFake(t)
	f.up = true
	got := f.run("--relay", fakeRelay)
	if got.code != 2 || got.errorCode(t) != "conflict" {
		t.Fatalf("code=%d stdout=%s", got.code, got.stdout)
	}
	if mutations := f.mutations(); len(mutations) != 0 {
		t.Fatalf("mutated: %v", mutations)
	}
}

func TestConnectInitFailureStopsBeforeDaemon(t *testing.T) {
	f := newConnectFake(t)
	f.initErr = os.ErrPermission
	got := f.run("--relay", fakeRelay)
	if got.code == 0 || f.count("spawn") != 0 || f.count("bind") != 0 || f.count("invite") != 0 {
		t.Fatalf("code=%d events=%v", got.code, f.events)
	}
}
