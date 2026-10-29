package runtimepolicy

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestPackagedVerifiedRecords(t *testing.T) {
	records, err := decodeVerified(verifiedJSON)
	if err != nil {
		t.Fatal(err)
	}
	for runtime, baseline := range map[string]Evidence{
		Claude: {Version: "2.1.306", Reference: "m3-claude; m6b Linux ARM64 gateway; macos-live-7 limitations"},
		Codex:  {Version: "0.163.0", Reference: "m4-codex/fix-20261022; macos-live-7 limitations"},
	} {
		if len(records[runtime]) == 0 || records[runtime][0].Version != baseline.Version ||
			(records[runtime][0].Reference != baseline.Reference && !strings.HasPrefix(records[runtime][0].Reference, baseline.Reference+"; https://github.com/")) {
			t.Fatalf("%s historical baseline missing: %+v", runtime, records[runtime])
		}
		for _, evidence := range records[runtime] {
			decision := EvaluateForPlatform(runtime, evidence.Version, evidence.OS, evidence.Arch)
			if decision.Status != Verified || len(decision.Evidence) != 1 || decision.Evidence[0] != evidence {
				t.Fatalf("%s packaged evidence not consumed: %+v", runtime, decision)
			}
		}
	}
}

func TestVerifiedRecordsRejectMalformedData(t *testing.T) {
	validClaude := `[` + testEvidence("2.1.306", "historical Claude evidence") + `]`
	validCodex := `[` + testEvidence("0.163.0", "historical Codex evidence") + `]`
	valid := `{"claude":` + validClaude + `,"codex":` + validCodex + `}`
	for name, data := range map[string]string{
		"empty": "", "null": "null", "array": "[]", "truncated": `{"claude":`,
		"trailing": valid + `{}`, "oversized": strings.Repeat(" ", 65537),
		"missing runtime":   `{"claude":` + validClaude + `}`,
		"extra runtime":     strings.TrimSuffix(valid, "}") + `,"other":[]}`,
		"duplicate runtime": strings.TrimSuffix(valid, "}") + `,"claude":` + validClaude + `}`,
		"null records":      `{"claude":null,"codex":` + validCodex + `}`,
		"empty records":     `{"claude":[],"codex":` + validCodex + `}`,
		"duplicate version": `{"claude":[` + strings.Trim(validClaude, "[]") + `,` + strings.Trim(validClaude, "[]") + `],"codex":` + validCodex + `}`,
		"invalid UTF-8":     strings.Replace(valid, "historical Claude evidence", "\xff", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if records, err := decodeVerified([]byte(data)); err == nil || records != nil {
				t.Fatalf("accepted malformed registry: %+v", records)
			}
		})
	}
	for name, record := range map[string]string{
		"missing version":   `{"reference":"evidence"}`,
		"missing reference": `{"version":"2.1.306"}`,
		"empty reference":   `{"version":"2.1.306","reference":"  "}`,
		"control reference": `{"version":"2.1.306","reference":"evidence\nforged"}`,
		"long reference":    fmt.Sprintf(`{"version":"2.1.306","reference":%q}`, strings.Repeat("x", 2049)),
		"unknown field":     `{"version":"2.1.306","reference":"evidence","ask_profile":"open"}`,
		"duplicate field":   `{"version":"2.1.306","version":"2.1.308","reference":"evidence"}`,
		"invalid version":   `{"version":"02.1.306","reference":"evidence"}`,
		"version type":      `{"version":21306,"reference":"evidence"}`,
		"reference type":    `{"version":"2.1.306","reference":null}`,
		"null record":       `null`,
		"missing platform":  `{"version":"2.1.306","reference":"evidence"}`,
		"empty platform":    strings.Replace(testEvidence("2.1.306", "evidence"), `"os":"linux"`, `"os":""`, 1),
		"non-ASCII profile": strings.Replace(testEvidence("2.1.306", "evidence"), PreviewProfile, "préview", 1),
		"duplicate profile": strings.TrimSuffix(testEvidence("2.1.306", "evidence"), "}") + `,"profile":"other"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if strings.HasPrefix(record, "{") && !strings.Contains(record, `"os"`) && name != "missing platform" {
				record = strings.TrimSuffix(record, "}") + fmt.Sprintf(`,"os":"linux","arch":"arm64","adapter_revision":%q,"profile":%q}`, AdapterRevision, PreviewProfile)
			}
			data := `{"claude":[` + record + `],"codex":` + validCodex + `}`
			if records, err := decodeVerified([]byte(data)); err == nil || records != nil {
				t.Fatalf("accepted malformed evidence: %+v", records)
			}
		})
	}
	entries := make([]string, 257)
	for i := range entries {
		entries[i] = testEvidence(fmt.Sprintf("2.2.%d", i), "evidence")
	}
	atLimit := `{"claude":[` + strings.Join(entries[:256], ",") + `],"codex":` + validCodex + `}`
	if _, err := decodeVerified([]byte(atLimit)); err != nil {
		t.Fatal("rejected evidence records at supported limit")
	}
	tooMany := `{"claude":[` + strings.Join(entries, ",") + `],"codex":` + validCodex + `}`
	if _, err := decodeVerified([]byte(tooMany)); err == nil {
		t.Fatal("accepted an unbounded number of evidence records")
	}
}

func TestAddedVerificationCannotExtendAsk(t *testing.T) {
	records, err := decodeVerified([]byte(`{"claude":[` + testEvidence("2.1.306", "historical") + `,` +
		testEvidence("2.1.308", "new qualification") + `,` + testEvidence("1.0.0", "cannot lower minimum") + `],"codex":[` +
		testEvidence("0.163.0", "historical") + `,` + testEvidence("0.164.0", "new qualification") + `]}`))
	if err != nil {
		t.Fatal(err)
	}
	original := packagedVerified
	packagedVerified = records
	t.Cleanup(func() { packagedVerified = original })
	for runtime, version := range map[string]string{Claude: "2.1.308", Codex: "0.164.0"} {
		if decision := EvaluateForPlatform(runtime, version, "linux", "arm64"); decision.Status != Verified {
			t.Fatalf("added %s version not verified: %+v", runtime, decision)
		}
		if _, ok := AskProfile(runtime, version); ok {
			t.Fatalf("%s ask inherited general verification", runtime)
		}
	}
	if _, ok := AskProfileForPlatform(Claude, "2.1.306", "linux", "arm64"); !ok {
		t.Fatal("existing independently qualified ask profile lost")
	}
	if decision := Evaluate(Claude, "1.0.0"); decision.Status != BelowMinimum || decision.Allowed() {
		t.Fatalf("verification data lowered minimum: %+v", decision)
	}
}

func testEvidence(version, reference string) string {
	data, err := json.Marshal(Evidence{Version: version, Reference: reference, OS: "linux", Arch: "arm64",
		AdapterRevision: AdapterRevision, Profile: PreviewProfile})
	if err != nil {
		panic(err)
	}
	return string(data)
}

func TestInvalidPackagedDataCannotBlockAdmissionOrEnableAsk(t *testing.T) {
	original := packagedVerified
	packagedVerified = loadVerified([]byte(`{"claude":[]}`))
	t.Cleanup(func() { packagedVerified = original })
	for runtime, baseline := range map[string]string{Claude: "2.1.306", Codex: "0.163.0"} {
		decision := Evaluate(runtime, baseline)
		if !decision.Allowed() || decision.Status != NewerUnverified || len(decision.Evidence) != 0 {
			t.Fatalf("invalid package changed admission: %+v", decision)
		}
		if _, ok := AskProfile(runtime, baseline); ok {
			t.Fatalf("%s ask enabled without valid qualification data", runtime)
		}
	}
}
