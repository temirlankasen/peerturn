package claude

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResponderConfigurationParentArguments(t *testing.T) {
	for _, test := range []struct {
		name    string
		args    []string
		want    string
		allowed bool
	}{
		{"defaults", []string{"/native/claude"}, "user,project,local", true},
		{"explicit-defaults", []string{"claude", "--setting-sources=user,project,local", "--model", "native-model"}, "user,project,local", true},
		{"explicit-empty", []string{"claude", "--setting-sources", "", "--dangerously-skip-permissions"}, "", true},
		{"custom-settings", []string{"claude", "--settings", "/private/settings.json"}, "", false},
		{"partial-sources", []string{"claude", "--setting-sources", "user,local"}, "", false},
		{"duplicate-sources", []string{"claude", "--setting-sources=", "--setting-sources="}, "", false},
		{"provider-option", []string{"claude", "--betas", "provider-beta"}, "", false},
		{"unknown-option", []string{"claude", "--future-provider-switch"}, "", false},
		{"missing-value", []string{"claude", "--setting-sources"}, "", false},
		{"no-binary", nil, "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			sources, err := configurationParentSources(test.args)
			if (err == nil) != test.allowed || (test.allowed && sources != test.want) {
				t.Fatal("unexpected parent source qualification", sources, err)
			}
		})
	}
}

func TestResponderConfigurationSourceSelectionAndRecheck(t *testing.T) {
	options, _ := configurationFixture(t)
	directory := filepath.Join(options.WorkingDirectory, ".claude")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "settings.json")
	if err := os.WriteFile(path, []byte(`{"apiKeyHelper":"never-run"}`), 0600); err != nil {
		t.Fatal(err)
	}
	evidence, err := responderConfiguration(options.Executable, options.WorkingDirectory, options.HomeDirectory, "", options.RuntimeEnv)
	if err != nil {
		t.Fatal("disabled parent sources were accidentally loaded", err)
	}
	configured, err := ConfigureResponder(options, &evidence)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"settings-only"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err = VerifyResponderConfiguration(configured); err != nil {
		t.Fatal("disabled source changed effective provider", err)
	}
	if err = os.WriteFile(path, []byte(`{"hooks":{"PreToolUse":[]},"enabledPlugins":{"example@local":true},"permissions":{"allow":["Bash"]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	evidence, err = responderConfiguration(options.Executable, options.WorkingDirectory, options.HomeDirectory, "user,project,local", options.RuntimeEnv)
	if err != nil {
		t.Fatal("execution-bearing settings cannot be inspected safely", err)
	}
	configured, err = ConfigureResponder(options, &evidence)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte(`{"hooks":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err = VerifyResponderConfiguration(configured); err == nil {
		t.Fatal("changed settings admitted before peer input")
	}
}

func TestResponderConfigurationBoundedArgumentsAndDuplicateEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cmdline")
	for _, data := range []string{"", "unterminated", "claude\x00--model\x00native\x00"} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		arguments, err := configurationProcessArguments(path)
		if data == "claude\x00--model\x00native\x00" {
			if err != nil || len(arguments) != 3 || arguments[2] != "native" {
				t.Fatal("valid proc argv rejected", err)
			}
		} else if err == nil {
			t.Fatal("incomplete proc argv accepted")
		}
	}
	options, _ := configurationFixture(t)
	options.RuntimeEnv = append(options.RuntimeEnv, options.RuntimeEnv[0])
	if _, err := providerEnvironment(options.RuntimeEnv); err == nil {
		t.Fatal("duplicate provider variables accepted")
	}
}
