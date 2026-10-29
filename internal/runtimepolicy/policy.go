// Package runtimepolicy owns runtime admission and qualification independently of
// PeerTurn's release version. Qualification never supplies session authority.
package runtimepolicy

import (
	"errors"
	"runtime"
	"strings"
)

// Runtime and qualification names are stable diagnostic values.
const (
	Claude             = "claude"
	Codex              = "codex"
	Verified           = "verified"
	NewerUnverified    = "newer_unverified"
	BelowMinimum       = "below_minimum"
	Unknown            = "unknown"
	Revision           = "2026-10-25.2"
	AdapterRevision    = "2026-10-25.2"
	PreviewProfile     = "native-preview/v1"
	UnverifiedPlatform = "unverified_platform"
)

// Evidence records limited qualification, not universal semantic guarantees.
type Evidence struct {
	Version         string `json:"version"`
	OS              string `json:"os"`
	Arch            string `json:"arch"`
	AdapterRevision string `json:"adapter_revision"`
	Profile         string `json:"profile"`
	Reference       string `json:"reference"`
}

type policy struct {
	minimum     string
	verified    []Evidence
	askProfiles map[string]string
}

func forRuntime(runtime string) policy {
	switch runtime {
	case Claude:
		baseline := "2.1.306"
		return policy{minimum: baseline, verified: packagedVerified[runtime],
			askProfiles: map[string]string{baseline: "peerturn/claude/" + baseline + "/m6c/read-only/v4"}}
	case Codex:
		baseline := "0.163.0"
		return policy{minimum: baseline, verified: packagedVerified[runtime]}
	default:
		return policy{}
	}
}

// Decision distinguishes version admission from current contract readiness.
type Decision struct {
	Runtime         string     `json:"runtime"`
	Version         string     `json:"version"`
	Minimum         string     `json:"minimum"`
	Status          string     `json:"verification_status"`
	OS              string     `json:"os"`
	Arch            string     `json:"arch"`
	AdapterRevision string     `json:"adapter_revision"`
	Profile         string     `json:"profile"`
	Reason          string     `json:"reason,omitempty"`
	PolicyRevision  string     `json:"policy_revision"`
	Evidence        []Evidence `json:"evidence,omitempty"`
}

// Allowed has no upper bound; newer unverified versions retain full functionality.
func (d Decision) Allowed() bool { return d.Status == Verified || d.Status == NewerUnverified }

// Evaluate accepts native CLI output and bare MCP/session versions, never arbitrary text.
func Evaluate(name, raw string) Decision {
	return EvaluateForPlatform(name, raw, runtime.GOOS, runtime.GOARCH)
}

// EvaluateForPlatform evaluates explicit adapter host facts without granting authority.
func EvaluateForPlatform(runtime, raw, os, arch string) Decision {
	p := forRuntime(runtime)
	d := Decision{Runtime: runtime, Minimum: p.minimum, Status: Unknown, PolicyRevision: Revision,
		OS: os, Arch: arch, AdapterRevision: AdapterRevision, Profile: PreviewProfile}
	if p.minimum == "" || len(raw) > 160 {
		return d
	}
	text := strings.Trim(raw, " \t\r\n")
	if runtime == Claude {
		text = strings.TrimSuffix(text, " (Claude Code)")
	}
	if runtime == Codex {
		text = strings.TrimPrefix(text, "codex-cli ")
	}
	v, err := Parse(text)
	if err != nil {
		return d
	}
	d.Version = v.Text
	minimum, err := Parse(p.minimum)
	if err != nil {
		d.Version = ""
		return d
	}
	if Compare(v, minimum) < 0 {
		d.Status = BelowMinimum
		return d
	}
	d.Status = NewerUnverified
	for _, e := range p.verified {
		if e.Version == v.Text {
			d.Reason = UnverifiedPlatform
			if e.OS != os || e.Arch != arch || e.AdapterRevision != d.AdapterRevision || e.Profile != d.Profile {
				continue
			}
			d.Status, d.Evidence = Verified, []Evidence{e}
			d.Reason = ""
			break
		}
	}
	return d
}

// Error carries only bounded public facts; raw host diagnostics stay outside it.
type Error struct {
	Code                 string   `json:"code"`
	Runtime              string   `json:"runtime"`
	DetectedVersion      string   `json:"detected_version"`
	ContractID           string   `json:"contract_id,omitempty"`
	Phase                string   `json:"phase,omitempty"`
	AffectedCapabilities []string `json:"affected_capabilities,omitempty"`
	PolicyRevision       string   `json:"policy_revision"`
	Action               string   `json:"action"`
}

func (e *Error) Error() string { return e.Code }

// Check refuses unknown/below-minimum versions before an adapter has side effects.
func Check(runtime, raw string) error {
	d := Evaluate(runtime, raw)
	if d.Allowed() {
		return nil
	}
	code := "runtime_version_unknown"
	if d.Status == BelowMinimum {
		code = "unsupported_version"
	}
	return &Error{Code: code, Runtime: d.Runtime, DetectedVersion: d.Version, PolicyRevision: Revision,
		Action: "Select a runtime on PATH at or above minimum " + d.Minimum + ", resume the same selected session and rerun peerturn setup " + runtime + " / bind."}
}

// ErrorCode classifies central policy failures without parsing untrusted text.
func ErrorCode(err error) string {
	if e, ok := errors.AsType[*Error](err); ok {
		return e.Code
	}
	return ""
}

// ContractFailure closes only the named capabilities. It never changes owner settings.
func ContractFailure(runtime, version, id, phase string, capabilities ...string) *Error {
	d := Evaluate(runtime, version)
	return &Error{Code: "runtime_contract_changed", Runtime: runtime, DetectedVersion: d.Version,
		ContractID: id, Phase: phase, AffectedCapabilities: capabilities, PolicyRevision: Revision,
		Action: "Update PeerTurn, reload/resume the same runtime session and rerun doctor / bind. Keep the affected capability disabled until the contract passes."}
}

// AskProfile is independently qualified; general runtime verification cannot
// silently extend the closed responder profile to another release.
func AskProfile(name, raw string) (string, bool) {
	return AskProfileForPlatform(name, raw, runtime.GOOS, runtime.GOARCH)
}

// AskProfileForPlatform keeps restricted responder evidence Linux ARM64 only.
func AskProfileForPlatform(runtime, raw, os, arch string) (string, bool) {
	d := EvaluateForPlatform(runtime, raw, os, arch)
	p := forRuntime(runtime)
	if d.Status != Verified || os != "linux" || arch != "arm64" {
		return "", false
	}
	profile, ok := p.askProfiles[d.Version]
	return profile, ok
}
