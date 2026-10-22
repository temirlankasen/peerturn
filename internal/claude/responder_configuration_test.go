package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func configurationFixture(t *testing.T) (ResponderOptions, ResponderConfiguration) {
	t.Helper()
	r := responderFixture(t)
	r.options.RuntimeEnv = []string{"ANTHROPIC_BASE_URL=https://gateway.example.invalid", "ANTHROPIC_AUTH_TOKEN=fixture-value"}
	directory := t.TempDir()
	executable := filepath.Join(directory, "claude")
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	if err = root.WriteFile("claude", []byte("fixture-native-binary"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = root.Chmod("claude", 0700); err != nil {
		t.Fatal(err)
	}
	r.options.Executable = executable
	evidence, err := responderConfiguration(executable, r.options.WorkingDirectory, r.options.HomeDirectory, "user,project,local", r.options.RuntimeEnv)
	if err != nil {
		t.Fatal(err)
	}
	return r.options, evidence
}

func TestResponderConfigurationPinsParentRuntimeAndRejectsDrift(t *testing.T) {
	for _, change := range []string{"matching", "binary", "provider", "source", "home", "project"} {
		t.Run(change, func(t *testing.T) {
			options, evidence := configurationFixture(t)
			switch change {
			case "binary":
				if err := os.WriteFile(options.Executable, []byte("changed-binary"), 0600); err != nil {
					t.Fatal(err)
				}
			case "provider":
				options.RuntimeEnv[0] = "ANTHROPIC_BASE_URL=https://other.example.invalid"
			case "source":
				path := filepath.Join(options.WorkingDirectory, ".claude")
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(path, "settings.json"), []byte(`{"model":"changed"}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "home":
				options.HomeDirectory = t.TempDir()
			case "project":
				options.WorkingDirectory = t.TempDir()
			}
			configured, err := ConfigureResponder(options, &evidence)
			if change == "matching" {
				if err != nil || configured.Executable != evidence.Executable || configured.ParentConfiguration == nil {
					t.Fatal("matching parent configuration rejected", err)
				}
				return
			}
			if err == nil || configured.ParentConfiguration != nil {
				t.Fatal("changed parent configuration accepted")
			}
		})
	}
}

func TestResponderConfigurationRejectsUnsupportedProviderSources(t *testing.T) {
	options, _ := configurationFixture(t)
	for _, contents := range []string{
		`{"apiKeyHelper":"private-command"}`, `{"env":{"ANTHROPIC_BASE_URL":"https://other.example.invalid"}}`,
		`{"unknownFutureProviderSetting":true}`, `{"env":{"CLAUDE_CODE_USE_BEDROCK":"1"}}`,
	} {
		dir := filepath.Join(options.WorkingDirectory, ".claude")
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := responderConfiguration(options.Executable, options.WorkingDirectory, options.HomeDirectory, "user,project,local", options.RuntimeEnv); err == nil {
			t.Fatal("unsupported provider source accepted")
		}
	}
}

func TestResponderConfigurationEvidenceHasNoEnvironmentValues(t *testing.T) {
	options, evidence := configurationFixture(t)
	data, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"fixture-value", "gateway.example.invalid", "ANTHROPIC_AUTH_TOKEN"} {
		if strings.Contains(string(data), value) {
			t.Fatal("provider details entered runtime evidence")
		}
	}
	options.Executable = filepath.Join(t.TempDir(), "unrelated-daemon-path")
	configured, err := ConfigureResponder(options, &evidence)
	if err != nil || configured.Executable != evidence.Executable {
		t.Fatal("daemon PATH replaced actual parent binary", err)
	}
	if _, err := ConfigureResponder(options, nil); err == nil {
		t.Fatal("missing parent evidence accepted")
	}
}

func TestResponderConfigurationRejectsUnsupportedProviderEnvironment(t *testing.T) {
	options, _ := configurationFixture(t)
	for _, entry := range []string{"CLAUDE_CODE_USE_BEDROCK=1", "ANTHROPIC_CUSTOM_HEADERS=private", "HTTPS_PROXY=https://proxy.example.invalid"} {
		if _, err := responderConfiguration(options.Executable, options.WorkingDirectory, options.HomeDirectory,
			"user,project,local", append(options.RuntimeEnv, entry)); err == nil {
			t.Fatal("unsupported provider environment accepted")
		}
	}
}

func TestResponderConfigurationQualifiesInstalledPluginSettings(t *testing.T) {
	for _, contents := range []string{
		`{"enabledPlugins":{"peerturn@local":true},"pluginConfigs":{"peerturn@local":{"binary":"/private/peerturn"}},"skipDangerousModePermissionPrompt":true,"env":{"DISABLE_AUTOUPDATER":"1"}}`,
		`{"enabledPlugins":{"peerturn@local":true},"env":{"DISABLE_AUTOUPDATER":"true"}}`,
	} {
		if !qualifiedProviderSettings([]byte(contents)) {
			t.Fatal("installed native plugin and updater settings rejected")
		}
	}
	for _, contents := range []string{
		`{"env":{"DISABLE_AUTOUPDATER":"1","ANTHROPIC_BASE_URL":"https://other.example.invalid"}}`,
		`{"env":{"DISABLE_AUTOUPDATER":"1","NODE_OPTIONS":"--require=private"}}`,
		`{"env":{"DISABLE_AUTOUPDATER":"arbitrary-value"}}`,
	} {
		if qualifiedProviderSettings([]byte(contents)) {
			t.Fatal("neutral setting opened unsupported provider or command environment")
		}
	}
}
