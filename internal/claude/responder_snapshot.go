package claude

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/runtimepolicy"
)

// ResponderSnapshot is an opaque native committed boundary. Its private fields
// prevent caller-provided model labels from becoming continuity evidence.
type ResponderSnapshot struct {
	WorkingDirectory, Model, Effort string
	parent, path, leaf, pattern     string
	qualifiedModel, qualifiedEffort string
	effectiveModel                  string
	size                            int64
	digest, system                  [32]byte
}

// Evidence is bounded local metadata for sealed run custody, never a peer body.
func (s *ResponderSnapshot) Evidence() []byte {
	if s == nil {
		return nil
	}
	data, err := json.Marshal(struct {
		Session, Directory, Boundary, Model, Effort string
		ConfigurationDirectory                      string
		Digest                                      [32]byte
	}{s.parent, s.WorkingDirectory, s.leaf, s.Model, s.Effort, filepath.Dir(filepath.Dir(filepath.Dir(s.path))), s.digest})
	if err != nil || len(data) > 4096 {
		return nil
	}
	return data
}

type snapshotRecord struct {
	Type           string          `json:"type"`
	Subtype        string          `json:"subtype"`
	Session        string          `json:"sessionId"`
	UUID           string          `json:"uuid"`
	ParentUUID     string          `json:"parentUuid"`
	Leaf           string          `json:"leafUuid"`
	CWD            string          `json:"cwd"`
	Version        string          `json:"version"`
	RequestedModel string          `json:"requestedModel"`
	Effort         string          `json:"effort"`
	PerTurnEffort  *string         `json:"perTurnEffort"`
	Message        json.RawMessage `json:"message"`
	Shape          struct {
		System json.RawMessage         `json:"system"`
		Tools  []struct{ Name string } `json:"tools"`
	} `json:"shape"`
}

// AcquireResponderSnapshot reads only the explicitly bound local UUID's native
// journal. It creates no attachment and does not write or trim owner history.
func AcquireResponderSnapshot(options ResponderOptions, parent string) (*ResponderSnapshot, error) {
	return acquireResponderSnapshot(options, parent, runtime.GOOS, runtime.GOARCH)
}

// acquireResponderSnapshot receives platform facts only from in-package code.
// Only the exported entrypoint above may pass the running host's, and neither a
// caller nor a peer can choose which platform qualifies the journal.
func acquireResponderSnapshot(options ResponderOptions, parent, goos, goarch string) (*ResponderSnapshot, error) {
	failure := &ResponderError{Code: SnapshotUnavailable}
	if !validNativeSessionID(parent) || !validResponderPath(options.WorkingDirectory) {
		return nil, failure
	}
	pattern := filepath.Join(responderConfigDirectory(options), "projects", "*", parent+".jsonl")
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) != 1 {
		return nil, failure
	}
	data, err := readSnapshotJournal(matches[0])
	if err != nil {
		return nil, failure
	}
	records, err := decodeSnapshotRecords(data)
	if err != nil {
		return nil, failure
	}
	snapshot := &ResponderSnapshot{parent: parent, path: matches[0], pattern: pattern, size: int64(len(data)), digest: sha256.Sum256(data)}
	var assistant snapshotRecord
	var lastUUID, promptLeaf string
	uuids := make(map[string]bool)
	for _, record := range records {
		// This pinned native metadata has no sessionId and contributes no
		// conversation boundary. It remains covered by the complete prefix hash.
		if record.Session == "" && record.Type == "file-history-snapshot" {
			continue
		}
		if record.Session != parent {
			return nil, failure
		}
		if record.UUID != "" {
			if !validNativeSessionID(record.UUID) || uuids[record.UUID] ||
				record.ParentUUID != "" && !uuids[record.ParentUUID] {
				return nil, failure
			}
			uuids[record.UUID] = true
			// The pinned TUI appends timing metadata after the final assistant.
			// It does not add model context or change the completed resume leaf.
			if record.Type != "system" || record.Subtype != "turn_duration" {
				lastUUID = record.UUID
			}
		}
		switch record.Type {
		case "assistant":
			assistant = record
		case "last-prompt":
			promptLeaf = record.Leaf
		case "api-request-shape":
			if len(record.Shape.System) > 0 {
				snapshot.system = sha256.Sum256(record.Shape.System)
			}
		}
	}
	var message struct {
		Stop  string `json:"stop_reason"`
		Model string `json:"model"`
	}
	if json.Unmarshal(assistant.Message, &message) != nil {
		return nil, failure
	}
	// last-prompt names the pre-generation input in ordinary TUI transcripts.
	// Resume the completed assistant instead of replaying that unfinished turn.
	snapshot.leaf = assistant.UUID
	version := runtimepolicy.EvaluateForPlatform(runtimepolicy.Claude, assistant.Version, goos, goarch)
	if !validNativeSessionID(promptLeaf) || !uuids[promptLeaf] || !validNativeSessionID(snapshot.leaf) ||
		snapshot.leaf != lastUUID || message.Stop != "end_turn" ||
		version.Status != runtimepolicy.Verified || version.Version != assistant.Version || assistant.CWD != options.WorkingDirectory ||
		!validSnapshotModel(assistant.RequestedModel) || !validSnapshotEffort(assistant.Effort) ||
		assistant.PerTurnEffort != nil || snapshot.system == ([32]byte{}) {
		return nil, failure
	}
	snapshot.WorkingDirectory, snapshot.Model, snapshot.Effort = assistant.CWD, assistant.RequestedModel, assistant.Effort
	snapshot.qualifiedModel, snapshot.qualifiedEffort, snapshot.effectiveModel = snapshot.Model, snapshot.Effort, message.Model
	return snapshot, nil
}

