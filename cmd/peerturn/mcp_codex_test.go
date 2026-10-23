package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

// This synthetic wire shape follows rust-v0.163.0, commit
// 83086188ae0614bd882818ff1aa23f3194962012: codex-mcp/src/rmcp_client.rs:1112,
// core/src/mcp_tool_call.rs:521,1394 and rmcp-client/src/rmcp_client.rs:847.
// It is source-derived evidence, not a live capture or owner-origin proof.
func codexMCPFixture(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("testdata/codex-mcp-0.163.0.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func codexMCPDaemon(t *testing.T) func() []localipc.Request {
	t.Helper()
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "")
	t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", "")
	// Keep fixture socket paths below macOS's sun_path limit.
	t.Setenv("TMPDIR", "/tmp")
	runtime := t.TempDir()
	return serveHook(t, func(r localipc.Request) (any, error) {
		if r.Method == "hello" {
			if r.NativeThread != hookThread || r.Session != hookThread {
				return nil, localipc.NewError("binding_mismatch", "needs_rebind")
			}
			return map[string]string{"capability": "synthetic-host-bound-capability"}, nil
		}
		return map[string]string{"state": "active"}, nil
	}, runtime)
}

func TestCodexMCPHostStartedSourceShape(t *testing.T) {
	calls := codexMCPDaemon(t)
	var stdout, stderr bytes.Buffer
	if code := runInput([]string{"mcp", "--current"}, strings.NewReader(codexMCPFixture(t)), &stdout, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("source-derived host request failed: code=%d stderr=%s", code, stderr.String())
	}
	requests := calls()
	if len(requests) != 3 || requests[0].Method != "hello" || requests[1].Method != "status" || requests[2].Method != "bye" {
		t.Fatalf("missing native handshake: %+v; stdout=%s", requests, stdout.String())
	}
	for _, request := range requests {
		if request.Session != hookThread || request.NativeThread != hookThread {
			t.Fatal("host metadata did not select the exact native binding")
		}
	}
	if requests[1].Capability != "synthetic-host-bound-capability" || strings.Contains(stdout.String(), `"isError":true`) {
		t.Fatal("tool did not use the daemon-issued session capability")
	}
}

func TestCodexMCPDiscoveryDoesNotAcquireAuthority(t *testing.T) {
	calls := codexMCPDaemon(t)
	input, _, ok := strings.Cut(codexMCPFixture(t), `{"jsonrpc":"2.0","id":3`)
	if !ok {
		t.Fatal("fixture has no tool request")
	}
	var stdout, stderr bytes.Buffer
	if code := runInput([]string{"mcp", "--current"}, strings.NewReader(input), &stdout, &stderr); code != 0 || stderr.Len() != 0 || len(calls()) != 0 || !strings.Contains(stdout.String(), `"peer_status"`) {
		t.Fatalf("discovery acquired authority: code=%d calls=%d stdout=%s stderr=%s", code, len(calls()), stdout.String(), stderr.String())
	}
}

func TestCodexMCPHostSelectors(t *testing.T) {
	for _, mode := range []string{"missing", "arguments", "clientInfo", "capabilities", "version", "client", "contradiction", "invalid", "foreign", "null", "wrong_type"} {
		t.Run(mode, func(t *testing.T) {
			calls := codexMCPDaemon(t)
			input := codexMCPFixture(t)
			metaStart := strings.Index(input, `,"_meta":`)
			if metaStart < 0 {
				t.Fatal("fixture has no metadata")
			}
			switch mode {
			case "missing":
				input = input[:metaStart] + "}}\n"
			case "arguments":
				input = strings.Replace(input[:metaStart], `"arguments":{}`, `"arguments":{"threadId":"`+hookThread+`"}`, 1) + "}}\n"
			case "clientInfo", "capabilities":
				input = input[:metaStart] + "}}\n"
				needle := `"clientInfo":{`
				if mode == "capabilities" {
					needle = `"capabilities":{`
				}
				input = strings.Replace(input, needle, needle+`"threadId":"`+hookThread+`",`, 1)
			case "version":
				input = strings.Replace(input, `"version":"0.163.0"`, `"version":"0.164.0"`, 1)
			case "client":
				input = strings.Replace(input, `"name":"codex-mcp-client"`, `"name":"fixture"`, 1)
			case "contradiction":
				input = strings.Replace(input, `"thread_id":"`+hookThread+`"`, `"thread_id":"019edaa1-4444-7444-8444-444444444444"`, 1)
			case "invalid":
				input = strings.ReplaceAll(input, hookThread, "not-a-thread")
			case "foreign":
				input = strings.ReplaceAll(input, hookThread, "019edaa1-4444-7444-8444-444444444444")
			case "null", "wrong_type":
				value := "null"
				if mode == "wrong_type" {
					value = "7"
				}
				input = strings.Replace(input, `"threadId":"`+hookThread+`"`, `"threadId":`+value, 1)
			}
			var stdout, stderr bytes.Buffer
			code := runInput([]string{"mcp", "--current"}, strings.NewReader(input), &stdout, &stderr)
			requests := calls()
			if code != 0 || stderr.Len() != 0 || (!strings.Contains(stdout.String(), `"isError":true`) && !strings.Contains(stdout.String(), `"error"`)) {
				t.Fatalf("unproven request escaped MCP failure: code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
			if mode == "foreign" {
				if len(requests) != 1 || requests[0].Method != "hello" {
					t.Fatal("foreign native binding reached a tool")
				}
			} else if len(requests) != 0 {
				t.Fatalf("unproven context contacted daemon: %+v", requests)
			}
		})
	}
}
