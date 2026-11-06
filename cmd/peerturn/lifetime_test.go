package main

import (
	"testing"
)

// Owners type durations, agents may type seconds; both must mean the same
// thing, and anything else must be refused before it reaches the daemon.
func TestParseLifetime(t *testing.T) {
	t.Parallel()
	for raw, want := range map[string]int64{"": 0, "10m": 600, "24h": 86400, "3600": 3600, "90s": 90} {
		got, err := parseLifetime(raw)
		if err != nil || got != want {
			t.Fatalf("%q → %d, %v; want %d", raw, got, err, want)
		}
	}
	for _, raw := range []string{"soon", "-5m", "0h", "1.5"} {
		if _, err := parseLifetime(raw); err == nil {
			t.Fatalf("%q accepted", raw)
		}
	}
	command, err := parseCommand([]string{"invite", "revoke", "abc", "--current"})
	if err != nil || command.name != "invite revoke" || command.id != "abc" {
		t.Fatalf("invite revoke parse: %+v %v", command, err)
	}
	if _, err = parseCommand([]string{"connect", "--lifetime", "10m", "--reuse"}); err != nil {
		t.Fatalf("connect flags: %v", err)
	}
}
