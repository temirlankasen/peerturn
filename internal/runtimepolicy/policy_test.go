package runtimepolicy

import (
	"strings"
	"testing"
)

func TestVersionPolicy(t *testing.T) {
	for _, test := range []struct{ runtime, raw, version, status string }{
		{Claude, "2.1.306 (Claude Code)\n", "2.1.306", Verified},
		{Codex, "codex-cli 0.163.0", "0.163.0", Verified},
		{Claude, "2.1.308", "2.1.308", NewerUnverified},
		{Codex, "0.164.0", "0.164.0", NewerUnverified},
		{Claude, "999.0.0", "999.0.0", NewerUnverified},
		{Codex, "0.163.0+custom.1", "0.163.0+custom.1", NewerUnverified},
		{Claude, "2.1.306-rc.1", "2.1.306-rc.1", BelowMinimum},
		{Claude, "2.2.0-rc.1+build.7", "2.2.0-rc.1+build.7", NewerUnverified},
		{Codex, "0.162.999", "0.162.999", BelowMinimum},
		{Claude, "2.1.305", "2.1.305", BelowMinimum},
		{Claude, "codex-cli 2.1.306", "", Unknown},
		{Codex, "0.163.0 (Claude Code)", "", Unknown},
		{"other", "1.0.0", "", Unknown},
	} {
		t.Run(test.runtime+"/"+test.raw, func(t *testing.T) {
			got := EvaluateForPlatform(test.runtime, test.raw, "linux", "arm64")
			if got.Status != test.status || got.Version != test.version {
				t.Fatalf("Evaluate = %+v, want %s %s", got, test.version, test.status)
			}
			if got.Allowed() != (test.status == Verified || test.status == NewerUnverified) {
				t.Fatal("admission and qualification were conflated")
			}
		})
	}
}

func TestSemverOrdering(t *testing.T) {
	ordered := []string{"0.163.0-1", "0.163.0-alpha", "0.163.0-alpha.1", "0.163.0-alpha.beta", "0.163.0-beta", "0.163.0-beta.2", "0.163.0-beta.11", "0.163.0-rc.1", "0.163.0", "0.164.0", "2.1.999", "2.2.0", "10.0.0"}
	for i, raw := range ordered {
		a, err := Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		for j, other := range ordered {
			b, parseErr := Parse(other)
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if Compare(a, b) != want {
				t.Fatalf("compare %s / %s", raw, other)
			}
		}
	}
	a, err := Parse("0.163.0+one")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Parse("0.163.0+two")
	if err != nil {
		t.Fatal(err)
	}
	if Compare(a, b) != 0 {
		t.Fatal("build metadata changed precedence")
	}
}

func TestMalformedVersions(t *testing.T) {
	for _, raw := range []string{"", "1", "1.2", "v2.1.306", "01.2.3", "1.02.3", "1.2.03", "1.2.3-01", "1.2.3-", "1.2.3+", "1.2.3-a..b", "1.2.3+a..b", "1.2.3++a", "1.2.3/a", "1.2.3\n4.5.6", "1.2.3-ß", "4294967296.0.0", strings.Repeat("1", 129)} {
		if _, err := Parse(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
}

func TestEvaluateRejectsNonASCIIWhitespace(t *testing.T) {
	for _, space := range []string{"\u0085", "\u00a0", "\u1680", "\u2000", "\u2007", "\u2028", "\u2029", "\u202f", "\u205f", "\u3000"} {
		for _, runtime := range []string{Claude, Codex} {
			version := forRuntime(runtime).minimum
			for _, raw := range []string{space + version, version + space, space + version + space} {
				if decision := Evaluate(runtime, raw); decision.Status != Unknown || decision.Allowed() || decision.Version != "" {
					t.Errorf("accepted non-ASCII version %q: %+v", raw, decision)
				}
			}
		}
	}
}

func TestTypedFailures(t *testing.T) {
	for _, test := range []struct{ raw, code string }{{"2.1.305", "unsupported_version"}, {"private/token", "runtime_version_unknown"}} {
		err := Check(Claude, test.raw)
		if ErrorCode(err) != test.code || strings.Contains(err.Error(), "private") {
			t.Fatalf("unsafe failure: %v", err)
		}
	}
	if err := Check(Codex, "9.0.0"); err != nil {
		t.Fatal(err)
	}
	err := ContractFailure(Codex, "0.164.0", "queue.list", "probe", "coauthor")
	if err.Code != "runtime_contract_changed" || err.Action == "" || len(err.AffectedCapabilities) != 1 {
		t.Fatalf("contract failure: %+v", err)
	}
}

func TestAskProfileRequiresSeparateVerification(t *testing.T) {
	for _, raw := range []string{"2.1.305", "2.1.308", "2.1.306+custom", "unknown"} {
		if _, ok := AskProfile(Claude, raw); ok {
			t.Fatalf("ask inherited profile for %s", raw)
		}
	}
	profile, ok := AskProfileForPlatform(Claude, "2.1.306", "linux", "arm64")
	if !ok || profile != "peerturn/claude/2.1.306/m6c/read-only/v4" {
		t.Fatalf("profile changed existing evidence: %s", profile)
	}
	if _, ok := AskProfile(Codex, "0.163.0"); ok {
		t.Fatal("Codex ask opened")
	}
}
