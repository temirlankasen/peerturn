package config

import (
	"fmt"
	"reflect"
	"testing"
)

const selfServiceDefaultTOML = "[self_service]\nactive_invites = 4\ndaily_invites = 20\nlifetime_invites = 128\ninviters = 100\n"

func TestParseSelfServiceDefaultsMatchExplicitPolicy(t *testing.T) {
	for _, mode := range admissionTestModes {
		t.Run(mode.name, func(t *testing.T) {
			implicit := parseAdmissionFixture(t, mode.literal, "")
			empty := parseAdmissionFixture(t, mode.literal, "[self_service]\n")
			explicit := parseAdmissionFixture(t, mode.literal, selfServiceDefaultTOML)
			want := SelfServiceLimits{ActiveInvites: 4, DailyInvites: 20, LifetimeInvites: 128, Inviters: 100}
			for _, cfg := range []Config{implicit, empty, explicit} {
				if cfg.AdmissionMode != mode.want || cfg.SelfService != want {
					t.Fatalf("mode=%q, self_service=%+v; want %q, %+v", cfg.AdmissionMode, cfg.SelfService, mode.want, want)
				}
			}
			if !reflect.DeepEqual(implicit, empty) || !reflect.DeepEqual(implicit, explicit) {
				t.Fatal("omitted and empty self_service tables must retain the explicit 4/20/128/100 defaults")
			}
		})
	}
}

func TestParseSelfServiceAcceptsBoundedOverrides(t *testing.T) {
	for _, mode := range admissionTestModes {
		t.Run(mode.name, func(t *testing.T) {
			for _, test := range []struct {
				name     string
				active   int
				daily    int
				lifetime int
				inviters int
			}{
				{"minimum", 1, 1, 1, 1},
				{"operator override", 2, 10, 64, 50},
				{"equal invitation caps", 128, 128, 128, 100},
				{"maximum", 128, 1024, 65536, 10000},
			} {
				t.Run(test.name, func(t *testing.T) {
					policy := fmt.Sprintf("[self_service]\nactive_invites = %d\ndaily_invites = %d\nlifetime_invites = %d\ninviters = %d\n",
						test.active, test.daily, test.lifetime, test.inviters)
					cfg := parseAdmissionFixture(t, mode.literal, policy)
					want := SelfServiceLimits{
						ActiveInvites: test.active, DailyInvites: test.daily, LifetimeInvites: test.lifetime, Inviters: test.inviters,
					}
					if cfg.AdmissionMode != mode.want || cfg.SelfService != want {
						t.Fatalf("mode=%q, self_service=%+v; want %q, %+v", cfg.AdmissionMode, cfg.SelfService, mode.want, want)
					}
				})
			}
		})
	}
}

func TestParseSelfServicePartialOverridesRespectDefaults(t *testing.T) {
	for _, mode := range admissionTestModes {
		t.Run(mode.name, func(t *testing.T) {
			defaults := parseAdmissionFixture(t, mode.literal, selfServiceDefaultTOML)
			for _, test := range []struct {
				setting string
				valid   bool
				want    SelfServiceLimits
			}{
				{"active_invites = 1", true, SelfServiceLimits{ActiveInvites: 1, DailyInvites: 20, LifetimeInvites: 128, Inviters: 100}},
				{"active_invites = 20", true, SelfServiceLimits{ActiveInvites: 20, DailyInvites: 20, LifetimeInvites: 128, Inviters: 100}},
				{"active_invites = 21", false, SelfServiceLimits{}},
				{"daily_invites = 4", true, SelfServiceLimits{ActiveInvites: 4, DailyInvites: 4, LifetimeInvites: 128, Inviters: 100}},
				{"daily_invites = 3", false, SelfServiceLimits{}},
				{"daily_invites = 128", true, SelfServiceLimits{ActiveInvites: 4, DailyInvites: 128, LifetimeInvites: 128, Inviters: 100}},
				{"daily_invites = 129", false, SelfServiceLimits{}},
				{"lifetime_invites = 20", true, SelfServiceLimits{ActiveInvites: 4, DailyInvites: 20, LifetimeInvites: 20, Inviters: 100}},
				{"lifetime_invites = 19", false, SelfServiceLimits{}},
				{"inviters = 1", true, SelfServiceLimits{ActiveInvites: 4, DailyInvites: 20, LifetimeInvites: 128, Inviters: 1}},
				{"inviters = 10000", true, SelfServiceLimits{ActiveInvites: 4, DailyInvites: 20, LifetimeInvites: 128, Inviters: 10000}},
			} {
				t.Run(test.setting, func(t *testing.T) {
					cfg, err := Parse(admissionTOML(mode.literal, "[self_service]\n"+test.setting+"\n"))
					if (err == nil) != test.valid {
						t.Fatalf("valid=%t, error=%v", test.valid, err)
					}
					if test.valid && (cfg.AdmissionMode != mode.want || cfg.SelfService != test.want || reflect.DeepEqual(cfg, defaults)) {
						t.Fatalf("mode=%q, self_service=%+v; want %q, %+v", cfg.AdmissionMode, cfg.SelfService, mode.want, test.want)
					}
				})
			}
		})
	}
}

func TestParseSelfServiceRejectsNonpositiveAndMistypedCaps(t *testing.T) {
	for _, mode := range admissionTestModes {
		t.Run(mode.name, func(t *testing.T) {
			parseAdmissionFixture(t, mode.literal, selfServiceDefaultTOML)
			for _, field := range []string{"active_invites", "daily_invites", "lifetime_invites", "inviters"} {
				for _, literal := range []string{"0", "-1", "9223372036854775807", "'4'", "4.0", "true", "[4]", "{ value = 4 }"} {
					t.Run(field+"="+literal, func(t *testing.T) {
						policy := fmt.Sprintf("[self_service]\n%s = %s\n", field, literal)
						if _, err := Parse(admissionTOML(mode.literal, policy)); err == nil {
							t.Fatal("nonpositive, excessive or incorrectly typed policy cap accepted")
						}
					})
				}
			}
		})
	}
}

func TestParseSelfServiceRejectsExcessiveAndInconsistentCaps(t *testing.T) {
	for _, mode := range admissionTestModes {
		t.Run(mode.name, func(t *testing.T) {
			parseAdmissionFixture(t, mode.literal, selfServiceDefaultTOML)
			for name, settings := range map[string]string{
				"active ceiling":       "active_invites = 129\ndaily_invites = 1024\nlifetime_invites = 65536\n",
				"daily ceiling":        "daily_invites = 1025\nlifetime_invites = 65536\n",
				"lifetime ceiling":     "lifetime_invites = 65537\n",
				"inviter ceiling":      "inviters = 10001\n",
				"active exceeds daily": "active_invites = 5\ndaily_invites = 4\n",
				"daily exceeds total":  "daily_invites = 129\nlifetime_invites = 128\n",
			} {
				t.Run(name, func(t *testing.T) {
					if _, err := Parse(admissionTOML(mode.literal, "[self_service]\n"+settings)); err == nil {
						t.Fatal("out-of-bound or inconsistent policy accepted")
					}
				})
			}
		})
	}
}

func TestParseSelfServiceRequiresTable(t *testing.T) {
	for _, policy := range []string{
		"self_service = true\n",
		"self_service = 4\n",
		"self_service = 'enabled'\n",
		"self_service = []\n",
		"[[self_service]]\nactive_invites = 4\n",
	} {
		t.Run(policy, func(t *testing.T) {
			if _, err := Parse(admissionTOML("'self_service'", policy)); err == nil {
				t.Fatal("self_service value must have the policy table type")
			}
		})
	}
}
