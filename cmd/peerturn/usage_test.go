package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

// An agent that gets the syntax wrong must see the right syntax in the same
// answer, not a code that sends it hunting through transcripts.
func TestHelpAndInvalidArgumentsCarryUsage(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
		code int
	}{
		{"subcommand help flag", []string{"join", "--help"}, "peerturn join --current --link-stdin", 0},
		{"two-word help", []string{"grants", "revoke", "-h"}, "peerturn grants revoke ID", 0},
		{"help command", []string{"help", "doctor"}, "peerturn doctor", 0},
		{"group help", []string{"invite", "--help"}, "peerturn invite create", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := run(tc.args, &stdout, &stderr); got != tc.code {
				t.Fatalf("exit %d, want %d (%s)", got, tc.code, stderr.String())
			}
			if !strings.Contains(stdout.String(), tc.want) {
				t.Fatalf("usage %q lacks %q", stdout.String(), tc.want)
			}
		})
	}
	var stdout, stderr bytes.Buffer
	if got := run([]string{"send", "--session", "lab"}, &stdout, &stderr); got == 0 {
		t.Fatal("missing --peer accepted")
	}
	var failure struct {
		Error *localipc.Error `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &failure); err != nil || failure.Error == nil {
		t.Fatalf("no JSON error: %q", stdout.String())
	}
	if failure.Error.Code != "invalid_arguments" || !strings.Contains(failure.Error.Message, "--peer ID --kind coauthor --stdin") {
		t.Fatalf("error does not name the syntax: %+v", failure.Error)
	}
}
