package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestCapturedCodexDiscovery(t *testing.T) {
	raw, err := os.ReadFile("testdata/codex-0.163.0-discovery.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	caller := &recordingCaller{}
	resolved := 0
	var output bytes.Buffer
	if err = Serve(t.Context(), bytes.NewReader(raw), &output, Options{ResolveCodex: func(string) (Caller, error) {
		resolved++
		return caller, nil
	}}); err != nil {
		t.Fatal(err)
	}
	responses := decodeResponses(t, &output)
	if len(responses) != 2 || responses[0].Error != nil || responses[1].Error != nil {
		t.Fatalf("stock Codex discovery failed: %+v", responses)
	}
	var result struct{ Tools []tool }
	if err = json.Unmarshal(responses[1].Result, &result); err != nil || len(result.Tools) != 7 {
		t.Fatalf("stock Codex tools = %+v: %v", result, err)
	}
	if resolved != 0 || len(caller.requests) != 0 {
		t.Fatal("discovery metadata selected a native binding")
	}
}

type testResponse struct {
	ID     json.RawMessage
	Result json.RawMessage
	Error  *rpcError
}

func decodeResponses(t *testing.T, output *bytes.Buffer) []testResponse {
	t.Helper()
	var responses []testResponse
	decoder := json.NewDecoder(output)
	for decoder.More() {
		var response testResponse
		if err := decoder.Decode(&response); err != nil {
			t.Fatal(err)
		}
		responses = append(responses, response)
	}
	return responses
}

func TestMCPMethodParameterAudit(t *testing.T) {
	for _, version := range []string{"2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25"} {
		for _, tc := range []struct {
			name, method, params string
		}{
			{"ping-omitted", "ping", ""},
			{"ping-empty", "ping", `{}`},
			{"ping-meta", "ping", `{"_meta":{"progressToken":0,"session":"other","threadId":"informational"}}`},
			{"list-omitted", "tools/list", ""},
			{"list-empty", "tools/list", `{}`},
			{"list-meta", "tools/list", `{"_meta":{"progressToken":"claude-token","authority":{"grant":"ignored"}}}`},
			{"list-cursor", "tools/list", `{"cursor":"","_meta":{"progressToken":0}}`},
			{"call-no-args", "tools/call", `{"name":"peer_status","_meta":{"progressToken":1.5,"session":"other","threadId":"informational"}}`},
			{"call-empty-args", "tools/call", `{"name":"peer_status","arguments":{},"_meta":{"progressToken":"token"}}`},
			{"call-task", "tools/call", `{"name":"peer_status","task":{"ttl":60000},"_meta":{"progressToken":0}}`},
			{"call-empty-task", "tools/call", `{"name":"peer_status","task":{}}`},
		} {
			t.Run(version+"/"+tc.name, func(t *testing.T) {
				request := claudeInitializeFixture(t)
				params := fixtureObject(t, request, "params")
				params["protocolVersion"] = version
				params["_meta"] = map[string]any{"progressToken": 0, "threadId": "informational"}
				init, err := json.Marshal(request)
				if err != nil {
					t.Fatal(err)
				}
				rpc := `{"jsonrpc":"2.0","id":2,"method":"` + tc.method + `"`
				if tc.params != "" {
					rpc += `,"params":` + tc.params
				}
				// Initialized is a notification, with or without informational metadata.
				input := string(init) + "\n" + `{"jsonrpc":"2.0","method":"notifications/initialized","params":{"_meta":{"threadId":"ignored"}}}` + "\n" + rpc + "}\n"
				caller := &recordingCaller{}
				var output bytes.Buffer
				if err = Serve(t.Context(), strings.NewReader(input), &output, Options{Session: "chosen", Caller: caller}); err != nil {
					t.Fatal(err)
				}
				responses := decodeResponses(t, &output)
				if len(responses) != 2 || responses[0].Error != nil || responses[1].Error != nil || strings.Contains(string(responses[1].Result), `"isError":true`) {
					t.Fatalf("valid params rejected: %+v", responses)
				}
				wantCalls := 2
				if tc.method == "tools/call" {
					wantCalls++
					if caller.requests[1].Method != "status" || caller.requests[1].Session != "chosen" || caller.requests[1].Capability != "bound-token" {
						t.Fatalf("metadata changed authority: %+v", caller.requests)
					}
				}
				if len(caller.requests) != wantCalls {
					t.Fatalf("unexpected effects: %+v", caller.requests)
				}
			})
		}
	}
}

func TestMCPUnsupportedMethodsAndNotificationsHaveNoEffects(t *testing.T) {
	caller := &recordingCaller{}
	input := codexInit
	for _, method := range []string{"resources/list", "resources/templates/list", "resources/read", "prompts/list", "prompts/get", "tasks/list", "notifications/initialized"} {
		input += `{"jsonrpc":"2.0","id":2,"method":"` + method + `","params":{"_meta":{"progressToken":0},"cursor":""}}` + "\n"
	}
	for _, params := range []string{`{}`, `{"_meta":{"progressToken":0}}`, `{"session":"other"}`} {
		input += `{"jsonrpc":"2.0","method":"notifications/initialized","params":` + params + "}\n"
	}
	input += `{"jsonrpc":"2.0","method":"tools/call","params":{"name":"peer_status","_meta":{"threadId":"ignored"}}}` + "\n"
	var output bytes.Buffer
	if err := Serve(t.Context(), strings.NewReader(input), &output, Options{Session: "chosen", Caller: caller}); err != nil {
		t.Fatal(err)
	}
	responses := decodeResponses(t, &output)
	if len(responses) != 8 || responses[0].Error != nil {
		t.Fatalf("notification response: %+v", responses)
	}
	for _, response := range responses[1:] {
		if response.Error == nil || response.Error.Code != -32601 {
			t.Fatalf("unadvertised method accepted: %+v", response)
		}
	}
	if len(caller.requests) != 2 {
		t.Fatalf("notification executed a command: %+v", caller.requests)
	}
}
