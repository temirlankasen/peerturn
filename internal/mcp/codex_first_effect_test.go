package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

type firstEffectVersionCaller struct {
	requests []localipc.Request
	version  json.RawMessage
	effects  int
	closes   int
}

type firstEffectLeaseCaller struct {
	firstEffectVersionCaller
	opened, released int
}

func (c *firstEffectLeaseCaller) OpenCapability(ctx context.Context, session string) (json.RawMessage, func() error, error) {
	c.opened++
	hello, err := c.Call(ctx, localipc.Request{Method: "hello", Session: session})
	return hello, func() error { c.released++; return nil }, err
}

func (c *firstEffectVersionCaller) Call(_ context.Context, request localipc.Request) (json.RawMessage, error) {
	c.requests = append(c.requests, request)
	switch request.Method {
	case "hello":
		response := map[string]json.RawMessage{"capability": json.RawMessage(`"version-capability"`)}
		if c.version != nil {
			response["runtime_version"] = c.version
		}
		return json.Marshal(response)
	case "send", "join.owner":
		c.effects++
	case "bye":
		c.closes++
	}
	return json.RawMessage(`{"state":"stored"}`), nil
}

func firstEffectRequest(method string) string {
	arguments := `{"peer":"known-peer","kind":"coauthor","body":"synthetic first effect"}`
	if method == "peer_join" {
		arguments = `{}`
	}
	return `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"` + method + `","arguments":` + arguments + `,"_meta":{"threadId":"` + codexThread + `"}}}` + "\n"
}

func TestCodexMCPChecksRuntimeVersionBeforeFirstEffect(t *testing.T) {
	for _, method := range []string{"peer_send", "peer_join"} {
		for _, version := range []json.RawMessage{nil, json.RawMessage(`""`), json.RawMessage(`"0.163.0"`), json.RawMessage(`"future"`), json.RawMessage(`7`)} {
			t.Run(method+string(version), func(t *testing.T) {
				caller := &firstEffectVersionCaller{version: version}
				options := Options{ResolveCodex: func(string) (Caller, error) { return caller, nil }}
				request := firstEffectRequest(method)
				input := strings.Replace(codexInit, "0.163.0", "0.164.0", 1) + request + request
				var output bytes.Buffer
				if err := Serve(t.Context(), strings.NewReader(input), &output, options); err != nil {
					t.Fatal(err)
				}
				if caller.effects != 0 || caller.closes != 1 || len(caller.requests) != 2 || caller.requests[0].Method != "hello" || caller.requests[1].Method != "bye" || strings.Count(output.String(), "runtime_contract_changed") != 2 {
					t.Fatalf("first effect escaped runtime comparison or retried: effects=%d calls=%+v output=%s", caller.effects, caller.requests, output.String())
				}
			})
		}
	}
}

func TestCodexMCPMatchingNewerVersionAllowsFirstEffectWithoutProbe(t *testing.T) {
	for _, version := range []string{"0.164.0", "12.0.0", "0.164.0+modified"} {
		for _, method := range []string{"peer_send", "peer_join"} {
			t.Run(version+method, func(t *testing.T) {
				encoded, err := json.Marshal(version)
				if err != nil {
					t.Fatal(err)
				}
				caller := &firstEffectVersionCaller{version: encoded}
				options := Options{ResolveCodex: func(string) (Caller, error) { return caller, nil }}
				input := strings.Replace(codexInit, "0.163.0", version, 1) + firstEffectRequest(method)
				var output bytes.Buffer
				if err = Serve(t.Context(), strings.NewReader(input), &output, options); err != nil {
					t.Fatal(err)
				}
				if caller.effects != 1 || caller.closes != 1 || len(caller.requests) != 3 || strings.Contains(output.String(), `"isError":true`) {
					t.Fatalf("matching newer runtime lost first effect: calls=%+v output=%s", caller.requests, output.String())
				}
			})
		}
	}
}

func TestCodexMCPVersionMismatchReleasesIPCLease(t *testing.T) {
	caller := &firstEffectLeaseCaller{}
	caller.version = json.RawMessage(`"0.163.0"`)
	options := Options{ResolveCodex: func(string) (Caller, error) { return caller, nil }}
	input := strings.Replace(codexInit, "0.163.0", "0.164.0", 1) + firstEffectRequest("peer_send")
	var output bytes.Buffer
	if err := Serve(t.Context(), strings.NewReader(input), &output, options); err != nil {
		t.Fatal(err)
	}
	if caller.effects != 0 || caller.opened != 1 || caller.released != 1 || caller.closes != 1 {
		t.Fatalf("version mismatch retained capability or IPC lease: %+v", caller)
	}
}

func TestCodexMCPPreopenedCapabilityNeedsRuntimeVersionBeforeFirstEffect(t *testing.T) {
	caller := &firstEffectVersionCaller{}
	options := Options{Session: codexThread, Caller: caller}
	var output bytes.Buffer
	if err := Serve(t.Context(), strings.NewReader(codexInit+firstEffectRequest("peer_send")), &output, options); err != nil {
		t.Fatal(err)
	}
	if caller.effects != 0 || caller.closes != 1 || !strings.Contains(output.String(), "runtime_contract_changed") {
		t.Fatalf("early hello bypassed runtime version comparison: effects=%d calls=%+v output=%s", caller.effects, caller.requests, output.String())
	}
}

type renewalVersionCaller struct {
	firstEffectVersionCaller
	hellos int
}

func (c *renewalVersionCaller) Call(ctx context.Context, request localipc.Request) (json.RawMessage, error) {
	if request.Method == "hello" {
		c.hellos++
		if c.hellos > 1 {
			c.version = json.RawMessage(`"0.165.0"`)
		}
	}
	if request.Method == "send" {
		c.requests = append(c.requests, request)
		return nil, localipc.NewError("unbound", "unbound")
	}
	return c.firstEffectVersionCaller.Call(ctx, request)
}

func TestCodexMCPRenewalChecksRuntimeBeforeRetryingRejectedEffect(t *testing.T) {
	caller := &renewalVersionCaller{}
	caller.version = json.RawMessage(`"0.164.0"`)
	options := Options{ResolveCodex: func(string) (Caller, error) { return caller, nil }}
	request := firstEffectRequest("peer_send")
	input := strings.Replace(codexInit, "0.163.0", "0.164.0", 1) + request + request
	var output bytes.Buffer
	if err := Serve(t.Context(), strings.NewReader(input), &output, options); err != nil {
		t.Fatal(err)
	}
	if caller.effects != 0 || caller.hellos != 2 || caller.closes != 2 || len(caller.requests) != 5 || strings.Count(output.String(), "runtime_contract_changed") != 2 {
		t.Fatalf("renewal bypassed version check or retried: calls=%+v output=%s", caller.requests, output.String())
	}
}
