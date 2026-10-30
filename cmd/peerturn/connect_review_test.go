package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestConnectRuntimePinsEndpointAfterFirstResponse(t *testing.T) {
	first := serveHook(t, func(localipc.Request) (any, error) {
		return map[string]string{"state": "active"}, nil
	}, currentNativeDirectory(t))
	runtime := &connectRuntime{}
	if _, err := runtime.call(t.Context(), localipc.Request{Method: "status"}); err != nil {
		t.Fatal(err)
	}
	second := serveHook(t, func(localipc.Request) (any, error) {
		return map[string]string{"state": "other"}, nil
	}, currentNativeDirectory(t))
	_, err := runtime.call(t.Context(), localipc.Request{Method: "status"})
	if err == nil || errorCode(err) != "needs_rebind" || len(first()) != 1 || len(second()) != 0 {
		t.Fatal("connect followed discovery to another daemon", err)
	}
}

func TestConnectCarriesProvenEpochIntoInvitation(t *testing.T) {
	f := newConnectFake(t).withProfile()
	f.up, f.ready = true, true
	c := f.connector()
	c.call = func(ctx context.Context, request localipc.Request) (json.RawMessage, error) {
		if request.Method == "invite" && request.BindingEpoch != strings.Repeat("ab", 16) {
			t.Error("invitation discarded the proven native epoch")
		}
		return f.call(ctx, request)
	}
	if _, err := c.run(t.Context(), connectOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestConnectRefusesWrongDaemonProfileBeforeBinding(t *testing.T) {
	f := newConnectFake(t).withProfile()
	f.up = true
	c := f.connector()
	c.call = func(ctx context.Context, request localipc.Request) (json.RawMessage, error) {
		if request.Method == "status" {
			return json.RawMessage(`{"state":"active","session":"foreign","binding":"test_session","profile_id":"other-profile"}`), nil
		}
		return f.call(ctx, request)
	}
	_, err := c.run(t.Context(), connectOptions{})
	if err == nil || errorCode(err) != "conflict" || f.count("bind") != 0 || f.count("invite") != 0 {
		t.Fatalf("foreign profile admitted: err=%v events=%v", err, f.events)
	}
}
