package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantOutput string
		wantError  string
	}{
		{name: "no arguments", wantOutput: "Usage: peerturn <command>"},
		{name: "help", args: []string{"help"}, wantOutput: "Usage: peerturn <command>"},
		{name: "help flag", args: []string{"--help"}, wantOutput: "Usage: peerturn <command>"},
		{name: "short help", args: []string{"-h"}, wantOutput: "Usage: peerturn <command>"},
		{
			name: "version", args: []string{"version"},
			wantOutput: "peerturn dev (commit unknown, built unknown)\n",
		},
		{
			name: "unknown", args: []string{"missing"}, wantCode: 2,
			wantError: "unknown command \"missing\"",
		},
		{
			name: "relay config required", args: []string{"relay"}, wantCode: 2,
			wantError: "relay requires --config FILE",
		},
		{
			name: "extra arguments", args: []string{"version", "extra"}, wantCode: 2,
			wantError: "expected exactly one command",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			if got := run(tt.args, &stdout, &stderr); got != tt.wantCode {
				t.Errorf("exit code = %d, want %d", got, tt.wantCode)
			}
			if tt.wantError != "" {
				if stdout.Len() != 0 || !strings.Contains(stderr.String(), tt.wantError) ||
					!strings.Contains(stderr.String(), "Usage: peerturn <command>") {
					t.Errorf("stdout = %q, stderr = %q; want error and usage on stderr only", stdout.String(), stderr.String())
				}
				return
			}
			if stderr.Len() != 0 || !strings.Contains(stdout.String(), tt.wantOutput) {
				t.Errorf("stdout = %q, stderr = %q; want %q on stdout only", stdout.String(), stderr.String(), tt.wantOutput)
			}
		})
	}
}
