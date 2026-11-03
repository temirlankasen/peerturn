package main

import (
	"strings"
	"sync/atomic"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestMCPClaudeRecoversSameNativeProcessEpochWithoutReconnect(t *testing.T) {
	var resolutions atomic.Int32
	calls := serveHook(t, func(r localipc.Request) (any, error) {
		epoch := strings.Repeat("b", 32)
		switch r.Method {
		case "adapter.current":
			if resolutions.Add(1) > 1 {
				epoch = strings.Repeat("c", 32)
			}
			return map[string]string{"session": "chosen", "binding_epoch": epoch}, nil
		case "hello":
			return map[string]string{"capability": "synthetic-" + r.BindingEpoch, "binding_epoch": r.BindingEpoch}, nil
		}
		return map[string]string{"state": "active"}, nil
	}, currentNativeDirectory(t))
	socket, done := currentNativeProbesFixture(t, 2)
	t.Setenv("CODEX_THREAD_ID", "")
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", socket)
	// Read-only current proof must not need a native token.
	t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", "")
	input := claudeColdDiscovery() + claudeColdTool("3") + claudeColdTool("4")
	code, stdout, stderr := currentCommandProcess(t, []string{"mcp", "--current"}, input)
	assertCurrentNoNativeBytes(t, done)
	assertCurrentNoNativeBytes(t, done)
	if code != 0 || stderr != "" || strings.Contains(stdout, `"isError":true`) || resolutions.Load() != 2 {
		t.Fatalf("same-process epoch recovery failed: code=%d resolutions=%d stdout=%s stderr=%s", code, resolutions.Load(), stdout, stderr)
	}
	var methods []string
	statuses := 0
	for _, request := range calls() {
		methods = append(methods, request.Method)
		if request.Method == "status" {
			statuses++
			want := strings.Repeat("b", 32)
			if statuses == 2 {
				want = strings.Repeat("c", 32)
			}
			if request.Session != "chosen" || request.Capability != "synthetic-"+want {
				t.Fatal("tool used stale or unproven epoch")
			}
		}
	}
	if strings.Join(methods, ",") != "adapter.current,hello,status,adapter.current,bye,hello,status,bye" {
		t.Fatalf("unexpected recovery path: %v", methods)
	}
}
