package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestResponderActualToolInventoryGuardsPeerAdmission(t *testing.T) {
	for index, tools := range []string{
		`[{"name":"Read"},{"name":"Grep"},{"name":"Glob"}]`,
		`[{"name":"Read"},{"name":"Grep"},{"name":"Glob"},{"name":"ScheduleWakeup"}]`,
		`[{"name":"Read"},{"name":"Grep"},{"name":"ToolSearch"}]`,
		`[{"name":"Read"},{"name":"Read"},{"name":"Glob"}]`,
		`null`, `[]`,
	} {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			r := responderFixture(t)
			snapshot := responderSnapshotFixture(t, r)
			child := "bbbbbbbb-cccc-4ddd-8eee-ffffffffffff"
			var records []json.RawMessage
			for line := range strings.SplitSeq(strings.TrimSpace(string(forkFixtureData(child, r.options.WorkingDirectory))), "\n") {
				records = append(records, json.RawMessage(line))
			}
			var shape map[string]any
			if err := json.Unmarshal(records[1], &shape); err != nil {
				t.Fatal(err)
			}
			definition, ok := shape["shape"].(map[string]any)
			if !ok {
				t.Fatal("invalid fixture shape")
			}
			definition["tools"] = json.RawMessage(tools)
			encoded, err := json.Marshal(shape)
			if err != nil {
				t.Fatal(err)
			}
			records[1] = encoded
			var data []byte
			for _, record := range records {
				data = append(append(data, record...), '\n')
			}
			if err = os.WriteFile(filepath.Join(filepath.Dir(snapshot.path), child+".jsonl"), data, 0600); err != nil {
				t.Fatal(err)
			}
			admitted := false
			r.options.AdmitPeerInput = func(_ context.Context, _ string, submit func() error) error {
				admitted = true
				return submit()
			}
			stream := responderStream{ctx: t.Context(), initialized: true,
				request:   AskRequest{NativeSessionID: responderParent, Snapshot: snapshot, FramedQuestion: "untrusted peer"},
				responder: r, input: &responderInputCapture{}, result: &AskResult{NativeSessionID: child}}
			line := `{"type":"result","subtype":"success","is_error":false,"session_id":"` + child + `","result":"PEERTURN_PROFILE_OK"}`
			err = stream.event([]byte(line))
			valid := tools == `[{"name":"Read"},{"name":"Grep"},{"name":"Glob"}]`
			if admitted != valid || (err == nil) != valid || stream.peerInput != valid {
				t.Fatal("actual provider inventory did not guard peer admission")
			}
		})
	}
}

func forkFixtureData(child, directory string) []byte {
	probe, err := json.Marshal(map[string]any{"type": "user", "sessionId": child,
		"message": map[string]string{"role": "user", "content": responderProbe}})
	if err != nil {
		panic(err)
	}
	return append(append(probe, '\n'), snapshotFixtureData(child, directory)...)
}

func TestResponderFreshShapeCannotBeInherited(t *testing.T) {
	r := responderFixture(t)
	snapshot := responderSnapshotFixture(t, r)
	child := "bbbbbbbb-cccc-4ddd-8eee-ffffffffffff"
	data := forkFixtureData(child, r.options.WorkingDirectory)
	owner := []byte(`{"type":"api-request-shape","sessionId":"` + child + `","shape":{"tools":[{"name":"Bash"}]}}` + "\n")
	for _, fixture := range []struct {
		name  string
		data  []byte
		valid bool
	}{
		{"owner history", append(append([]byte(nil), owner...), data...), true},
		{"no trusted probe", snapshotFixtureData(child, r.options.WorkingDirectory), false},
		{"old identical probe", append(append([]byte(nil), data...), data[:strings.IndexByte(string(data), '\n')+1]...), false},
		{"wrong session", []byte(strings.ReplaceAll(string(data), child, responderParent)), false},
		{"late inventory drift", append(append([]byte(nil), data...), owner...), false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(filepath.Dir(snapshot.path), child+".jsonl"), fixture.data, 0600); err != nil {
				t.Fatal(err)
			}
			if (snapshot.verifyFork(child) == nil) != fixture.valid {
				t.Fatal("inherited or later native inventory bypassed qualification")
			}
		})
	}
}
