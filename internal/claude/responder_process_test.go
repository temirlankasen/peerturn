package claude

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain turns this test binary into a native-process fixture only for the
// responder's fixed argv; no shell or real native session is used.
func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println("2.1.306 (Claude Code)")
		os.Exit(0)
	}
	if len(os.Args) > 1 && os.Args[1] == "-p" {
		os.Exit(responderProcess())
	}
	os.Exit(m.Run())
}

func responderProcess() int {
	input := bufio.NewScanner(os.Stdin)
	if !input.Scan() {
		return 2
	}
	session := argumentValue("--session-id")
	if !validNativeSessionID(session) {
		return 2
	}
	encoder := json.NewEncoder(os.Stdout)
	home, err := os.OpenRoot(os.Getenv("HOME"))
	if err != nil {
		return 2
	}
	directory := filepath.Join(".claude", "projects", "fixture")
	writeErr := home.MkdirAll(directory, 0700)
	if writeErr == nil {
		writeErr = home.WriteFile(filepath.Join(directory, session+".jsonl"),
			forkFixtureData(session, mustWorkingDirectory()), 0600)
	}
	startupMode, modeErr := home.ReadFile(filepath.Join(".claude", "fixture-process-mode"))
	if home.Close() != nil || writeErr != nil {
		return 2
	}
	if modeErr != nil && !os.IsNotExist(modeErr) {
		return 2
	}
	if string(startupMode) == "pre-init-exit" {
		return 1
	}
	initSession := session
	if string(startupMode) == "wrong-child-init" {
		initSession = "cccccccc-dddd-4eee-8fff-aaaaaaaaaaaa"
	}
	if encoder.Encode(map[string]any{"type": "system", "subtype": "init", "session_id": initSession,
		"model": "fixture-model", "permissionMode": "default", "tools": []string{"Read", "Grep", "Glob"},
		"mcp_servers": []string{}, "plugins": []string{}, "skills": []string{}}) != nil {
		return 2
	}
	if encoder.Encode(map[string]any{"type": "result", "subtype": "success", "is_error": false,
		"session_id": session, "result": "PEERTURN_PROFILE_OK"}) != nil || !input.Scan() {
		return 2
	}
	var request struct{ Message struct{ Content string } }
	if json.Unmarshal(input.Bytes(), &request) != nil {
		return 2
	}
	mode := request.Message.Content
	if encoder.Encode(map[string]any{"type": "assistant", "session_id": session, "message": map[string]any{
		"role": "assistant", "model": "fixture-model",
		"content": []map[string]string{{"type": "text", "text": "native fixture answer"}}}}) != nil {
		return 2
	}
	if mode == "sleep" {
		// The supervisor owns cancellation; the fixture waits for input/EOF
		// rather than depending on how quickly the host schedules this child.
		input.Scan()
		return 1
	}
	if mode == "malformed" {
		fmt.Print("tool or debug output before result")
		return 0
	}
	if mode == "partial" {
		fmt.Print(`{"type":"result","result":"private partial`)
		return 0
	}
	if mode == "invalid-utf8" {
		_, err = os.Stdout.Write([]byte{0xff})
		if err != nil {
			return 2
		}
		return 0
	}
	if mode == "empty" {
		return 0
	}
	if mode == "oversized" {
		fmt.Print(strings.Repeat("x", (1<<20)+1))
		return 0
	}
	if mode == "same-session" {
		session = responderParent
	}
	result := map[string]any{
		"type": "result", "subtype": "success", "is_error": false,
		"session_id": session, "result": "Useful final answer",
	}
	switch mode {
	case "wrong-type":
		result["type"] = "assistant"
	case "error-result":
		result["is_error"] = true
	case "capture":
		path := argumentValue("--settings")
		settings, readErr := os.ReadFile(filepath.Clean(path))
		info, statErr := os.Stat(path)
		if readErr != nil || statErr != nil {
			return 2
		}
		capture, marshalErr := json.Marshal(map[string]any{
			"args": os.Args[1:], "settings": string(settings), "mode": uint32(info.Mode().Perm()),
			"env": os.Environ(),
		})
		if marshalErr != nil {
			return 2
		}
		captureHome, openErr := os.OpenRoot(os.Getenv("HOME"))
		if openErr != nil {
			return 2
		}
		captureErr := captureHome.WriteFile("responder-capture.json", capture, 0600)
		captureCloseErr := captureHome.Close()
		if captureErr != nil || captureCloseErr != nil {
			return 2
		}
		result["result"] = "captured"
	case "redact":
		result["result"] = "core-fixture-only and ghp_" + strings.Repeat("z", 36)
	case "redaction-expansion":
		result["result"] = strings.Repeat("x", 256)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		return 2
	}
	if mode == "duplicate" {
		fmt.Print(`{"type":"result"}`)
	}
	if mode == "nonzero" {
		fmt.Fprint(os.Stderr, "PRIVATE_DIAGNOSTIC")
		return 1
	}
	return 0
}

func mustWorkingDirectory() string {
	directory, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	return directory
}

func argumentValue(name string) string {
	for i, value := range os.Args[1:] {
		if value == name && i+2 < len(os.Args) {
			return os.Args[i+2]
		}
	}
	return ""
}
