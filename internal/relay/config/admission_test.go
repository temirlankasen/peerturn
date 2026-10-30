package config

import (
	"fmt"
	"reflect"
	"testing"
)

var admissionTestModes = []struct {
	name    string
	literal string
	want    AdmissionMode
}{
	{"omitted", "", AdmissionClosed},
	{"closed", "'closed'", AdmissionClosed},
	{"self_service", "'self_service'", AdmissionSelfService},
}

// Keep the selector at the root; a following table must not capture it.
func admissionTOML(mode, policy string) []byte {
	input := validTOML()
	if mode != "" {
		input += "admission_mode = " + mode + "\n"
	}
	return []byte(input + policy)
}

func parseAdmissionFixture(t *testing.T, mode, policy string) Config {
	t.Helper()
	cfg, err := Parse(admissionTOML(mode, policy))
	if err != nil {
		t.Fatalf("valid admission configuration rejected: %v", err)
	}
	return cfg
}

func TestParseAdmissionAcceptsExplicitModes(t *testing.T) {
	for _, mode := range admissionTestModes {
		t.Run(mode.name, func(t *testing.T) {
			cfg := parseAdmissionFixture(t, mode.literal, "")
			if cfg.AdmissionMode != mode.want {
				t.Fatalf("admission_mode=%q, want %q", cfg.AdmissionMode, mode.want)
			}
		})
	}
}

func TestParseAdmissionKeepsExplicitModesDistinct(t *testing.T) {
	closed := parseAdmissionFixture(t, "'closed'", "")
	selfService := parseAdmissionFixture(t, "'self_service'", "")
	if reflect.DeepEqual(closed, selfService) {
		t.Fatal("explicit admission_mode was accepted but discarded")
	}
}

func TestParseAdmissionMissingModeMatchesExplicitClosed(t *testing.T) {
	for _, policy := range []string{"", "[self_service]\n", selfServiceDefaultTOML} {
		t.Run(fmt.Sprintf("policy_bytes_%d", len(policy)), func(t *testing.T) {
			implicit := parseAdmissionFixture(t, "", policy)
			closed := parseAdmissionFixture(t, "'closed'", policy)
			if implicit.AdmissionMode != AdmissionClosed || closed.AdmissionMode != AdmissionClosed {
				t.Fatal("self_service policy table enabled admission without an explicit mode")
			}
			if !reflect.DeepEqual(implicit, closed) {
				t.Fatal("omitting admission_mode must produce the explicit closed configuration")
			}
		})
	}
}

func TestParseAdmissionRejectsUnknownModesAndTypes(t *testing.T) {
	for _, literal := range []string{
		"''", "'open'", "'self-service'", "'CLOSED'", "'SELF_SERVICE'",
		"' closed'", "'closed '", "'self_service '",
		"true", "false", "0", "1", "1.0", "['closed']", "{ mode = 'closed' }",
	} {
		t.Run(literal, func(t *testing.T) {
			if _, err := Parse(admissionTOML(literal, "")); err == nil {
				t.Fatal("unknown or incorrectly typed admission_mode accepted")
			}
		})
	}
}

func TestParseAdmissionKeepsUnknownFieldsStrict(t *testing.T) {
	for _, mode := range admissionTestModes {
		t.Run(mode.name, func(t *testing.T) {
			parseAdmissionFixture(t, mode.literal, selfServiceDefaultTOML)
			for name, extra := range map[string]string{
				"root typo":             "admisson_mode = 'self_service'\n",
				"root policy cap":       "active_invites = 4\n",
				"unknown table":         "[self_services]\nactive_invites = 4\n",
				"unknown policy field":  "[self_service]\ndaily_invite = 20\n",
				"nested admission mode": "[self_service]\nadmission_mode = 'self_service'\n",
				"misplaced legacy cap":  "[self_service]\ntotal_bytes = 1073741824\n",
				"misplaced new cap":     "[limits]\nactive_invites = 4\n",
				"nested unknown table":  "[self_service.extra]\nactive_invites = 4\n",
			} {
				t.Run(name, func(t *testing.T) {
					if _, err := Parse(admissionTOML(mode.literal, extra)); err == nil {
						t.Fatal("unknown or misplaced TOML setting accepted")
					}
				})
			}
		})
	}
}
