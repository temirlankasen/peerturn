package mcp

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestMCPCurrentFreshProofRenewsSameSessionEpoch(t *testing.T) {
	first := &currentCaller{epoch: currentEpoch}
	second := &currentCaller{epoch: strings.Repeat("c", 32)}
	resolutions := 0
	options := Options{ResolveCurrent: func(context.Context) (CurrentBinding, error) {
		resolutions++
		caller := first
		if resolutions > 1 {
			caller = second
		}
		return CurrentBinding{Session: "chosen", BindingEpoch: caller.epoch, Caller: caller}, nil
	}}
	input := currentDiscovery + currentTool("3", "peer_status", `{}`) +
		currentTool("4", "peer_send", `{"peer":"p","kind":"coauthor","body":"synthetic"}`)
	var output bytes.Buffer
	if err := Serve(t.Context(), strings.NewReader(input), &output, options); err != nil {
		t.Fatal(err)
	}
	if resolutions != 2 || currentMethods(first) != "hello,status,bye" || currentMethods(second) != "hello,send,bye" || strings.Contains(output.String(), `"isError":true`) {
		t.Fatalf("fresh native proof did not renew: resolutions=%d first=%s second=%s output=%s", resolutions, currentMethods(first), currentMethods(second), output.String())
	}
}

func TestMCPCurrentChangedEpochNeverReplaysAmbiguousEffect(t *testing.T) {
	first := &currentCaller{epoch: currentEpoch, sendFailure: "in_doubt"}
	second := &currentCaller{epoch: strings.Repeat("c", 32)}
	resolutions := 0
	options := Options{ResolveCurrent: func(context.Context) (CurrentBinding, error) {
		resolutions++
		caller := first
		if resolutions > 1 {
			caller = second
		}
		return CurrentBinding{Session: "chosen", BindingEpoch: caller.epoch, Caller: caller}, nil
	}}
	input := currentDiscovery + currentTool("3", "peer_send", `{"peer":"p","kind":"coauthor","body":"one attempt"}`) +
		currentTool("4", "peer_status", `{}`)
	var output bytes.Buffer
	if err := Serve(t.Context(), strings.NewReader(input), &output, options); err != nil {
		t.Fatal(err)
	}
	if currentMethods(first) != "hello,send,bye" || currentMethods(second) != "hello,status,bye" || strings.Count(output.String(), `"isError":true`) != 1 {
		t.Fatalf("epoch recovery replayed an effect or failed: first=%s second=%s", currentMethods(first), currentMethods(second))
	}
}

func TestMCPCurrentNewEpochStillRequiresMatchingHello(t *testing.T) {
	caller := &currentCaller{epoch: currentEpoch}
	resolutions := 0
	options := Options{ResolveCurrent: func(context.Context) (CurrentBinding, error) {
		resolutions++
		proof := currentProof(caller)
		if resolutions > 1 {
			proof.BindingEpoch = strings.Repeat("c", 32)
		}
		return proof, nil
	}}
	input := currentDiscovery + currentTool("3", "peer_status", `{}`) +
		currentTool("4", "peer_send", `{"peer":"p","kind":"coauthor","body":"must refuse"}`)
	var output bytes.Buffer
	if err := Serve(t.Context(), strings.NewReader(input), &output, options); err != nil {
		t.Fatal(err)
	}
	for _, request := range caller.requests {
		if request.Method == "send" {
			t.Fatal("stale hello admitted an effect")
		}
	}
	if !strings.Contains(output.String(), "needs_rebind") {
		t.Fatal("stale epoch was not refused")
	}
}
