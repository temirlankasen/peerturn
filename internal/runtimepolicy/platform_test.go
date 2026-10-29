package runtimepolicy

import (
	"runtime"
	"testing"
)

func TestPlatformQualification(t *testing.T) {
	for _, name := range []string{Claude, Codex} {
		baseline := forRuntime(name).minimum
		for _, platform := range []struct{ os, arch string }{
			{"linux", "arm64"}, {"darwin", "arm64"}, {"linux", "amd64"}, {"darwin", "amd64"}, {"windows", "arm64"},
		} {
			d := EvaluateForPlatform(name, baseline, platform.os, platform.arch)
			qualified := platform.arch == "arm64" && (platform.os == "linux" || platform.os == "darwin")
			if !d.Allowed() || d.OS != platform.os || d.Arch != platform.arch || d.Profile != PreviewProfile || d.AdapterRevision != AdapterRevision {
				t.Fatalf("platform facts/admission lost: %+v", d)
			}
			if qualified && (d.Status != Verified || len(d.Evidence) != 1 || d.Reason != "") {
				t.Fatalf("qualified platform not verified: %+v", d)
			}
			if !qualified && (d.Status != NewerUnverified || d.Reason != UnverifiedPlatform || len(d.Evidence) != 0) {
				t.Fatalf("platform inherited evidence: %+v", d)
			}
		}
		if got, want := Evaluate(name, baseline), EvaluateForPlatform(name, baseline, runtime.GOOS, runtime.GOARCH); got.Status != want.Status || got.OS != want.OS || got.Arch != want.Arch {
			t.Fatalf("host facts not used: %+v / %+v", got, want)
		}
		if d := EvaluateForPlatform(name, baseline+"+custom", "linux", "arm64"); d.Status != NewerUnverified || d.Reason != "" || len(d.Evidence) != 0 {
			t.Fatalf("modified build inherited platform evidence: %+v", d)
		}
	}
}

func TestEvidenceProfileCannotVerifyAnotherAdapter(t *testing.T) {
	old := packagedVerified
	t.Cleanup(func() { packagedVerified = old })
	for _, mismatch := range []string{"profile", "adapter_revision"} {
		e := old[Claude][0]
		if mismatch == "profile" {
			e.Profile = "different/v1"
		} else {
			e.AdapterRevision = "other"
		}
		packagedVerified = map[string][]Evidence{Claude: {e}}
		d := EvaluateForPlatform(Claude, e.Version, e.OS, e.Arch)
		if !d.Allowed() || d.Status != NewerUnverified || d.Reason != UnverifiedPlatform || len(d.Evidence) != 0 {
			t.Fatalf("mismatched %s verified: %+v", mismatch, d)
		}
	}
}

func TestAskQualificationIsLinuxARM64Only(t *testing.T) {
	for _, platform := range []struct{ os, arch string }{{"linux", "arm64"}, {"linux", "amd64"}, {"darwin", "arm64"}, {"windows", "arm64"}} {
		profile, ok := AskProfileForPlatform(Claude, "2.1.306", platform.os, platform.arch)
		want := platform.os == "linux" && platform.arch == "arm64"
		if ok != want || ok && profile != "peerturn/claude/2.1.306/m6c/read-only/v4" {
			t.Fatalf("unexpected ask profile for %+v: %q/%t", platform, profile, ok)
		}
	}
}

func TestVersionOnlyEvidenceCannotQualifyAPlatform(t *testing.T) {
	old := packagedVerified
	packagedVerified = loadVerified([]byte(`{"claude":[{"version":"2.1.306","reference":"version only"}],"codex":[{"version":"0.163.0","reference":"version only"}]}`))
	t.Cleanup(func() { packagedVerified = old })
	for _, runtime := range []string{Claude, Codex} {
		decision := Evaluate(runtime, forRuntime(runtime).minimum)
		if decision.Status != NewerUnverified || len(decision.Evidence) != 0 || !decision.Allowed() {
			t.Fatalf("version-only evidence qualified the host platform: %+v", decision)
		}
	}
}
