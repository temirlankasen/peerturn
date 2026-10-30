package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

const currentEpoch = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

const currentDiscovery = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"fixture","version":"1"}}}` + "\n" +
	`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"

type currentCaller struct {
	requests     []localipc.Request
	epoch        string
	helloFailure string
	sendFailure  string
}

func (c *currentCaller) Call(_ context.Context, request localipc.Request) (json.RawMessage, error) {
	c.requests = append(c.requests, request)
	if request.Method == "hello" {
		if c.helloFailure != "" {
			return nil, localipc.NewError(c.helloFailure, "needs_rebind")
		}
		return json.Marshal(map[string]string{"capability": "synthetic-current-capability", "binding_epoch": c.epoch})
	}
	if request.Method == "send" && c.sendFailure != "" {
		return nil, localipc.NewError(c.sendFailure, c.sendFailure)
	}
	return json.RawMessage(`{"state":"active"}`), nil
}

func currentTool(id, name, arguments string) string {
	return `{"jsonrpc":"2.0","id":` + id + `,"method":"tools/call","params":{"name":"` + name + `","arguments":` + arguments + `}}` + "\n"
}

func currentProof(caller Caller) CurrentBinding {
	return CurrentBinding{Session: "chosen", BindingEpoch: currentEpoch, Caller: caller}
}

func currentMethods(caller *currentCaller) string {
	methods := make([]string, 0, len(caller.requests))
	for _, request := range caller.requests {
		methods = append(methods, request.Method)
	}
	return strings.Join(methods, ",")
}

func TestMCPCurrentDiscoveryCannotResolveOrAcquireAuthority(t *testing.T) {
	resolved := 0
	options := Options{ResolveCurrent: func(context.Context) (CurrentBinding, error) {
		resolved++
		return CurrentBinding{}, localipc.NewError("needs_rebind", "needs_rebind")
	}}
	var output bytes.Buffer
	if err := Serve(t.Context(), strings.NewReader(currentDiscovery), &output, options); err != nil {
		t.Fatal(err)
	}
	if resolved != 0 || !strings.Contains(output.String(), `"peer_status"`) {
		t.Fatalf("discovery acquired authority: resolutions=%d output=%s", resolved, output.String())
	}
}

func TestMCPCurrentRecoversAfterBindingWithoutRestart(t *testing.T) {
	caller := &currentCaller{epoch: currentEpoch}
	resolved := 0
	options := Options{ResolveCurrent: func(context.Context) (CurrentBinding, error) {
		resolved++
		if resolved == 1 {
			return CurrentBinding{}, localipc.NewError("needs_rebind", "needs_rebind")
		}
		return currentProof(caller), nil
	}}
	input := currentDiscovery + currentTool("3", "peer_status", `{}`) + currentTool("4", "peer_status", `{}`)
	var output bytes.Buffer
	if err := Serve(t.Context(), strings.NewReader(input), &output, options); err != nil {
		t.Fatal(err)
	}
	if resolved != 2 || currentMethods(caller) != "hello,status,bye" ||
		strings.Count(output.String(), `"isError":true`) != 1 || !strings.Contains(output.String(), `active`) {
		t.Fatalf("late binding did not recover: resolutions=%d calls=%s output=%s", resolved, currentMethods(caller), output.String())
	}
	for _, request := range caller.requests {
		if request.Session != "chosen" {
			t.Fatal("late binding selected another session")
		}
	}
}

func TestMCPCurrentChecksEveryCallAndNeverFollowsChangedProof(t *testing.T) {
	for _, change := range []string{"session", "epoch", "missing", "unavailable"} {
		t.Run(change, func(t *testing.T) {
			caller := &currentCaller{epoch: currentEpoch}
			resolved := 0
			options := Options{ResolveCurrent: func(context.Context) (CurrentBinding, error) {
				resolved++
				proof := currentProof(caller)
				if resolved == 1 {
					return proof, nil
				}
				switch change {
				case "session":
					proof.Session = "another-session"
				case "epoch":
					proof.BindingEpoch = strings.Repeat("c", 32)
				case "missing":
					proof.BindingEpoch = ""
				case "unavailable":
					return CurrentBinding{}, localipc.NewError("needs_rebind", "needs_rebind")
				}
				return proof, nil
			}}
			input := currentDiscovery + currentTool("3", "peer_status", `{}`) +
				currentTool("4", "peer_send", `{"peer":"p","kind":"coauthor","body":"must not send"}`)
			var output bytes.Buffer
			if err := Serve(t.Context(), strings.NewReader(input), &output, options); err != nil {
				t.Fatal(err)
			}
			if resolved != 2 || currentMethods(caller) != "hello,status,bye" || !strings.Contains(output.String(), "needs_rebind") {
				t.Fatalf("changed proof admitted an effect: resolutions=%d calls=%s output=%s", resolved, currentMethods(caller), output.String())
			}
		})
	}
}

