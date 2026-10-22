package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const responderLeaf = "11111111-2222-4333-8444-555555555555"

func responderSnapshotFixture(t *testing.T, r *Responder, parents ...string) *ResponderSnapshot {
	t.Helper()
	parent := responderParent
	if len(parents) > 0 {
		parent = parents[0]
	}
	directory := filepath.Join(responderConfigDirectory(r.options), "projects", "fixture")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	data := snapshotFixtureData(parent, r.options.WorkingDirectory)
	if err := os.WriteFile(filepath.Join(directory, parent+".jsonl"), data, 0600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := AcquireResponderSnapshot(r.options, parent)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
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
		[]byte(strings.ReplaceAll(string(data), `"stop_reason":"end_turn"`, `"stop_reason":"tool_use"`)),
		[]byte(strings.ReplaceAll(string(data), `"requestedModel":"fixture-model"`, `"requestedModel":"--bad"`)),
	} {
		if err := os.WriteFile(snapshot.path, invalid, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := AcquireResponderSnapshot(r.options, responderParent); err == nil {
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
		_, err := AcquireResponderSnapshot(r.options, responderParent)
		if (err == nil) != (kind == "file-history-snapshot") {
			t.Fatalf("native metadata %s: %v", kind, err)
		}
	}
}
