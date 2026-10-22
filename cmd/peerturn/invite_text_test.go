package main

import (
	"strings"
	"testing"
)

func TestJoinPastedInvitationTextStaysUnparsedInIPC(t *testing.T) {
	link := testInvitationLink()
	command, err := parseCommand([]string{"join", "--session", "chosen", "--scope", "coauthor", "--link-stdin"})
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{link, "Join my selected session:\n[PeerTurn invitation](" + link + ")\nKeep the fragment private.", "[" + link + "](" + link + ")", "Only the daemon parses this text."} {
		request, requestErr := command.request(strings.NewReader(text))
		if requestErr != nil || request.Method != "join.text" || request.InvitationText != text || request.Link != "" {
			t.Fatalf("full pasted text was not preserved for daemon parsing; error=%v", requestErr)
		}
	}
	for _, text := range []string{"", "\n", link + "\x00", strings.Repeat("a", 65537), string([]byte{0xff})} {
		if _, err = command.request(strings.NewReader(text)); err == nil {
			t.Fatal("accepted empty, invalid encoding or oversized invitation text")
		}
	}
}

func testInvitationLink() string {
	return "https://relay.example/i/" + strings.Repeat("A", 22) + "#v=1&ik=" + strings.Repeat("A", 43) + "&ek=" + strings.Repeat("A", 43) + "&fp=" + strings.Repeat("a", 64) + "&mh=" + strings.Repeat("b", 64)
}
