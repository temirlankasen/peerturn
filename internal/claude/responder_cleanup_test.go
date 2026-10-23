package claude

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestResponderCleansForkJournalOnEveryFinishedProcess(t *testing.T) {
	for _, mode := range []string{"success", "wrong-type", "oversized", "sleep", "observer-failure"} {
		t.Run(mode, func(t *testing.T) {
			r := responderFixture(t)
			snapshot := responderSnapshotFixture(t, r)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "sleep" {
				// Cancellation starts after the child is observed, so host startup
				// latency cannot turn this cleanup check into a pre-init timeout.
				r.options.Timeout = 30 * time.Second
				r.options.Observer = func(_ context.Context, stage ResponderStage, _ string) error {
					if stage == ResponderModelInput {
						cancel()
					}
					return nil
				}
			}
			if mode == "observer-failure" {
				r.options.Observer = func(context.Context, ResponderStage, string) error { return errors.New("fixture observation failed") }
			}
			result, err := r.Run(ctx, AskRequest{Snapshot: snapshot, NativeSessionID: responderParent, FramedQuestion: mode})
			if mode == "success" && err != nil || mode != "success" && err == nil {
				t.Fatal("unexpected process result", err)
			}
			child := result.NativeSessionID
			if child == "" {
				t.Fatal("fixture child was not observed")
			}
			if _, statErr := os.Stat(filepath.Join(filepath.Dir(snapshot.path), child+".jsonl")); !os.IsNotExist(statErr) {
				t.Fatal("plaintext fork transcript survived process exit", statErr)
			}
			if _, statErr := os.Stat(snapshot.path); statErr != nil {
				t.Fatal("cleanup changed owner transcript", statErr)
			}
		})
	}
}

func TestResponderPreparesForkCustodyBeforeProcessStart(t *testing.T) {
	r := responderFixture(t)
	snapshot := responderSnapshotFixture(t, r)
	prepared := ""
	r.options.PrepareFork = func(_ context.Context, child string) error {
		if !validNativeSessionID(child) || child == responderParent || prepared != "" {
			t.Fatal("invalid preselected child custody")
		}
		prepared = child
		if _, err := os.Stat(filepath.Join(filepath.Dir(snapshot.path), child+".jsonl")); !os.IsNotExist(err) {
			t.Fatal("process artifact existed before durable custody")
		}
		return nil
	}
	r.options.Observer = func(_ context.Context, _ ResponderStage, child string) error {
		if child != prepared || prepared == "" {
			t.Fatal("native process started before selected fork custody")
		}
		return nil
	}
	if _, err := r.Run(t.Context(), AskRequest{Snapshot: snapshot, NativeSessionID: responderParent, FramedQuestion: "success"}); err != nil {
		t.Fatal(err)
	}
	for _, prepare := range []func(context.Context, string) error{nil, func(context.Context, string) error { return errors.New("fixture durable failure") }} {
		r.options.PrepareFork = prepare
		result, err := r.Run(t.Context(), AskRequest{Snapshot: snapshot, NativeSessionID: responderParent, FramedQuestion: "success"})
		if err == nil || result.Answer != "" || len(result.Stages) != 0 {
			t.Fatal("fork launched without durable custody", err)
		}
	}
}

func TestResponderCleansKnownForkBeforeNativeInit(t *testing.T) {
	for _, mode := range []string{"pre-init-exit", "wrong-child-init"} {
		t.Run(mode, func(t *testing.T) {
			r := responderFixture(t)
			snapshot := responderSnapshotFixture(t, r)
			marker := filepath.Join(r.options.HomeDirectory, ".claude", "fixture-process-mode")
			if err := os.WriteFile(marker, []byte(mode), 0600); err != nil {
				t.Fatal(err)
			}
			result, err := r.Run(t.Context(), AskRequest{Snapshot: snapshot, NativeSessionID: responderParent, FramedQuestion: "success"})
			if err == nil || result.Answer != "" || result.NativeSessionID != "" || len(result.Stages) != 0 {
				t.Fatal("invalid initialization became accepted evidence", err)
			}
			entries, err := os.ReadDir(filepath.Dir(snapshot.path))
			if err != nil || len(entries) != 1 || entries[0].Name() != responderParent+".jsonl" {
				t.Fatal("unobserved fork transcript survived process exit", err)
			}
		})
	}
}

func TestResponderRecoveryCleanupKeepsOtherSessionsAndSymlinkTargets(t *testing.T) {
	r := responderFixture(t)
	snapshot := responderSnapshotFixture(t, r)
	child := "bbbbbbbb-cccc-4ddd-8eee-ffffffffffff"
	base := filepath.Join(r.options.HomeDirectory, ".claude")
	artifacts := []string{
		"projects/fixture/" + child + ".jsonl", "projects/fixture/" + child + "/tool-results/private.txt",
		"debug/" + child + ".txt", "todos/" + child + "-agent-" + child + ".json",
		"tasks/" + child + "/1.json", "session-env/" + child + "/env", "file-history/" + child + "/backup",
	}
	for _, path := range artifacts {
		target := filepath.Join(base, path)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte("private fixture artifact"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(t.TempDir(), "keep")
	if err := os.WriteFile(outside, []byte("unrelated fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(base, "tasks", child, "alias")); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := CleanupResponderArtifacts(r.options, responderParent, child); err != nil {
			t.Fatal("idempotent recovery cleanup failed", err)
		}
	}
	for _, path := range artifacts {
		if _, err := os.Stat(filepath.Join(base, path)); !os.IsNotExist(err) {
			t.Errorf("fork artifact survived cleanup: %s: %v", path, err)
		}
	}
	for _, path := range []string{snapshot.path, outside} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("cleanup changed another session or symlink target", err)
		}
	}
	for _, invalid := range []string{responderParent, "../other", "", "/tmp/other"} {
		if err := CleanupResponderArtifacts(r.options, responderParent, invalid); err == nil {
			t.Fatal("unsafe cleanup target accepted")
		}
	}
}

func TestResponderCleanupFailureSuppressesCompletedAnswer(t *testing.T) {
	r := responderFixture(t)
	snapshot := responderSnapshotFixture(t, r)
	base := filepath.Join(r.options.HomeDirectory, ".claude")
	outside := t.TempDir()
	r.options.Observer = func(_ context.Context, stage ResponderStage, _ string) error {
		if stage == OutboundRedacted {
			return os.Symlink(outside, filepath.Join(base, "todos"))
		}
		return nil
	}
	result, err := r.Run(t.Context(), AskRequest{Snapshot: snapshot, NativeSessionID: responderParent, FramedQuestion: "success"})
	requireResponderFailure(t, result, err, ForkCleanupFailed)
	if !slices.Contains(result.Stages, ResponderCompleted) {
		t.Fatal("fixture never reached completed output")
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(snapshot.path), result.NativeSessionID+".jsonl")); !os.IsNotExist(statErr) {
		t.Fatal("independent fork journal survived a different cleanup failure", statErr)
	}
	if err := os.Remove(filepath.Join(base, "todos")); err != nil {
		t.Fatal(err)
	}
	if err := CleanupResponderArtifacts(r.options, responderParent, result.NativeSessionID); err != nil {
		t.Fatal("recovery cleanup failed", err)
	}
}
