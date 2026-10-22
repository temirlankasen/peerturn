package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/temirlankasen/peerturn/internal/codec"
)

// CheckResponderProfile checks the pinned binary and every qualified managed
// policy source before a process receives either a probe or peer-derived input.
// macOS remains closed until a native managed-preferences probe is qualified.
func CheckResponderProfile(ctx context.Context, options ResponderOptions) error {
	if err := validateResponderOptions(options); err != nil {
		return err
	}
	if options.ParentConfiguration != nil {
		if err := VerifyResponderConfiguration(options); err != nil {
			return err
		}
	}
	if err := checkResponderManagedPolicy(options); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	command := responderCommand(ctx, options.Executable)
	command.Args = append(command.Args, "--version")
	command.Dir, command.Env, command.Stderr = options.WorkingDirectory, responderEnvironment(options), io.Discard
	var output bytes.Buffer
	command.Stdout = &versionOutput{buffer: &output}
	if command.Run() != nil || strings.TrimSpace(output.String()) != "2.1.306 (Claude Code)" {
		return &ResponderError{Code: ResponderUnavailable}
	}
	return nil
}

func checkResponderManagedPolicy(options ResponderOptions) error {
	base := "/etc/claude-code"
	if runtime.GOOS == "darwin" {
		base = "/Library/Application Support/ClaudeCode"
	} else if runtime.GOOS != "linux" {
		return &ResponderError{Code: ManagedPolicyUnverified}
	}
	for _, path := range []string{filepath.Join(base, "managed-settings.json"),
		filepath.Join(base, "managed-mcp.json"), filepath.Join(responderConfigDirectory(options), "remote-settings.json")} {
		if err := checkManagedDocument(path); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(filepath.Join(base, "managed-settings.d"))
	if err != nil && !os.IsNotExist(err) {
		return &ResponderError{Code: ManagedPolicyUnverified}
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			if policyErr := checkManagedDocument(filepath.Join(base, "managed-settings.d", entry.Name())); policyErr != nil {
				return policyErr
			}
		}
	}
	// Reading a few plist paths cannot prove CFPreferences/MDM is absent.
	// WSL can inherit Windows registry/file policy, outside this Linux probe.
	if runtime.GOOS == "darwin" {
		return &ResponderError{Code: ManagedPolicyUnverified}
	}
	osrelease, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil || strings.Contains(strings.ToLower(string(osrelease)), "microsoft") || !explicitGatewayAuth(options.RuntimeEnv) {
		return &ResponderError{Code: ManagedPolicyUnverified}
	}
	return nil
}

func checkManagedDocument(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return &ResponderError{Code: ManagedPolicyUnverified}
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return &ResponderError{Code: ManagedPolicyUnverified}
	}
	data, err := root.ReadFile(filepath.Base(path))
	closeErr := root.Close()
	if err != nil || closeErr != nil || codec.Validate(data, 1<<20) != nil {
		return &ResponderError{Code: ManagedPolicyUnverified}
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return &ResponderError{Code: ManagedPolicyUnverified}
	}
	if len(fields) != 0 {
		return &ResponderError{Code: ManagedPolicyPresent}
	}
	return nil
}

func openResponderFile(path string) (*os.File, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	file, err := root.Open(filepath.Base(path))
	if closeErr := root.Close(); closeErr != nil {
		if file != nil {
			return nil, errors.Join(err, closeErr, file.Close())
		}
		return nil, errors.Join(err, closeErr)
	}
	return file, err
}

func explicitGatewayAuth(environment []string) bool {
	var endpoint string
	var credential bool
	for _, entry := range environment {
		key, value, _ := strings.Cut(entry, "=")
		switch key {
		case "ANTHROPIC_BASE_URL":
			endpoint = value
		case "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN":
			credential = credential || value != ""
		case "CLAUDE_CODE_OAUTH_TOKEN":
			return false
		}
	}
	u, err := url.Parse(endpoint)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.Hostname() != "api.anthropic.com" && u.User == nil && credential
}

func responderConfigDirectory(options ResponderOptions) string {
	for _, entry := range options.RuntimeEnv {
		key, value, _ := strings.Cut(entry, "=")
		if key == "CLAUDE_CONFIG_DIR" {
			return value
		}
	}
	return filepath.Join(options.HomeDirectory, ".claude")
}

type versionOutput struct{ buffer *bytes.Buffer }

func (v *versionOutput) Write(data []byte) (int, error) {
	if len(data) > 256-v.buffer.Len() {
		return 0, &ResponderError{Code: ResponderUnavailable}
	}
	return v.buffer.Write(data)
}
