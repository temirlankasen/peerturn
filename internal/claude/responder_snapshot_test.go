package claude

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/runtimepolicy"
)

const responderLeaf = "11111111-2222-4333-8444-555555555555"

// The exported entrypoint takes its platform from the host and offers no override.
var _ func(ResponderOptions, string) (*ResponderSnapshot, error) = AcquireResponderSnapshot

// Parser and lifecycle fixtures run on the one platform the packaged policy
// qualifies for restricted ask, so the CI host's architecture cannot change them.
const (
	snapshotFixtureOS   = "linux"
	snapshotFixtureArch = "arm64"
)

func acquireFixtureSnapshot(options ResponderOptions, parent string) (*ResponderSnapshot, error) {
	return acquireResponderSnapshot(options, parent, snapshotFixtureOS, snapshotFixtureArch)
}

func writeSnapshotFixture(t *testing.T, r *Responder, parent string) {
	t.Helper()
	directory := filepath.Join(responderConfigDirectory(r.options), "projects", "fixture")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	data := snapshotFixtureData(parent, r.options.WorkingDirectory)
	if err := os.WriteFile(filepath.Join(directory, parent+".jsonl"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func responderSnapshotFixture(t *testing.T, r *Responder, parents ...string) *ResponderSnapshot {
	t.Helper()
	parent := responderParent
	if len(parents) > 0 {
		parent = parents[0]
	}
	writeSnapshotFixture(t, r, parent)
	snapshot, err := acquireFixtureSnapshot(r.options, parent)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func requireSnapshotRefused(t *testing.T, snapshot *ResponderSnapshot, err error) {
	t.Helper()
	var failure *ResponderError
	if snapshot != nil || !errors.As(err, &failure) || failure.Code != SnapshotUnavailable {
		t.Fatalf("snapshot issued = %t, err = %v; want %s with no snapshot", snapshot != nil, err, SnapshotUnavailable)
	}
}

func snapshotFixtureData(session, directory string) []byte {
	var data []byte
	for _, record := range []map[string]any{
		{"type": "api-request-shape", "sessionId": session, "shape": map[string]any{"system": []string{"fixture persona"},
			"tools": []map[string]string{{"name": "Read"}, {"name": "Grep"}, {"name": "Glob"}}}},
		{"type": "assistant", "sessionId": session, "uuid": responderLeaf, "cwd": directory,
			"version": "2.1.306", "requestedModel": "fixture-model", "effort": "high", "perTurnEffort": nil,
			"message": map[string]string{"model": "fixture-model", "stop_reason": "end_turn"}},
		{"type": "last-prompt", "sessionId": session, "leafUuid": responderLeaf},
	} {
		encoded, err := json.Marshal(record)
		if err != nil {
			panic(err)
		}
		data = append(append(data, encoded...), '\n')
	}
	return data
}

func TestResponderSnapshotRejectsMissingTornOrDriftedNativeMetadata(t *testing.T) {
	r := responderFixture(t)
	snapshot := responderSnapshotFixture(t, r)
	if len(snapshot.Evidence()) == 0 || len(snapshot.Evidence()) > 4096 {
		t.Fatal("snapshot evidence is missing or unbounded")
	}
	snapshot.Model = "different-model"
	if snapshot.verify(responderParent, r.options.WorkingDirectory) == nil {
		t.Fatal("caller labels substituted native model evidence")
	}
	snapshot.Model = snapshot.qualifiedModel
	data := snapshotFixtureData(responderParent, r.options.WorkingDirectory)
	for _, invalid := range [][]byte{
		data[:len(data)-1],
		[]byte(strings.ReplaceAll(string(data), `"effort":"high"`, `"effort":"unknown"`)),
		[]byte(strings.ReplaceAll(string(data), `"perTurnEffort":null`, `"perTurnEffort":"max"`)),
		[]byte(strings.ReplaceAll(string(data), `"version":"2.1.306"`, `"version":"2.1.307"`)),
		[]byte(strings.ReplaceAll(string(data), `"version":"2.1.306"`, `"version":"2.1.306 (Claude Code)"`)),
		[]byte(strings.ReplaceAll(string(data), `"stop_reason":"end_turn"`, `"stop_reason":"tool_use"`)),
		[]byte(strings.ReplaceAll(string(data), `"requestedModel":"fixture-model"`, `"requestedModel":"--bad"`)),
	} {
		if err := os.WriteFile(snapshot.path, invalid, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := acquireFixtureSnapshot(r.options, responderParent); err == nil {
			t.Fatal("unqualified conversation accepted")
		}
	}
	if err := os.WriteFile(snapshot.path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := snapshot.verify(responderParent, r.options.WorkingDirectory); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(snapshot.path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.WriteString("native later owner turn\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := snapshot.verify(responderParent, r.options.WorkingDirectory); err != nil {
		t.Fatal("later owner append invalidated immutable prefix")
	}
	if err := os.WriteFile(snapshot.path, []byte("changed prefix\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := snapshot.verify(responderParent, r.options.WorkingDirectory); err == nil {
		t.Fatal("changed prefix was accepted")
	}
}

func TestResponderObserverRejectsAndDiscardsLateAnswer(t *testing.T) {
	r := responderFixture(t)
	r.options.Observer = func(_ context.Context, stage ResponderStage, child string) error {
		if child == "" {
			t.Error("stage without native child evidence")
		}
		if stage == ResponderModelInput {
			return context.Canceled
		}
		return nil
	}
	result, err := r.Run(t.Context(), AskRequest{Snapshot: responderSnapshotFixture(t, r),
		NativeSessionID: responderParent, FramedQuestion: "success"})
	requireResponderFailure(t, result, err, ResponderCancelled)
}

func TestResponderSnapshotRecognizesPinnedNativeFileHistoryMetadata(t *testing.T) {
	r := responderFixture(t)
	snapshot := responderSnapshotFixture(t, r)
	data := snapshotFixtureData(responderParent, r.options.WorkingDirectory)
	for _, kind := range []string{"file-history-snapshot", "unknown-metadata"} {
		prefix := []byte(`{"type":"` + kind + `","snapshot":{}}` + "\n")
		if err := os.WriteFile(snapshot.path, append(prefix, data...), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := acquireFixtureSnapshot(r.options, responderParent)
		if (err == nil) != (kind == "file-history-snapshot") {
			t.Fatalf("native metadata %s: %v", kind, err)
		}
	}
}

func TestResponderSnapshotRefusesUnqualifiedSyntheticPlatform(t *testing.T) {
	r := responderFixture(t)
	writeSnapshotFixture(t, r, responderParent)
	for _, platform := range []struct {
		os, arch  string
		qualified bool
	}{
		{"linux", "arm64", true},
		{"darwin", "arm64", true},
		{"linux", "amd64", false},
		{"darwin", "amd64", false},
		{"windows", "arm64", false},
	} {
		t.Run(platform.os+"-"+platform.arch, func(t *testing.T) {
			snapshot, err := acquireResponderSnapshot(r.options, responderParent, platform.os, platform.arch)
			if platform.qualified {
				if err != nil || snapshot == nil {
					t.Fatalf("qualified platform refused: %v", err)
				}
				return
			}
			requireSnapshotRefused(t, snapshot, err)
		})
	}
}

func TestAcquireResponderSnapshotFollowsActualHostQualification(t *testing.T) {
	r := responderFixture(t)
	writeSnapshotFixture(t, r, responderParent)
	hostQualified := runtimepolicy.EvaluateForPlatform(runtimepolicy.Claude, "2.1.306", runtime.GOOS, runtime.GOARCH).Status == runtimepolicy.Verified
	snapshot, err := AcquireResponderSnapshot(r.options, responderParent)
	if hostQualified {
		if err != nil || snapshot == nil {
			t.Fatalf("qualified host refused: %v", err)
		}
		return
	}
	requireSnapshotRefused(t, snapshot, err)
}
