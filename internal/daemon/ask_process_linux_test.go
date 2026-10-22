//go:build linux

package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/claude"
	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

const askProcessParent = "11111111-1111-4111-8111-111111111111"
const askProcessLeaf = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"

// The daemon's own test binary is a deterministic native executable fixture.
// No runtime gate or keystore check in the daemon is replaced by a test hook.
func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "capture-ask-configuration" {
		cwd, err := os.Getwd()
		if err != nil {
			os.Exit(2)
		}
		configuration, err := claude.CaptureResponderConfiguration(os.Getpid(), cwd, os.Environ())
		if err != nil || json.NewEncoder(os.Stdout).Encode(configuration) != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println("2.1.306 (Claude Code)")
		os.Exit(0)
	}
	if len(os.Args) > 1 && os.Args[1] == "-p" {
		os.Exit(askNativeProcess())
	}
	os.Exit(m.Run())
}

func askJournal(session, project string, fork bool) []byte {
	var records []any
	if fork {
		records = append(records, map[string]any{"type": "user", "sessionId": session,
			"message": map[string]string{"content": "PeerTurn local startup check. Reply exactly PEERTURN_PROFILE_OK. Do not use tools."}})
	}
	records = append(records,
		map[string]any{"type": "api-request-shape", "sessionId": session, "shape": map[string]any{
			"system": []string{"daemon fixture persona"}, "tools": []map[string]string{{"name": "Read"}, {"name": "Grep"}, {"name": "Glob"}}}},
		map[string]any{"type": "assistant", "sessionId": session, "uuid": askProcessLeaf, "cwd": project,
			"version": "2.1.306", "requestedModel": "fixture-model", "effort": "high", "perTurnEffort": nil,
			"message": map[string]string{"model": "fixture-model", "stop_reason": "end_turn"}},
		map[string]any{"type": "last-prompt", "sessionId": session, "leafUuid": askProcessLeaf},
	)
	var result []byte
	for _, record := range records {
		line, err := json.Marshal(record)
		if err != nil {
			panic(err)
		}
		result = append(append(result, line...), '\n')
	}
	return result
}

func askNativeProcess() int {
	var child string
	for i, argument := range os.Args {
		if argument == "--session-id" && i+1 < len(os.Args) {
			child = os.Args[i+1]
		}
	}
	cwd, err := os.Getwd()
	if err != nil || child == "" {
		return 2
	}
	input := bufio.NewScanner(os.Stdin)
	if !input.Scan() {
		return 2
	}
	journal := filepath.Join(os.Getenv("HOME"), ".claude/projects/fixture", child+".jsonl")
	if err = os.WriteFile(journal, askJournal(child, cwd, true), 0600); err != nil {
		return 2
	}
	encoder := json.NewEncoder(os.Stdout)
	for _, event := range []any{
		map[string]any{"type": "system", "subtype": "init", "session_id": child, "model": "fixture-model", "permissionMode": "default",
			"tools": []string{"Read", "Grep", "Glob"}, "mcp_servers": []string{}, "plugins": []string{}, "skills": []string{}},
		map[string]any{"type": "result", "subtype": "success", "is_error": false, "session_id": child, "result": "PEERTURN_PROFILE_OK"},
	} {
		if encoder.Encode(event) != nil {
			return 2
		}
	}
	if !input.Scan() {
		return 2
	}
	if encoder.Encode(map[string]any{"type": "assistant", "session_id": child, "message": map[string]any{
		"role": "assistant", "model": "fixture-model", "content": []map[string]string{{"type": "text", "text": "answer"}}}}) != nil {
		return 2
	}
	if encoder.Encode(map[string]any{"type": "result", "subtype": "success", "is_error": false,
		"session_id": child, "result": "daemon fixture answer"}) != nil {
		return 2
	}
	return 0
}

func TestAskDaemonStartsRealSupervisedWorker(t *testing.T) {
	a, b := pairedStores(t)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	backend := &memoryKeyStore{}
	if err := MigrateAuthoritySecrets(t.Context(), a.options.StateDir, backend); err != nil {
		t.Fatal(err)
	}
	options := a.options
	options.KeyStore = backend
	a, err := Open(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := a.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	bindClaude(t, a)
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	for _, key := range []string{"CLAUDE_CONFIG_DIR", "CLAUDE_CODE_OAUTH_TOKEN"} {
		t.Setenv(key, "")
		if err = os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	environment := []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
	for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "ANTHROPIC_MODEL",
		"ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL"} {
		value := ""
		if key == "ANTHROPIC_AUTH_TOKEN" {
			value = "synthetic-daemon-fixture"
		}
		if key == "ANTHROPIC_BASE_URL" {
			value = "https://gateway.example.invalid"
		}
		t.Setenv(key, value)
		environment = append(environment, key+"="+value)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), executable, "capture-ask-configuration")
	command.Dir, command.Env = project, environment
	data, err := command.Output()
	if err != nil {
		t.Fatal("native configuration fixture failed", err)
	}
	var configuration claude.ResponderConfiguration
	if err = json.Unmarshal(data, &configuration); err != nil {
		t.Fatal(err)
	}
	a.state.ClaudeProject, a.claudeBinding.WorkingDirectory = project, project
	a.claudeBinding.ParentConfiguration = &configuration
	directory := filepath.Join(home, ".claude/projects/fixture")
	if err = os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(directory, askProcessParent+".jsonl"), askJournal(askProcessParent, project, false), 0600); err != nil {
		t.Fatal(err)
	}
	packet, err := b.makePacket(peerID(a.state.Chain), b.state.Peers[peerID(a.state.Chain)], protocol.Ask, "shareable fact", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.receive(t.Context(), packet.Packet, 1); err != nil {
		t.Fatal(err)
	}
	items, err := a.db.PendingRoute(t.Context(), envelope.Responder, 1)
	if err != nil || len(items) != 1 {
		t.Fatal(items, err)
	}
	a.opMu.Lock()
	a.mu.Lock()
	err = a.startClaudeAsk(t.Context(), items[0])
	a.mu.Unlock()
	a.opMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { a.askWorkers.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("supervised worker never settled")
	}
	receipt, err := a.db.Receipt(t.Context(), packet.Message.MessageID)
	if err != nil || receipt.Stage != "reply" {
		t.Fatal("no supervised reply", receipt, err, "local:", a.lastError)
	}
	files, err := os.ReadDir(directory)
	if err != nil || len(files) != 1 || files[0].Name() != askProcessParent+".jsonl" {
		t.Fatal("fork remains resumable or parent was removed", files, err)
	}
}
