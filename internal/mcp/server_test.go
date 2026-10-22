package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

type recordingCaller struct{ requests []localipc.Request }

func (c *recordingCaller) Call(_ context.Context, request localipc.Request) (json.RawMessage, error) {
	c.requests = append(c.requests, request)
	if request.Method == "hello" {
		return json.RawMessage(`{"capability":"bound-token"}`), nil
	}
	return json.RawMessage(`{"state":"stored","model_input":false}`), nil
}

func TestMCPOnlySevenToolsAndBoundSession(t *testing.T) {
	caller := &recordingCaller{}
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"fixture","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"peer_send","arguments":{"peer":"known-peer","kind":"coauthor","body":"literal @path https://example.invalid"}}}`,
	}, "\n") + "\n"
	var output bytes.Buffer
	if err := Serve(t.Context(), strings.NewReader(input), &output, Options{Session: "chosen", Caller: caller}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("responses = %s", output.String())
	}
	var listed struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Result.Tools) != 7 {
		t.Fatalf("tools = %s", lines[1])
	}
	if len(caller.requests) != 3 || caller.requests[1].Session != "chosen" || caller.requests[1].Capability != "bound-token" || caller.requests[1].Body != "literal @path https://example.invalid" || caller.requests[2].Method != "bye" {
		t.Fatalf("requests = %#v", caller.requests)
	}
}

func TestMCPRejectsOwnerControlsAndUnknownArguments(t *testing.T) {
	for _, params := range []string{
		`{"name":"grants_revoke","arguments":{}}`,
		`{"name":"peer_send","arguments":{"peer":"p","kind":"coauthor","body":"x","url":"https://evil.invalid"}}`,
		`{"name":"peer_status","arguments":{"session":"other"}}`,
		`{"name":"peer_send","arguments":{"peer":"p","kind":"owner","body":"x"}}`,
		`{"name":"peer_reply","arguments":{"message_id":"m","body":"x","key":"secret"}}`,
		`{"name":"peer_status","arguments":{"native_uuid":"other"}}`,
	} {
		caller := &recordingCaller{}
		input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}` + "\n" + `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":` + params + "}\n"
		var output bytes.Buffer
		if err := Serve(t.Context(), strings.NewReader(input), &output, Options{Session: "chosen", Caller: caller}); err != nil {
			t.Fatal(err)
		}
		if len(caller.requests) != 2 || caller.requests[1].Method != "bye" || !strings.Contains(output.String(), `"error"`) {
			t.Fatalf("unsafe command escaped: %s, %#v", output.String(), caller.requests)
		}
	}
}

func TestMCPRequiresExplicitBinding(t *testing.T) {
	var output bytes.Buffer
	if err := Serve(t.Context(), strings.NewReader(""), &output, Options{Caller: &recordingCaller{}}); localipc.ExitCode(err) != 3 {
		t.Fatalf("unbound = %v", err)
	}
}

func TestMCPCancellationUnblocksStdin(t *testing.T) {
	reader, writer := io.Pipe()
	t.Cleanup(func() {
		if err := writer.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithCancel(t.Context())
	finished := make(chan error, 1)
	go func() {
		finished <- Serve(ctx, reader, io.Discard, Options{Session: "chosen", Caller: &recordingCaller{}})
	}()
	cancel()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("canceled MCP returned success")
		}
	case <-time.After(time.Second):
		t.Fatal("MCP did not cancel blocked stdio")
	}
}
