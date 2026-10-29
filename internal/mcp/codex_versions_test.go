package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestCodexMCPNewerVersionsKeepExactHostThreadBinding(t *testing.T) {
	for _, version := range []string{"0.163.1", "0.164.0", "1.0.0", "12.0.0"} {
		t.Run(version, func(t *testing.T) {
			caller := &recordingCaller{runtimeVersion: version}
			resolved := 0
			options := Options{ResolveCodex: func(thread string) (Caller, error) {
				resolved++
				if thread != codexThread {
					t.Fatal("resolved foreign native thread")
				}
				return caller, nil
			}}
			input := strings.Replace(codexInit, "0.163.0", version, 1) +
				codexStatus(`{"threadId":"`+codexThread+`"}`) +
				codexStatus(`{"threadId":"`+foreignThread+`"}`)
			var output bytes.Buffer
			if err := Serve(t.Context(), strings.NewReader(input), &output, options); err != nil {
				t.Fatal(err)
			}
			if resolved != 1 || len(caller.requests) != 3 || strings.Count(output.String(), `"isError":true`) != 1 || !strings.Contains(output.String(), `mcp_client_policy`) {
				t.Fatalf("newer runtime lost admission or exact-thread fence: resolved=%d calls=%+v output=%s", resolved, caller.requests, output.String())
			}
		})
	}
}

type codexVersionCaller struct {
	recordingCaller
	version string
}

func (c *codexVersionCaller) Call(ctx context.Context, request localipc.Request) (json.RawMessage, error) {
	result, err := c.recordingCaller.Call(ctx, request)
	if err == nil && request.Method == "status" {
		return json.Marshal(map[string]string{"runtime_version": c.version})
	}
	return result, err
}

func TestCodexMCPContradictoryVersionsCloseOnlyThisLane(t *testing.T) {
	caller := &codexVersionCaller{version: "0.163.0"}
	caller.runtimeVersion = "0.164.0"
	options := Options{ResolveCodex: func(string) (Caller, error) { return caller, nil }}
	request := codexStatus(`{"threadId":"` + codexThread + `"}`)
	input := strings.Replace(codexInit, "0.163.0", "0.164.0", 1) + request + request
	var output bytes.Buffer
	if err := Serve(t.Context(), strings.NewReader(input), &output, options); err != nil {
		t.Fatal(err)
	}
	if len(caller.requests) != 3 || caller.requests[1].Method != "status" || strings.Count(output.String(), `runtime_contract_changed`) != 2 {
		t.Fatalf("contradiction was retried or ignored: calls=%+v output=%s", caller.requests, output.String())
	}
}

func TestCodexMCPRejectedVersionIsTypedBeforeResolution(t *testing.T) {
	for _, tc := range []struct{ version, code string }{{"0.162.0", "unsupported_version"}, {"future", "runtime_version_unknown"}} {
		t.Run(tc.version, func(t *testing.T) {
			options := Options{ResolveCodex: func(string) (Caller, error) {
				t.Fatal("rejected MCP version contacted daemon")
				return nil, nil
			}}
			input := strings.Replace(codexInit, "0.163.0", tc.version, 1) + codexStatus(`{"threadId":"`+codexThread+`"}`)
			var output bytes.Buffer
			if err := Serve(t.Context(), strings.NewReader(input), &output, options); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), tc.code) {
				t.Fatalf("version diagnostic lost: %s", output.String())
			}
		})
	}
}
