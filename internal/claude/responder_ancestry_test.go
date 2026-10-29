package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResponderSnapshotRejectsAmbiguousOrNonterminalLeaf(t *testing.T) {
	for _, scenario := range []string{"duplicate_uuid", "user_leaf", "stale_leaf", "other_session", "duplicate_session"} {
		t.Run(scenario, func(t *testing.T) {
			r := responderFixture(t)
			snapshot := responderSnapshotFixture(t, r)
			data := snapshotFixtureData(responderParent, r.options.WorkingDirectory)
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			switch scenario {
			case "duplicate_uuid":
				data = []byte(strings.Join([]string{lines[0], lines[1], lines[1], lines[2]}, "\n") + "\n")
			case "user_leaf":
				data = []byte(strings.Join([]string{lines[0], lines[1],
					`{"type":"user","sessionId":"` + responderParent + `","uuid":"` + responderParent + `"}`,
					strings.ReplaceAll(lines[2], responderLeaf, responderParent)}, "\n") + "\n")
			case "stale_leaf":
				data = []byte(strings.Join([]string{lines[0], lines[1], lines[2],
					`{"type":"user","sessionId":"` + responderParent + `","uuid":"` + responderParent + `"}`}, "\n") + "\n")
			case "other_session":
				data = []byte(strings.ReplaceAll(string(data), responderParent, responderLeaf))
			case "duplicate_session":
				other := filepath.Join(filepath.Dir(filepath.Dir(snapshot.path)), "duplicate")
				if err := os.MkdirAll(other, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(other, responderParent+".jsonl"), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(snapshot.path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := acquireFixtureSnapshot(r.options, responderParent); err == nil {
				t.Fatal("ambiguous or nonterminal native parent accepted")
			}
		})
	}
}

func TestResponderSnapshotRechecksSessionUniqueness(t *testing.T) {
	r := responderFixture(t)
	snapshot := responderSnapshotFixture(t, r)
	other := filepath.Join(filepath.Dir(filepath.Dir(snapshot.path)), "duplicate")
	if err := os.MkdirAll(other, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, responderParent+".jsonl"), snapshotFixtureData(responderParent, r.options.WorkingDirectory), 0600); err != nil {
		t.Fatal(err)
	}
	if snapshot.verify(responderParent, r.options.WorkingDirectory) == nil {
		t.Fatal("ambiguous session appeared after acquisition")
	}
}

func TestResponderSnapshotSelectsCompletedLeafAfterNativeLastPrompt(t *testing.T) {
	r := responderFixture(t)
	snapshot := responderSnapshotFixture(t, r)
	lines := strings.Split(strings.TrimSpace(string(snapshotFixtureData(responderParent, r.options.WorkingDirectory))), "\n")
	user := "22222222-2222-4333-8444-555555555555"
	data := strings.Join([]string{lines[0],
		`{"type":"user","sessionId":"` + responderParent + `","uuid":"` + user + `"}`,
		strings.ReplaceAll(lines[2], responderLeaf, user),
		strings.Replace(lines[1], `"cwd":`, `"parentUuid":"`+user+`","cwd":`, 1),
		`{"type":"system","subtype":"turn_duration","sessionId":"` + responderParent + `","uuid":"` + responderParent + `","parentUuid":"` + responderLeaf + `"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(snapshot.path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := acquireFixtureSnapshot(r.options, responderParent)
	if err != nil || got.leaf != responderLeaf {
		t.Fatal("native last-prompt is a pre-generation boundary; expected completed assistant leaf", err)
	}
	data = strings.Replace(data, `"parentUuid":"`+user+`"`, `"parentUuid":"33333333-2222-4333-8444-555555555555"`, 1)
	if err := os.WriteFile(snapshot.path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := acquireFixtureSnapshot(r.options, responderParent); err == nil {
		t.Fatal("dangling native parent accepted")
	}
}
