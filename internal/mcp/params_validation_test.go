package mcp

import (
	"bytes"
	"strings"
	"testing"
)

func TestMCPRejectsUnknownOrInvalidParameters(t *testing.T) {
	for _, method := range []string{"initialize", "ping", "tools/list", "tools/call"} {
		for _, extra := range []string{
			`"session":"other"`, `"capability":"forged"`, `"_Meta":{}`,
			`"_meta":null`, `"_meta":[]`, `"_meta":"invalid"`,
			`"_meta":{"progressToken":null}`, `"_meta":{"progressToken":true}`,
			`"_meta":{"progressToken":{}}`, `"_meta":{"progressToken":[]}`,
			`"_meta":{"progressToken":0,"progressToken":1}`,
			`"_meta":{"extension":{"duplicate":0,"duplicate":1}}`,
		} {
			t.Run(method+"/"+extra, func(t *testing.T) {
				assertRejectedParams(t, method, extra)
			})
		}
	}
	for _, extra := range []string{`"cursor":null`, `"cursor":0`, `"cursor":{}`, `"cursor":"unknown-position"`, `"cursor":"` + strings.Repeat("x", 4097) + `"`} {
		assertRejectedParams(t, "tools/list", extra)
	}
	for _, extra := range []string{`"cursor":""`, `"task":{}`} {
		assertRejectedParams(t, "ping", extra)
	}
	for _, extra := range []string{`"task":null`, `"task":[]`, `"task":{"ttl":"60000"}`, `"task":{"ttl":null}`, `"task":{"session":"other"}`, `"cursor":""`} {
		assertRejectedParams(t, "tools/call", extra)
	}
}

func assertRejectedParams(t *testing.T, method, extra string) {
	t.Helper()
	params := paramsWithExtra(method, extra)
	input := `{"jsonrpc":"2.0","id":2,"method":"` + method + `","params":` + params + "}\n"
	if method != "initialize" {
		input = codexInit + input
	}
	// A rejected frame must not poison the following request.
	input += `{"jsonrpc":"2.0","id":3,"method":"ping"}` + "\n"
	caller := &recordingCaller{}
	var output bytes.Buffer
	if err := Serve(t.Context(), strings.NewReader(input), &output, Options{Session: "chosen", Caller: caller}); err != nil {
		t.Fatal(err)
	}
	responses := decodeResponses(t, &output)
	rejected := responses[len(responses)-2]
	if rejected.Error == nil || (rejected.Error.Code != -32602 && rejected.Error.Code != -32700) {
		t.Fatalf("%s accepted invalid params %s: %+v", method, extra, rejected)
	}
	if method != "initialize" && responses[len(responses)-1].Error != nil {
		t.Fatal("invalid params poisoned later ping")
	}
	if len(caller.requests) != 2 {
		t.Fatalf("invalid params executed a command: %+v", caller.requests)
	}
}

func paramsWithExtra(method, extra string) string {
	prefix := ""
	switch method {
	case "initialize":
		prefix = `"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"fixture","version":"1"},`
	case "tools/call":
		prefix = `"name":"peer_status",`
	}
	return "{" + prefix + extra + "}"
}

func TestMCPRequestMetadataBounds(t *testing.T) {
	for _, method := range []string{"initialize", "ping", "tools/list", "tools/call"} {
		for _, size := range []int{64 * 1024, 64*1024 + 1} {
			meta := `{"extension":""}`
			meta = strings.Replace(meta, `""`, `"`+strings.Repeat("x", size-len(meta))+`"`, 1)
			params := paramsWithExtra(method, `"_meta":`+meta)
			if method == "initialize" || size > 64*1024 {
				assertRejectedParams(t, method, `"_meta":`+meta)
				continue
			}
			caller := &recordingCaller{}
			var output bytes.Buffer
			input := codexInit + `{"jsonrpc":"2.0","id":2,"method":"` + method + `","params":` + params + "}\n"
			if err := Serve(t.Context(), strings.NewReader(input), &output, Options{Session: "chosen", Caller: caller}); err != nil {
				t.Fatal(err)
			}
			responses := decodeResponses(t, &output)
			if len(responses) != 2 || responses[1].Error != nil {
				t.Fatalf("%s rejected metadata at bound: %+v", method, responses)
			}
		}
	}
}

func TestMCPDiscoveryMetadataCannotBindCodex(t *testing.T) {
	resolved := 0
	var output bytes.Buffer
	input := strings.Replace(codexInit, `"capabilities":{}`, `"_meta":{"threadId":"`+codexThread+`"},"capabilities":{}`, 1)
	input += `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{"_meta":{"threadId":"` + codexThread + `"}}}` + "\n"
	input += codexStatus(`{"progressToken":0}`)
	if err := Serve(t.Context(), strings.NewReader(input), &output, Options{ResolveCodex: func(string) (Caller, error) {
		resolved++
		return &recordingCaller{}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	if resolved != 0 || !strings.Contains(output.String(), `binding_error`) {
		t.Fatalf("discovery established authority: %d %s", resolved, output.String())
	}
}
