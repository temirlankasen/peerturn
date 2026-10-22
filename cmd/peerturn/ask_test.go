package main

import (
	"strings"
	"testing"
)

func TestAskSenderDefersRuntimeAdmissionToReceiver(t *testing.T) {
	t.Setenv("CODEX_THREAD_ID", "")
	command, err := parseCommand([]string{"send", "--session", "chosen", "--peer", "peer", "--kind", "ask", "--stdin"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := command.request(strings.NewReader("literal @path"))
	if err != nil || r.Kind != "ask" || r.Body != "literal @path" {
		t.Fatal("ask never reaches recipient admission", r, err)
	}
}
