package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"

	"github.com/temirlankasen/peerturn/internal/codec"
)

const setupClaudeConfigLimit = 1024 * 1024

func observeSetupClaudeMCP(root string) (bool, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return false, invalidArguments()
	}
	for _, name := range []string{"CLAUDE_CODE_CUSTOM_OAUTH_URL", "USE_LOCAL_OAUTH", "USE_STAGING_OAUTH", "CLAUDE_CODE_MANAGED_SETTINGS_PATH", "CLAUDE_CODE_REMOTE_SETTINGS_PATH", "CLAUDE_CODE_MOCK_REMOTE_SETTINGS", "CLAUDE_CODE_PROJECT_DIR_NAME"} {
		if os.Getenv(name) != "" {
			return false, invalidArguments()
		}
	}
	config := os.Getenv("CLAUDE_CONFIG_DIR")
	global := filepath.Join(home, ".claude.json")
	if config == "" {
		config = filepath.Join(home, ".claude")
	} else {
		global = filepath.Join(config, ".claude.json")
	}
	// The pinned host prefers this legacy config when it is populated. Refuse
	// an empty legacy file rather than guessing its writable-file fallback.
	legacy := filepath.Join(config, ".config.json")
	if info, statErr := lstatSetupClaudePath(legacy); statErr == nil {
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return false, invalidArguments()
		}
		global = legacy
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return false, statErr
	}
	// Enterprise files and managed settings can replace or constrain native
	// scopes. Without a structured effective export, setup leaves them alone.
	managed := "/etc/claude-code"
	if runtime.GOOS == "darwin" {
		managed = "/Library/Application Support/ClaudeCode"
	}
	if runtime.GOOS == "windows" {
		return false, invalidArguments()
	}
	policyPaths := []string{filepath.Join(managed, "managed-mcp.json"), filepath.Join(managed, "managed-settings.json"), filepath.Join(managed, "managed-settings.d"), filepath.Join(config, "managed-settings.json"), filepath.Join(config, "remote-settings.json")}
	if runtime.GOOS == "darwin" {
		current, userErr := user.Current()
		if userErr != nil {
			return false, userErr
		}
		policyPaths = append(policyPaths, "/Library/Managed Preferences/com.anthropic.claudecode.plist", filepath.Join("/Library/Managed Preferences", current.Username, "com.anthropic.claudecode.plist"))
	}
	for _, path := range policyPaths {
		if _, statErr := lstatSetupClaudePath(path); !errors.Is(statErr, fs.ErrNotExist) {
			return false, invalidArguments()
		}
	}
	settings, err := readSetupClaudeConfig(global)
	if err != nil {
		return false, err
	}
	entry, err := setupClaudeMCPEntry(settings)
	if err != nil {
		return false, err
	}
	var entryErr error
	if entry != nil {
		entryErr = checkSetupClaudeMCP(entry, root)
		if entryErr != nil && !errors.Is(entryErr, errSetupMCPUpgrade) {
			return false, entryErr
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return false, err
	}
	cwd, err = filepath.EvalSymlinks(cwd)
	if err != nil {
		return false, err
	}
	var projects map[string]json.RawMessage
	if data, ok := settings["projects"]; ok && codec.Decode(data, setupClaudeConfigLimit, &projects) != nil {
		return false, invalidArguments()
	}
	for path := cwd; ; path = filepath.Dir(path) {
		project, readErr := readSetupClaudeConfig(filepath.Join(path, ".mcp.json"))
		if readErr != nil || setupClaudeHasMCPEntry(project) {
			return false, invalidArguments()
		}
		for name, data := range projects {
			canonical, resolveErr := filepath.EvalSymlinks(name)
			if resolveErr != nil || canonical != path {
				continue
			}
			var local map[string]json.RawMessage
			if codec.Decode(data, setupClaudeConfigLimit, &local) != nil || local == nil || setupClaudeHasMCPEntry(local) {
				return false, invalidArguments()
			}
		}
		if parent := filepath.Dir(path); parent == path {
			break
		}
	}
	return entry != nil, entryErr
}

func lstatSetupClaudePath(path string) (fs.FileInfo, error) {
	directory, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	info, statErr := directory.Lstat(filepath.Base(path))
	if closeErr := directory.Close(); closeErr != nil {
		return nil, closeErr
	}
	return info, statErr
}

func readSetupClaudeConfig(path string) (map[string]json.RawMessage, error) {
	directory, err := os.OpenRoot(filepath.Dir(path))
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return nil, err
	}
	info, statErr := directory.Lstat(filepath.Base(path))
	var data []byte
	readErr := statErr
	if statErr == nil {
		if !info.Mode().IsRegular() {
			readErr = invalidArguments()
		} else {
			data, readErr = readSetupFile(directory, filepath.Base(path), setupClaudeConfigLimit)
		}
	}
	closeErr := directory.Close()
	if errors.Is(readErr, fs.ErrNotExist) && closeErr == nil {
		return map[string]json.RawMessage{}, nil
	}
	if readErr != nil || closeErr != nil {
		return nil, invalidArguments()
	}
	var config map[string]json.RawMessage
	if codec.Decode(data, setupClaudeConfigLimit, &config) != nil || config == nil {
		return nil, invalidArguments()
	}
	return config, nil
}

func setupClaudeMCPEntry(config map[string]json.RawMessage) (json.RawMessage, error) {
	data, ok := config["mcpServers"]
	if !ok {
		return nil, nil
	}
	var entries map[string]json.RawMessage
	if codec.Decode(data, setupClaudeConfigLimit, &entries) != nil || entries == nil {
		return nil, invalidArguments()
	}
	return entries["peerturn"], nil
}

func setupClaudeHasMCPEntry(config map[string]json.RawMessage) bool {
	entry, err := setupClaudeMCPEntry(config)
	return err != nil || entry != nil
}

func checkSetupClaudeMCP(data []byte, root string) error {
	var entry struct {
		Type    string            `json:"type"`
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
	}
	if codec.Decode(data, setupClaudeConfigLimit, &entry) != nil || entry.Type != "stdio" ||
		!slices.Equal(entry.Args, []string{"mcp", "--current"}) || len(entry.Env) != 0 {
		return invalidArguments()
	}
	return checkSetupMCPCommand(entry.Command, root)
}
