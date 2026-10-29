package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

const codexThread = "019edaa1-1111-7111-8111-111111111111"
const foreignThread = "019edaa1-2222-7222-8222-222222222222"
const codexInit = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"codex-mcp-client","version":"0.163.0"}}}` + "\n"

func codexStatus(meta string) string {
	return `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"peer_status","arguments":{},"_meta":` + meta + "}}\n"
}

func TestCodexMCPChecksEveryToolContext(t *testing.T) {
	caller := &recordingCaller{}
	resolved := 0
	options := Options{ResolveCodex: func(thread string) (Caller, error) {
		resolved++
		if thread != codexThread {
			t.Fatal("resolved a foreign native session")
		}
		return caller, nil
	}}
	input := codexInit + codexStatus(`{"threadId":"`+codexThread+`"}`) +
		codexStatus(`{}`) + codexStatus(`{"threadId":"`+foreignThread+`"}`) +
		codexStatus(`{"threadId":"`+codexThread+`"}`)
	var output bytes.Buffer
	if err := Serve(t.Context(), strings.NewReader(input), &output, options); err != nil {
		t.Fatal(err)
	}
	if resolved != 1 || len(caller.requests) != 4 || strings.Count(output.String(), `"isError":true`) != 2 {
		t.Fatalf("missing/cross-thread metadata reused pinned authority: resolved=%d requests=%+v output=%s", resolved, caller.requests, output.String())
	}
	for _, request := range caller.requests {
		if request.Session != codexThread {
			t.Fatal("call escaped the pinned session")
		}
	}
}

func TestCodexMCPInheritedThreadRequiresMatchingMetadata(t *testing.T) {
	caller := &recordingCaller{}
	options := Options{Session: codexThread, Caller: caller, CodexThread: codexThread}
	input := codexInit + codexStatus(`{}`) + codexStatus(`{"threadId":"`+foreignThread+`"}`) + codexStatus(`{"threadId":"`+codexThread+`"}`)
	var output bytes.Buffer
	if err := Serve(t.Context(), strings.NewReader(input), &output, options); err != nil {
		t.Fatal(err)
	}
	if len(caller.requests) != 3 || caller.requests[0].Method != "hello" || caller.requests[1].Method != "status" || strings.Count(output.String(), `"isError":true`) != 2 {
		t.Fatalf("inherited thread bypassed host context: %+v %s", caller.requests, output.String())
	}
}

func TestCodexMCPRejectsInvalidContextBeforeResolution(t *testing.T) {
	for _, meta := range []string{
		`null`, `[]`, `{}`, `{"threadId":null}`, `{"threadId":7}`,
		`{"ThreadId":"` + codexThread + `"}`,
		`{"threadId":"` + strings.ToUpper(codexThread) + `"}`,
		`{"threadId":"` + codexThread + `","threadId":"` + foreignThread + `"}`,
		`{"x-codex-turn-metadata":{"thread_id":"` + codexThread + `"}}`,
		`{"threadId":"` + codexThread + `","x-codex-turn-metadata":null}`,
		`{"threadId":"` + codexThread + `","x-codex-turn-metadata":{"thread_id":"` + foreignThread + `"}}`,
		`{"threadId":"` + codexThread + `","sessionId":"` + foreignThread + `","x-codex-turn-metadata":{"session_id":"` + codexThread + `"}}`,
	} {
		options := Options{ResolveCodex: func(string) (Caller, error) {
			t.Fatal("unproven context contacted the daemon")
			return nil, nil
		}}
		var output bytes.Buffer
		if err := Serve(t.Context(), strings.NewReader(codexInit+codexStatus(meta)), &output, options); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), `"isError":true`) && !strings.Contains(output.String(), `"error"`) {
			t.Fatalf("unproven metadata was accepted: %s", meta)
		}
	}
}

type codexFailureCaller struct {
	requests    []localipc.Request
	hellos      int
	refuseFirst bool
}

func (c *codexFailureCaller) Call(_ context.Context, request localipc.Request) (json.RawMessage, error) {
	c.requests = append(c.requests, request)
	switch request.Method {
	case "hello":
		c.hellos++
		if c.refuseFirst || c.hellos > 1 {
			return nil, localipc.NewError("binding_mismatch", "needs_rebind")
		}
		return json.RawMessage(`{"capability":"synthetic-capability","runtime_version":"0.163.0"}`), nil
	case "status":
		return nil, localipc.NewError("unbound", "unbound")
	}
	return json.RawMessage(`{"state":"released"}`), nil
}

func TestCodexMCPFailedHandshakeCannotSwitchThreads(t *testing.T) {
	caller := &codexFailureCaller{refuseFirst: true}
	options := Options{ResolveCodex: func(string) (Caller, error) { return caller, nil }}
	input := codexInit + codexStatus(`{"threadId":"`+codexThread+`"}`) + codexStatus(`{"threadId":"`+foreignThread+`"}`)
	var output bytes.Buffer
	if err := Serve(t.Context(), strings.NewReader(input), &output, options); err != nil {
		t.Fatal(err)
	}
	if len(caller.requests) != 1 || caller.requests[0].Method != "hello" || strings.Count(output.String(), `"isError":true`) != 2 {
		t.Fatalf("failed native handshake silently rebound: %+v %s", caller.requests, output.String())
	}
}

func TestCodexMCPRenewalRequiresSameNativeBinding(t *testing.T) {
	caller := &codexFailureCaller{}
	options := Options{ResolveCodex: func(string) (Caller, error) { return caller, nil }}
	var output bytes.Buffer
	if err := Serve(t.Context(), strings.NewReader(codexInit+codexStatus(`{"threadId":"`+codexThread+`"}`)), &output, options); err != nil {
		t.Fatal(err)
	}
	if len(caller.requests) != 4 || caller.requests[0].Method != "hello" || caller.requests[1].Method != "status" || caller.requests[2].Method != "bye" || caller.requests[3].Method != "hello" || !strings.Contains(output.String(), `"isError":true`) {
		t.Fatalf("renewal lost the native verification fence: %+v %s", caller.requests, output.String())
	}
}

func TestCodexMCPInvalidArgumentsCannotPinSession(t *testing.T) {
	caller := &recordingCaller{}
	resolved := 0
	options := Options{ResolveCodex: func(string) (Caller, error) {
		resolved++
		return caller, nil
	}}
	input := strings.Replace(codexInit+codexStatus(`{"threadId":"`+codexThread+`"}`), `"arguments":{}`, `"arguments":{"session":"`+foreignThread+`"}`, 1)
	input += codexStatus(`{"threadId":"` + foreignThread + `"}`)
	var output bytes.Buffer
	if err := Serve(t.Context(), strings.NewReader(input), &output, options); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"error"`) || resolved != 1 || len(caller.requests) != 3 || caller.requests[0].Session != foreignThread {
		t.Fatal("model-supplied selector acquired authority or pinned a native session")
	}
}
