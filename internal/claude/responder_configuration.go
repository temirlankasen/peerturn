package claude

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
)

const (
	// ConfigurationUnavailable rejects parent facts that cannot be qualified.
	ConfigurationUnavailable ResponderFailure = "configuration_unavailable"
	// ProviderMismatch rejects binary, provider environment or settings drift.
	ProviderMismatch ResponderFailure = "provider_mismatch"
)

// ResponderConfiguration contains private helper evidence, never public status.
// Authentication values remain in the existing process environments; only their
// digest crosses the private daemon pipe. It is not an authorization credential.
type ResponderConfiguration struct {
	Executable       string `json:"executable"`
	HomeDirectory    string `json:"home_directory"`
	WorkingDirectory string `json:"working_directory"`
	SettingsSources  string `json:"settings_sources"`
	BinaryHash       string `json:"binary_hash"`
	SettingsHash     string `json:"settings_hash"`
	EnvironmentHash  string `json:"environment_hash"`
}

// ConfigureResponder selects the authenticated parent's actual binary rather
// than resolving the daemon's PATH. The existing daemon environment must match;
// this never imports a new credential from the parent or a settings file.
func ConfigureResponder(options ResponderOptions, parent *ResponderConfiguration) (ResponderOptions, error) {
	if parent == nil {
		return ResponderOptions{}, &ResponderError{Code: ConfigurationUnavailable}
	}
	options.Executable = parent.Executable
	copyOfParent := *parent
	options.ParentConfiguration = &copyOfParent
	if err := VerifyResponderConfiguration(options); err != nil {
		return ResponderOptions{}, err
	}
	return options, nil
}

// VerifyResponderConfiguration must run again immediately before peer input.
// Files changing after that point are covered only by subsequent checks; this
// cheap qualification does not claim an immutable filesystem or runtime sandbox.
func VerifyResponderConfiguration(options ResponderOptions) error {
	parent := options.ParentConfiguration
	if parent == nil {
		return &ResponderError{Code: ConfigurationUnavailable}
	}
	if options.Executable != parent.Executable || options.HomeDirectory != parent.HomeDirectory ||
		options.WorkingDirectory != parent.WorkingDirectory || !validConfigurationHashes(*parent) {
		return &ResponderError{Code: ProviderMismatch}
	}
	current, err := responderConfiguration(options.Executable, options.WorkingDirectory, options.HomeDirectory,
		parent.SettingsSources, options.RuntimeEnv)
	if err != nil {
		return err
	}
	if current != *parent {
		return &ResponderError{Code: ProviderMismatch}
	}
	return nil
}

func validConfigurationHashes(configuration ResponderConfiguration) bool {
	for _, value := range []string{configuration.BinaryHash, configuration.SettingsHash, configuration.EnvironmentHash} {
		decoded, err := hex.DecodeString(value)
		if err != nil || len(decoded) != sha256.Size {
			return false
		}
	}
	return true
}

func responderConfiguration(executable, project, home, sources string, environment []string) (ResponderConfiguration, error) {
	var result ResponderConfiguration
	for _, path := range []string{executable, project, home} {
		if !validResponderPath(path) {
			return result, &ResponderError{Code: ConfigurationUnavailable}
		}
	}
	provider, err := providerEnvironment(environment)
	if err != nil || !explicitGatewayAuth(provider) {
		return result, &ResponderError{Code: ConfigurationUnavailable}
	}
	binary, err := configurationBinaryHash(executable)
	if err != nil {
		return result, err
	}
	options := ResponderOptions{HomeDirectory: home, WorkingDirectory: project, RuntimeEnv: provider}
	settings, err := configurationSettingsHash(options, sources)
	if err != nil {
		return result, err
	}
	data, err := json.Marshal(provider)
	if err != nil {
		return result, &ResponderError{Code: ConfigurationUnavailable}
	}
	envHash := sha256.Sum256(data)
	return ResponderConfiguration{Executable: executable, WorkingDirectory: project, HomeDirectory: home,
		SettingsSources: sources, BinaryHash: binary, SettingsHash: settings, EnvironmentHash: hex.EncodeToString(envHash[:])}, nil
}

func providerEnvironment(environment []string) ([]string, error) {
	var provider []string
	seen := make(map[string]bool)
	for _, entry := range environment {
		key, value, found := strings.Cut(entry, "=")
		if !found || strings.ContainsAny(entry, "\x00\r\n") {
			return nil, &ResponderError{Code: ConfigurationUnavailable}
		}
		if responderRuntimeVariable(key) {
			if seen[key] || len(value) > 16<<10 || (key == "CLAUDE_CONFIG_DIR" && !validResponderPath(value)) {
				return nil, &ResponderError{Code: ConfigurationUnavailable}
			}
			seen[key] = true
			provider = append(provider, entry)
			continue
		}
		if unsupportedProviderVariable(key) {
			return nil, &ResponderError{Code: ConfigurationUnavailable}
		}
	}
	slices.Sort(provider)
	return provider, nil
}

func unsupportedProviderVariable(key string) bool {
	switch key {
	case "HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY", "NO_PROXY", "https_proxy", "http_proxy", "all_proxy", "no_proxy",
		"NODE_EXTRA_CA_CERTS", "NODE_TLS_REJECT_UNAUTHORIZED", "SSL_CERT_FILE", "SSL_CERT_DIR",
		"NODE_OPTIONS", "BUN_OPTIONS", "LD_PRELOAD", "LD_LIBRARY_PATH", "DYLD_INSERT_LIBRARIES", "DYLD_LIBRARY_PATH":
		return true
	case "CLAUDE_CODE_MESSAGING_SOCKET", "CLAUDE_CODE_MESSAGING_TOKEN", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_MODS_DIR":
		return false
	}
	return strings.HasPrefix(key, "ANTHROPIC_") || strings.HasPrefix(key, "CLAUDE_") ||
		strings.HasPrefix(key, "AWS_") || strings.HasPrefix(key, "GOOGLE_") || strings.HasPrefix(key, "AZURE_")
}
