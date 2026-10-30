package main

import (
	"strings"
	"sync/atomic"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestMCPClaudeSameConnectionRechecksNativeProof(t *testing.T) {
	for _, mode := range []string{"late_binding", "replaced_binding", "missing_binding"} {
		t.Run(mode, func(t *testing.T) {
			epoch := strings.Repeat("b", 32)
			var resolutions atomic.Int32
			calls := serveHook(t, func(r localipc.Request) (any, error) {
				switch r.Method {
				case "adapter.current":
					n := resolutions.Add(1)
					unconfirmed := (mode == "late_binding" && n == 1) ||
						(mode == "missing_binding" && n > 1)
					if unconfirmed {
						return nil, localipc.NewError("needs_rebind", "needs_rebind")
					}
					current := epoch
					if mode == "replaced_binding" && n > 1 {
						current = strings.Repeat("c", 32)
					}
					return map[string]string{"session": "chosen", "binding_epoch": current}, nil
				case "hello":
					return map[string]string{"capability": "synthetic-cold-start-capability", "binding_epoch": epoch}, nil
				}
				return map[string]string{"state": "active"}, nil
			}, currentNativeDirectory(t))
			socket, done := currentNativeProbesFixture(t, 2)
			t.Setenv("CODEX_THREAD_ID", "")
			t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", socket)
			t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", "synthetic-private-native-token")
			input := claudeColdDiscovery() + claudeColdTool("3") + claudeColdTool("4")
			code, stdout, stderr := currentCommandProcess(t, []string{"mcp", "--current"}, input)
			assertCurrentNoNativeBytes(t, done)
			assertCurrentNoNativeBytes(t, done)
			if code != 0 || stderr != "" || resolutions.Load() != 2 {
				t.Fatalf("same connection stopped: code=%d resolutions=%d stderr=%s", code, resolutions.Load(), stderr)
			}
			if strings.Count(stdout, `"isError":true`) != 1 || !strings.Contains(stdout, "needs_rebind") {
				t.Fatalf("invalid proof did not produce a tool refusal: %s", stdout)
			}
			methods := make([]string, 0, 5)
			for _, request := range calls() {
				methods = append(methods, request.Method)
				if request.Method == "status" && (request.Session != "chosen" || request.Capability != "synthetic-cold-start-capability") {
					t.Fatal("current MCP used unproven session authority")
				}
			}
			expected := "adapter.current,hello,status,adapter.current,bye"
			if mode == "late_binding" {
				expected = "adapter.current,adapter.current,hello,status,bye"
			}
			if strings.Join(methods, ",") != expected {
				t.Fatalf("unconfirmed native route reached effects: %v", methods)
			}
			if strings.Contains(stdout+stderr, "synthetic-private-native-token") {
				t.Fatal("read-only proof disclosed native credentials")
			}
		})
	}
}
