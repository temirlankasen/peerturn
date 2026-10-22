package claude

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func validateResponderOptions(options ResponderOptions) error {
	for _, path := range []string{
		options.AuthorityPaths.StateDirectory, options.AuthorityPaths.ConfigFile, options.AuthorityPaths.RuntimeDirectory,
	} {
		if !validResponderPath(path) || filepath.Clean(path) != path {
			return &ResponderError{Code: InvalidResponderRequest}
		}
	}
	for _, path := range append([]string{options.Executable, options.WorkingDirectory, options.HomeDirectory}, options.ProtectedPaths...) {
		// Metacharacters would change a generated gitignore-style deny rule.
		if !validResponderPath(path) {
			return &ResponderError{Code: InvalidResponderRequest}
		}
	}
	if _, err := QualifyResponderProjectRoot(options.WorkingDirectory, options.HomeDirectory); err != nil {
		return err
	}
	if options.Timeout < 0 || options.Timeout > 10*time.Minute || options.MaxConcurrent < 0 || options.MaxConcurrent > 16 ||
		options.MaxOutputBytes < 0 || options.MaxOutputBytes > 16<<10 {
		return &ResponderError{Code: InvalidResponderRequest}
	}
	for _, entry := range options.RuntimeEnv {
		key, value, found := strings.Cut(entry, "=")
		if !found || strings.ContainsAny(entry, "\x00\r\n") || !responderRuntimeVariable(key) {
			return &ResponderError{Code: InvalidResponderRequest}
		}
		if key == "CLAUDE_CONFIG_DIR" && !validResponderPath(value) {
			return &ResponderError{Code: InvalidResponderRequest}
		}
	}
	return nil
}

func validResponderPath(path string) bool {
	return filepath.IsAbs(path) && !strings.ContainsAny(path, "\x00\r\n()*?[]{}!\\")
}

func validNativeSessionID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, r := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if r != '-' {
				return false
			}
		} else if r < '0' || r > '9' {
			if r < 'a' || r > 'f' {
				return false
			}
		}
	}
	return true
}

func responderArgs(parent, settings string) []string {
	return []string{
		"-p", "--resume", parent, "--fork-session", "--permission-mode", "default", "--restricted",
		"--permission-prompts", "none", "--tools", "Read,Grep,Glob",
		"--disallowedTools", "Bash", "Edit", "Write", "NotebookEdit", "WebFetch", "WebSearch", "Task", "Agent", "mcp__*",
		"AskUserQuestion", "ListAgents", "ReportFindings", "ScheduleWakeup", "Skill", "DeferredToolPlaceholder", "ToolSearch",
		"--settings", settings, "--setting-sources", "", "--disable-slash-commands",
		"--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--no-chrome",
		"--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--system-prompt-snapshot", "on", "--max-budget-usd", "1", "--max-turns", "9",
	}
}

var responderBuiltinPlugins = []string{
	"cc-plugin-agents-md@builtin", "cc-plugin-telemetry@builtin", "cc-plugin-plugin-authoring@builtin",
}

func responderSettings(options ResponderOptions) (string, string, error) {
	directory, err := os.MkdirTemp("", "peerturn-responder-")
	if err != nil {
		return "", "", err
	}
	paths := []string{
		filepath.Join(options.HomeDirectory, ".claude.json"), options.AuthorityPaths.StateDirectory,
		filepath.Dir(options.AuthorityPaths.ConfigFile), options.AuthorityPaths.RuntimeDirectory, directory,
	}
	for _, credential := range responderCredentialPaths {
		paths = append(paths, filepath.Join(options.HomeDirectory, credential))
	}
	paths = append(paths, options.ProtectedPaths...)
	for _, entry := range options.RuntimeEnv {
		key, value, _ := strings.Cut(entry, "=")
		if key == "CLAUDE_CONFIG_DIR" {
			paths = append(paths, value)
		}
	}
	deny := make([]string, 0, 4*len(paths)+3)
	seen := make(map[string]bool)
	for _, path := range paths {
		resolved, resolveErr := responderResolvedPath(path)
		if resolveErr != nil || !validResponderPath(resolved) {
			return "", "", errors.Join(resolveErr, &ResponderError{Code: InvalidResponderRequest}, os.RemoveAll(directory))
		}
		for _, target := range []string{filepath.Clean(path), resolved} {
			if !seen[target] {
				absolute := "/" + filepath.ToSlash(target)
				deny = append(deny, "Read("+absolute+")", "Read("+absolute+"/**)")
				seen[target] = true
			}
		}
	}
	root := "/" + filepath.ToSlash(filepath.Clean(options.WorkingDirectory))
	deny = append(deny, responderProjectDenies(filepath.Clean(options.WorkingDirectory))...)
	deny = append(deny, "Read("+root+"/.env*)")
	deny = append(deny, "Read(//proc/**)", "Read(//dev/fd/**)")
	settings := struct {
		Permissions struct {
			Allow []string `json:"allow"`
			Deny  []string `json:"deny"`
		} `json:"permissions"`
		DisableAllHooks bool            `json:"disableAllHooks"`
		EnabledPlugins  map[string]bool `json:"enabledPlugins"`
	}{DisableAllHooks: true, EnabledPlugins: map[string]bool{}}
	settings.Permissions.Allow = []string{"Read(" + root + "/**)"}
	settings.Permissions.Deny = deny
	for _, name := range responderBuiltinPlugins {
		settings.EnabledPlugins[name] = false
	}
	data, err := json.Marshal(settings)
	if err != nil {
		return "", "", errors.Join(err, os.RemoveAll(directory))
	}
	path := filepath.Join(directory, "settings.json")
	if err = os.WriteFile(path, data, 0600); err != nil {
		return "", "", errors.Join(err, os.RemoveAll(directory))
	}
	return directory, path, nil
}

func responderResolvedPath(path string) (string, error) {
	// Protect the real existing ancestor too when a not-yet-created config or
	// credential path is selected through a symlink. Qualification still owns
	// hardlinks and changes to the filesystem after this resolution.
	var suffix []string
	for {
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil {
			return filepath.Join(append([]string{resolved}, suffix...)...), nil
		}
		parent := filepath.Dir(path)
		if !os.IsNotExist(err) || parent == path {
			return "", err
		}
		suffix = append([]string{filepath.Base(path)}, suffix...)
		path = parent
	}
}

func responderEnvironment(options ResponderOptions) []string {
	// Authentication is an explicit runtime dependency; daemon/session control
	// variables, arbitrary PATH, NODE_OPTIONS and inherited secrets are not copied.
	return append([]string{
		"HOME=" + options.HomeDirectory, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LANG=C.UTF-8",
		"ENABLE_TOOL_SEARCH=false",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1",
		"DISABLE_TELEMETRY=1", "DISABLE_ERROR_REPORTING=1",
	}, options.RuntimeEnv...)
}

func responderRuntimeVariable(key string) bool {
	switch key {
	case "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "ANTHROPIC_MODEL",
		"ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL",
		"CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CONFIG_DIR":
		return true
	default:
		return false
	}
}
