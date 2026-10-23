package main

import (
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func hookInvitation() string {
	return "https://relay.example/i/AAAAAAAAAAAAAAAAAAAAAA#v=1&ik=" + strings.Repeat("A", 43) + "&ek=" + strings.Repeat("A", 43) + "&fp=" + strings.Repeat("a", 64) + "&mh=" + strings.Repeat("b", 64)
}

func TestCodexOwnerInviteCapability(t *testing.T) {
	calls := serveHook(t, func(localipc.Request) (any, error) { return map[string]string{}, nil })
	input := hookInput()
	link := hookInvitation()
	input["prompt"] = "owner prose stays private: " + link
	var out strings.Builder
	executeHook(t, input, &out)
	requests := calls()
	want := []string{link}
	if len(requests) != 1 || len(requests[0].OwnerInvites) != 1 || len(want) != 1 || requests[0].OwnerInvites[0] != want[0] || requests[0].Body != "" || requests[0].InvitationText != "" || requests[0].Link != "" {
		t.Fatal("owner prompt was not reduced to a native-bound invitation capability")
	}
}

func TestCodexPeerInviteDenied(t *testing.T) {
	calls := serveHook(t, func(localipc.Request) (any, error) { return map[string]string{}, nil })
	input := hookInput()
	frame, _ := hookPeerFrame(t, hookInvitation())
	input["prompt"] = frame
	var out strings.Builder
	executeHook(t, input, &out)
	requests := calls()
	if len(requests) != 1 || requests[0].Method != "codex_peer_started" || len(requests[0].OwnerInvites) != 0 {
		t.Fatal("queued peer prompt acquired owner invitation authority")
	}
}

func TestCodexOwnerLinkAllowsInformAck(t *testing.T) {
	calls := serveHook(t, func(localipc.Request) (any, error) { return hookOffer(), nil })
	input := hookInput()
	input["prompt"] = hookInvitation()
	var out strings.Builder
	executeHook(t, input, &out)
	requests := calls()
	if len(requests) != 2 || requests[1].Method != "codex_inform_ack" || len(requests[1].OwnerInvites) != 0 {
		t.Fatal("owner invitation capabilities prevented pending inform acknowledgement")
	}
}
