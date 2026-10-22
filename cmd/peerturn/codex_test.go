package main

import (
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestCurrentCodexUsesToolThreadWithoutCWDDiscovery(t *testing.T) {
	t.Setenv("CODEX_THREAD_ID", "019edaa1-1111-7111-8111-111111111111")
	c, err := parseCommand([]string{"invite", "create", "--current", "--scope", "coauthor"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.request(strings.NewReader(""))
	if err != nil || r.Session != "019edaa1-1111-7111-8111-111111111111" || r.NativeThread != r.Session {
		t.Fatalf("current tool binding: %+v %v", r, err)
	}
}

func TestCodexHookForeignThreadNoBlock(t *testing.T) {
	calls := serveHook(t, func(localipc.Request) (any, error) { return hookOffer(), nil })
	t.Setenv("CODEX_THREAD_ID", "019edaa1-1111-7111-8111-111111111111")
	input := hookInput()
	input["session_id"] = "019edaa1-2222-7222-8222-222222222222"
	var out strings.Builder
	executeHook(t, input, &out)
	if out.String() != "{}\n" || len(calls()) != 0 {
		t.Fatalf("foreign hook: %q, %+v", out.String(), calls())
	}
}
