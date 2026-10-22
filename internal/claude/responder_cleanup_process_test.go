package claude

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

func TestResponderForkLivenessRequiresExactSessionArgument(t *testing.T) {
	child := "bbbbbbbb-cccc-4ddd-8eee-ffffffffffff"
	for _, arguments := range []string{
		"claude\x00--session-id\x00" + child + "\x00", "claude\x00--session-id=" + child + "\x00",
		"claude\x00--question\x00" + child + "\x00", "claude\x00--session-id\x00" + child + "-suffix\x00",
	} {
		processes := fstest.MapFS{"123/cmdline": {Data: []byte(arguments)}}
		active, err := responderForkActiveIn(processes, child, func(fs.FileInfo) (bool, error) { return true, nil })
		expected := strings.Contains(arguments, "--session-id\x00"+child+"\x00") || strings.Contains(arguments, "--session-id="+child+"\x00")
		if err != nil || active != expected {
			t.Fatalf("exact fork liveness: got %t, want %t: %v", active, expected, err)
		}
		active, err = responderForkActiveIn(processes, child, func(fs.FileInfo) (bool, error) { return false, nil })
		if active || err != nil {
			t.Fatal("another user's process affected local custody", err)
		}
	}
}

func TestResponderForkLivenessFailsClosedOnUnreadableEvidence(t *testing.T) {
	child := "bbbbbbbb-cccc-4ddd-8eee-ffffffffffff"
	for _, contents := range []string{"claude\x00--session-id\x00" + child, strings.Repeat("x", (32<<10)+1)} {
		processes := fstest.MapFS{"123/cmdline": {Data: []byte(contents)}}
		if _, err := responderForkActiveIn(processes, child, func(fs.FileInfo) (bool, error) { return true, nil }); err == nil {
			t.Fatal("unbounded or ambiguous process evidence accepted")
		}
	}
	processes := fstest.MapFS{"123/cmdline": {Data: []byte("claude\x00")}}
	if _, err := responderForkActiveIn(processes, child, func(fs.FileInfo) (bool, error) { return false, errors.New("unverified ownership") }); err == nil {
		t.Fatal("unverified process owner accepted")
	}
	if _, err := ResponderForkActive("../other"); err == nil {
		t.Fatal("malformed recovery UUID accepted")
	}
}
