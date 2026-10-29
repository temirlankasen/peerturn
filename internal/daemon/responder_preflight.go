package daemon

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"time"

	"github.com/temirlankasen/peerturn/internal/claude"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/runtimepolicy"
)

// ResponderPreflight is local startup evidence, not a persisted authorization.
// A storage mode or successful native fork alone cannot qualify admission.
type ResponderPreflight struct {
	Platform    string    `json:"platform"`
	StorageMode string    `json:"storage_mode"`
	CheckedAt   int64     `json:"checked_at"`
	Available   bool      `json:"available"`
	Failures    []string  `json:"failures"`
	Policy      AskPolicy `json:"policy"`
	Active      int       `json:"active"`
}

func (d *Daemon) checkResponderStartup() ResponderPreflight {
	result := ResponderPreflight{Platform: runtime.GOOS, StorageMode: "file_permissions", CheckedAt: unixNow(), Policy: defaultAskPolicy(), Active: len(d.askActive)}
	if d.state.AskBudget != nil {
		result.Policy = d.state.AskBudget.Policy
	}
	if d.askCleanupFailed {
		result.Failures = append(result.Failures, "fork_cleanup_failed")
	}
	if !d.authoritySecretsIsolated {
		result.Failures = append(result.Failures, "wrapping_key_not_isolated", "authority_file_key")
	} else {
		result.StorageMode = "os_keystore"
	}
	if d.state.Codex != nil {
		result.Failures = append(result.Failures, "codex_responder_unqualified")
	}
	if d.state.NativeSessionID == "" || !d.claudeBinding.Ready(time.Now()) {
		result.Failures = append(result.Failures, "native_binding_unavailable")
	} else if _, qualified := runtimepolicy.AskProfile(runtimepolicy.Claude, d.claudeBinding.RuntimeVersion); !qualified {
		result.Failures = append(result.Failures, "runtime_version_unqualified")
	}
	if d.state.ClaudeProject != "" {
		home, err := os.UserHomeDir()
		if err != nil {
			result.Failures = append(result.Failures, "project_root_unqualified")
		} else if _, err = claude.QualifyResponderProjectRoot(d.state.ClaudeProject, home); err != nil {
			result.Failures = append(result.Failures, askFailureReason(err))
		}
	}
	if _, _, err := d.responderDirectories(); err != nil {
		result.Failures = append(result.Failures, "protected_directories_unavailable")
	}
	if reason := responderPlatformFailure(runtime.GOOS); reason != "" {
		result.Failures = append(result.Failures, reason)
	}
	return result
}

func responderPlatformFailure(platform string) string {
	switch platform {
	case "linux":
		return ""
	case "darwin":
		return "macos_unqualified"
	default:
		return "platform_unqualified"
	}
}

func (d *Daemon) responderStatus(ctx context.Context, status *Status) {
	preflight := d.checkResponderStartup()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	isolated := d.authoritySecretsIsolated
	if len(preflight.Failures) != 0 && isolated {
		d.mu.Unlock()
		isolated = d.checkAuthorityKey(ctx) == nil
		d.mu.Lock()
		if !isolated {
			preflight.Failures = append(preflight.Failures, "keystore_unavailable")
		}
	}
	if len(preflight.Failures) == 0 {
		options, err := d.captureAskOptions()
		if err == nil {
			native, instance := d.state.NativeSessionID, d.claudeBinding.Instance
			// Inspection must not hold mu across a keyring service, executable
			// probe or a large native journal. Rebinding invalidates the result.
			d.mu.Unlock()
			_, _, err = qualifyAskOptions(ctx, options, native)
			d.mu.Lock()
			if !d.claudeBinding.Ready(time.Now()) || d.state.NativeSessionID != native || d.claudeBinding.Instance != instance {
				err = localipc.NewError("responder_unavailable", "native_binding_unavailable")
			}
		}
		if err != nil {
			preflight.Failures = append(preflight.Failures, askFailureReason(err))
			if askFailureReason(err) == "keystore_unavailable" {
				isolated = false
			}
		}
	}
	preflight.Available = len(preflight.Failures) == 0
	d.responderPreflight = preflight
	status.Responder = preflight
	status.Responder.Failures = slices.Clone(preflight.Failures)
	status.Capabilities.AuthoritySecretsIsolated = isolated
	status.Capabilities.ForkResponder = preflight.Available
	status.ResponderReason = ""
	if len(preflight.Failures) > 0 {
		status.ResponderReason = preflight.Failures[0]
	}
	if preflight.Available {
		status.SupportedScopes = append(status.SupportedScopes, "ask")
		status.Capabilities.ReceiptStages = append(status.Capabilities.ReceiptStages, "responder_started", "model_input", "turn_completed", "reply")
	}
}

// responderDirectories uses the daemon's effective selection plus configured
// and default roots. It never infers state from the fork's cwd or Linux defaults.
func (d *Daemon) responderDirectories() (claude.ResponderAuthorityPaths, []string, error) {
	var paths claude.ResponderAuthorityPaths
	state, err := canonicalResponderDirectory(d.options.StateDir)
	if err != nil {
		return paths, nil, err
	}
	config, err := DefaultConfigPath()
	if err != nil {
		return paths, nil, err
	}
	runtimeDir := d.options.RuntimeDir
	if runtimeDir == "" {
		runtimeDir, err = DefaultRuntimeDir()
		if err != nil {
			return paths, nil, err
		}
	}
	runtimeDir, err = canonicalResponderDirectory(runtimeDir)
	if err != nil {
		return paths, nil, err
	}
	paths = claude.ResponderAuthorityPaths{StateDirectory: state, ConfigFile: config, RuntimeDirectory: runtimeDir}
	defaultState, err := DefaultStateDir()
	if err != nil {
		return paths, nil, err
	}
	configuredState, err := ConfiguredStateDir()
	if err != nil {
		return paths, nil, err
	}
	defaultRuntime, err := DefaultRuntimeDir()
	if err != nil {
		return paths, nil, err
	}
	roots := []string{state, filepath.Dir(config), runtimeDir, defaultState, configuredState, defaultRuntime}
	for i, root := range roots {
		roots[i], err = canonicalResponderDirectory(root)
		if err != nil {
			return paths, nil, err
		}
	}
	slices.Sort(roots)
	return paths, slices.Compact(roots), nil
}

func canonicalResponderDirectory(path string) (string, error) {
	if !configPath(path) || path == string(filepath.Separator) {
		return "", localipc.NewError("responder_unavailable", "protected_path_policy_failed")
	}
	// canonicalDefault resolves platform ancestors; resolve an existing leaf too
	// so /var aliases and explicitly configured symlinks cannot omit the target.
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved, nil
	}
	return canonicalDefault(path)
}
