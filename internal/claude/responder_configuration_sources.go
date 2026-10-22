package claude

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"github.com/temirlankasen/peerturn/internal/codec"
)

func configurationSettingsHash(options ResponderOptions, sources string) (string, error) {
	failure := &ResponderError{Code: ConfigurationUnavailable}
	if sources != "" && sources != "user,project,local" {
		return "", failure
	}
	var paths []string
	if sources != "" {
		paths = []string{filepath.Join(responderConfigDirectory(options), "settings.json"),
			filepath.Join(options.WorkingDirectory, ".claude", "settings.json"),
			filepath.Join(options.WorkingDirectory, ".claude", "settings.local.json")}
		// A nested launch can select a project above cwd. Qualify every possible
		// ancestor as absent or empty instead of guessing the native root lookup.
		for directory := filepath.Dir(options.WorkingDirectory); ; directory = filepath.Dir(directory) {
			for _, name := range []string{"settings.json", "settings.local.json"} {
				path := filepath.Join(directory, ".claude", name)
				if path != paths[0] {
					paths = append(paths, path)
				}
			}
			if directory == filepath.Dir(directory) {
				break
			}
		}
	}
	type source struct {
		Path, Digest string
		Present      bool
	}
	records := make([]source, 0, len(paths))
	for index, path := range paths {
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			records = append(records, source{Path: path})
			continue
		}
		if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
			return "", failure
		}
		root, err := os.OpenRoot(filepath.Dir(path))
		if err != nil {
			return "", failure
		}
		file, openErr := root.Open(filepath.Base(path))
		rootCloseErr := root.Close()
		if openErr != nil {
			return "", failure
		}
		data, readErr := io.ReadAll(io.LimitReader(file, (1<<20)+1))
		closeErr := file.Close()
		if readErr != nil || rootCloseErr != nil || closeErr != nil || !qualifiedProviderSettings(data) {
			return "", failure
		}
		if index >= 3 {
			var ancestor map[string]json.RawMessage
			if json.Unmarshal(data, &ancestor) != nil || len(ancestor) != 0 {
				return "", failure
			}
		}
		digest := sha256.Sum256(data)
		records = append(records, source{Path: path, Digest: hex.EncodeToString(digest[:]), Present: true})
	}
	data, err := json.Marshal(records)
	if err != nil {
		return "", failure
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func qualifiedProviderSettings(data []byte) bool {
	if codec.Validate(data, 1<<20) != nil {
		return false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return false
	}
	for key, value := range fields {
		switch key {
		// These sources are fingerprinted but deliberately not loaded by the
		// restricted fork. Model/effort/system context have separate evidence.
		case "$schema", "permissions", "hooks", "disableAllHooks", "enabledPlugins", "extraKnownMarketplaces",
			"model", "effortLevel", "alwaysThinkingEnabled", "outputStyle", "statusLine", "attribution",
			"includeCoAuthoredBy", "cleanupPeriodDays", "enableAllProjectMcpServers", "enabledMcpjsonServers",
			"disabledMcpjsonServers", "respectGitignore", "language", "autoUpdatesChannel", "feedbackSurveyState",
			"pluginConfigs", "skipDangerousModePermissionPrompt":
		case "env":
			// Settings-derived provider values and command environments are not
			// imported. Only an already matching explicit gateway env qualifies.
			var environment map[string]string
			if json.Unmarshal(value, &environment) != nil {
				return false
			}
			for name, setting := range environment {
				// This native maintenance switch cannot select a provider, model,
				// credential or executable command. No other env key is inferred safe.
				if name != "DISABLE_AUTOUPDATER" || (setting != "1" && setting != "true") {
					return false
				}
			}
		default:
			// This includes apiKeyHelper, OAuth/account overrides and future
			// provider settings whose precedence is not part of this profile.
			return false
		}
	}
	return true
}