func (s *ResponderSnapshot) verify(parent, directory string) (verificationErr error) {
	if s == nil || s.parent != parent || s.WorkingDirectory != directory || s.path == "" || s.size == 0 ||
		s.Model != s.qualifiedModel || s.Effort != s.qualifiedEffort || s.effectiveModel == "" {
		return &ResponderError{Code: SnapshotUnavailable}
	}
	matches, err := filepath.Glob(s.pattern)
	if err != nil || len(matches) != 1 || matches[0] != s.path {
		return &ResponderError{Code: SnapshotUnavailable}
	}
	file, err := openResponderFile(s.path)
	if err != nil {
		return &ResponderError{Code: SnapshotUnavailable}
	}
	defer func() {
		if file.Close() != nil {
			verificationErr = &ResponderError{Code: SnapshotUnavailable}
		}
	}()
	hash := sha256.New()
	n, err := io.CopyN(hash, file, s.size)
	if err != nil || n != s.size || !bytes.Equal(hash.Sum(nil), s.digest[:]) {
		return &ResponderError{Code: SnapshotUnavailable}
	}
	return nil
}

func (s *ResponderSnapshot) verifyFork(child string) error {
	data, err := readSnapshotJournal(filepath.Join(filepath.Dir(s.path), child+".jsonl"))
	if err != nil {
		return &ResponderError{Code: SnapshotUnavailable}
	}
	records, err := decodeSnapshotRecords(data)
	if err != nil {
		return &ResponderError{Code: SnapshotUnavailable}
	}
	probe := -1
	for index, record := range records {
		if record.Type == "user" && record.Session == child {
			var message struct{ Content string }
			if json.Unmarshal(record.Message, &message) == nil && message.Content == responderProbe {
				probe = index
			}
		}
	}
	var modelVerified, systemVerified bool
	for index, record := range records {
		if record.Type == "api-request-shape" {
			// The fork rewrites inherited session IDs. Its trusted probe, rather
			// than the rewritten ID, separates fresh inventory from owner history.
			// An identical older probe cannot qualify the latest probe's request.
			if probe < 0 || index <= probe {
				continue
			}
			if record.Session != child || len(record.Shape.System) == 0 {
				return &ResponderError{Code: ProfileMismatch}
			}
			seen := make(map[string]bool)
			for _, tool := range record.Shape.Tools {
				if seen[tool.Name] || tool.Name != "Read" && tool.Name != "Grep" && tool.Name != "Glob" {
					return &ResponderError{Code: ProfileMismatch}
				}
				seen[tool.Name] = true
			}
			if len(seen) != 3 {
				return &ResponderError{Code: ProfileMismatch}
			}
			if sha256.Sum256(record.Shape.System) != s.system {
				return &ResponderError{Code: SnapshotUnavailable}
			}
			systemVerified = true
		}
		if record.Type == "assistant" {
			if record.RequestedModel != s.Model || record.Effort != s.Effort || record.PerTurnEffort != nil {
				return &ResponderError{Code: ModelMismatch}
			}
			modelVerified = true
		}
	}
	if !modelVerified || !systemVerified {
		return &ResponderError{Code: SnapshotUnavailable}
	}
	return nil
}

func readSnapshotJournal(path string) (data []byte, readErr error) {
	file, err := openResponderFile(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if file.Close() != nil {
			data, readErr = nil, &ResponderError{Code: SnapshotUnavailable}
		}
	}()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 64<<20 {
		return nil, &ResponderError{Code: SnapshotUnavailable}
	}
	data, err = io.ReadAll(io.LimitReader(file, (64<<20)+1))
	if err != nil || int64(len(data)) != info.Size() || data[len(data)-1] != '\n' {
		return nil, &ResponderError{Code: SnapshotUnavailable}
	}
	return data, nil
}

func decodeSnapshotRecords(data []byte) ([]snapshotRecord, error) {
	var records []snapshotRecord
	for line := range bytes.SplitSeq(bytes.TrimSuffix(data, []byte{'\n'}), []byte{'\n'}) {
		if codec.Validate(line, 8<<20) != nil {
			return nil, &ResponderError{Code: SnapshotUnavailable}
		}
		var record snapshotRecord
		if json.Unmarshal(line, &record) != nil {
			return nil, &ResponderError{Code: SnapshotUnavailable}
		}
		records = append(records, record)
	}
	return records, nil
}

func validSnapshotModel(value string) bool {
	return len(value) > 0 && len(value) <= 200 && !strings.ContainsAny(value, "\x00\r\n\t ") && !strings.HasPrefix(value, "-")
}

func validSnapshotEffort(value string) bool {
	switch value {
	case "low", "medium", "high", "xhigh", "max":
		return true
	default:
		return false
	}
}
