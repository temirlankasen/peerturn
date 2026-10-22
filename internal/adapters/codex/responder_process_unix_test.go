//go:build darwin || linux

package codex

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestResponderNativeSupervisorBoundsAndDiscardsFailedOutput(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		mode, input, reason string
		limit               int
	}{
		{"copy", "safe final bytes", "", 64},
		{"exit", "raw partial answer", "responder_failed", 64},
		{"copy", strings.Repeat("x", 1024), "budget_exceeded", 16},
		{"wait", "", "cancelled", 64},
	} {
		t.Run(tc.mode+tc.reason, func(t *testing.T) {
			deadline := 3 * time.Second
			if tc.mode == "wait" {
				deadline = 100 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(t.Context(), deadline)
			defer cancel()
			command := ResponderCommand{Executable: executable, Directory: t.TempDir(), Environment: []string{"PEERTURN_CHILD_TEST=1", "GORACE=atexit_sleep_ms=0"},
				Args: []string{"-test.run=^TestResponderChildFixture$", "--", tc.mode}, Stdin: []byte(tc.input)}
			got, runErr := runResponderProcess(ctx, command, tc.limit)
			if tc.reason == "" {
				if runErr != nil || string(got) != tc.input {
					t.Fatalf("output %q error %v", got, runErr)
				}
			} else if responderReason(runErr) != tc.reason || len(got) != 0 {
				t.Fatalf("output %q error %v", got, runErr)
			}
		})
	}
}

func TestResponderChildFixture(_ *testing.T) {
	if os.Getenv("PEERTURN_CHILD_TEST") != "1" {
		return
	}
	switch os.Args[len(os.Args)-1] {
	case "copy", "exit":
		if _, err := io.Copy(os.Stdout, os.Stdin); err != nil {
			os.Exit(2)
		}
		if os.Args[len(os.Args)-1] == "exit" {
			os.Exit(3)
		}
	case "wait":
		time.Sleep(10 * time.Second)
	default:
		os.Exit(2)
	}
	os.Exit(0)
}