func TestMCPCurrentRefusedHelloStillPinsSessionAndEndpoint(t *testing.T) {
	for _, change := range []string{"session", "caller"} {
		t.Run(change, func(t *testing.T) {
			first := &currentCaller{epoch: currentEpoch, helloFailure: "unbound"}
			second := &currentCaller{epoch: currentEpoch}
			resolved := 0
			options := Options{ResolveCurrent: func(context.Context) (CurrentBinding, error) {
				resolved++
				proof := currentProof(first)
				if resolved > 1 {
					if change == "session" {
						proof.Session = "another-session"
					} else {
						proof.Caller = second
					}
				}
				return proof, nil
			}}
			input := currentDiscovery + currentTool("3", "peer_status", `{}`) + currentTool("4", "peer_status", `{}`)
			var output bytes.Buffer
			if err := Serve(t.Context(), strings.NewReader(input), &output, options); err != nil {
				t.Fatal(err)
			}
			expected := "hello"
			if change == "caller" {
				expected += ",hello"
			}
			if currentMethods(first) != expected || len(second.requests) != 0 || strings.Count(output.String(), `"isError":true`) != 2 {
				t.Fatalf("refused hello changed authority: first=%s second=%s output=%s", currentMethods(first), currentMethods(second), output.String())
			}
		})
	}
}

func TestMCPCurrentRequiresExactHelloEpoch(t *testing.T) {
	caller := &currentCaller{epoch: strings.Repeat("c", 32)}
	options := Options{ResolveCurrent: func(context.Context) (CurrentBinding, error) {
		return currentProof(caller), nil
	}}
	var output bytes.Buffer
	if err := Serve(t.Context(), strings.NewReader(currentDiscovery+currentTool("3", "peer_status", `{}`)), &output, options); err != nil {
		t.Fatal(err)
	}
	if currentMethods(caller) != "hello,bye" || !strings.Contains(output.String(), "needs_rebind") {
		t.Fatalf("hello changed the proven incarnation: calls=%s output=%s", currentMethods(caller), output.String())
	}
}

func TestMCPCurrentMetadataCannotSelectANativeSession(t *testing.T) {
	caller := &currentCaller{epoch: currentEpoch}
	options := Options{ResolveCurrent: func(context.Context) (CurrentBinding, error) {
		return currentProof(caller), nil
	}}
	input := strings.Replace(currentDiscovery, `"name":"fixture","version":"1"`, `"name":"codex-mcp-client","version":"0.163.0"`, 1) +
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"peer_status","arguments":{},"_meta":{"threadId":"019edaa1-1111-7111-8111-111111111111"}}}` + "\n"
	var output bytes.Buffer
	if err := Serve(t.Context(), strings.NewReader(input), &output, options); err != nil {
		t.Fatal(err)
	}
	if currentMethods(caller) != "hello,status,bye" || strings.Contains(output.String(), `"isError":true`) {
		t.Fatalf("informational metadata overrode native proof: calls=%s output=%s", currentMethods(caller), output.String())
	}
	for _, request := range caller.requests {
		if request.Session != "chosen" || request.NativeThread != "" {
			t.Fatal("client metadata became native authority")
		}
	}
}

func TestMCPCurrentMissingProofCannotOpenCapability(t *testing.T) {
	for _, change := range []string{"session", "epoch", "uppercase", "caller"} {
		t.Run(change, func(t *testing.T) {
			caller := &currentCaller{epoch: currentEpoch}
			proof := currentProof(caller)
			switch change {
			case "session":
				proof.Session = ""
			case "epoch":
				proof.BindingEpoch = "not-an-epoch"
			case "uppercase":
				proof.BindingEpoch = strings.ToUpper(currentEpoch)
			case "caller":
				proof.Caller = nil
			}
			options := Options{ResolveCurrent: func(context.Context) (CurrentBinding, error) { return proof, nil }}
			var output bytes.Buffer
			if err := Serve(t.Context(), strings.NewReader(currentDiscovery+currentTool("3", "peer_status", `{}`)), &output, options); err != nil {
				t.Fatal(err)
			}
			if len(caller.requests) != 0 || !strings.Contains(output.String(), "needs_rebind") {
				t.Fatalf("missing proof opened authority: %s", output.String())
			}
		})
	}
}

func TestMCPCurrentRejectsMixedBindingProfiles(t *testing.T) {
	for _, change := range []string{"session", "caller", "epoch", "codex"} {
		t.Run(change, func(t *testing.T) {
			options := Options{ResolveCurrent: func(context.Context) (CurrentBinding, error) {
				t.Fatal("mixed profile attempted resolution")
				return CurrentBinding{}, nil
			}}
			switch change {
			case "session":
				options.Session = "chosen"
			case "caller":
				options.Caller = &currentCaller{}
			case "epoch":
				options.ExpectedBindingEpoch = currentEpoch
			case "codex":
				options.ResolveCodex = func(string) (Caller, error) { return nil, nil }
			}
			var output bytes.Buffer
			if err := Serve(t.Context(), strings.NewReader(currentDiscovery), &output, options); localipc.PublicError(err).Code != "binding_mismatch" || output.Len() != 0 {
				t.Fatalf("mixed native profile admitted: err=%v output=%s", err, output.String())
			}
		})
	}
}

func TestMCPCurrentNeverResubmitsAmbiguousSend(t *testing.T) {
	caller := &currentCaller{epoch: currentEpoch, sendFailure: "in_doubt"}
	options := Options{ResolveCurrent: func(context.Context) (CurrentBinding, error) { return currentProof(caller), nil }}
	input := currentDiscovery + currentTool("3", "peer_send", `{"peer":"p","kind":"coauthor","body":"one attempt"}`)
	var output bytes.Buffer
	if err := Serve(t.Context(), strings.NewReader(input), &output, options); err != nil {
		t.Fatal(err)
	}
	if currentMethods(caller) != "hello,send,bye" || !strings.Contains(output.String(), "in_doubt") {
		t.Fatalf("ambiguous send was replayed: calls=%s output=%s", currentMethods(caller), output.String())
	}
}
