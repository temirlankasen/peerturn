package config

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestDefaultsKeepAdmissionClosed(t *testing.T) {
	cfg := Defaults()
	want := SelfServiceLimits{ActiveInvites: 4, DailyInvites: 20, LifetimeInvites: 128, Inviters: 100}
	if cfg.AdmissionMode != AdmissionClosed || cfg.SelfService != want {
		t.Fatalf("programmatic defaults: mode=%q, self_service=%+v; want closed, %+v", cfg.AdmissionMode, cfg.SelfService, want)
	}
}

func TestAdmissionProgrammaticZeroValuesAreRejected(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*Config)
	}{
		{"empty mode", func(c *Config) { c.AdmissionMode = "" }},
		{"unknown mode", func(c *Config) { c.AdmissionMode = "open" }},
		{"zero policy", func(c *Config) { c.SelfService = SelfServiceLimits{} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := parseAdmissionFixture(t, "", "")
			test.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("invalid programmatic admission configuration was silently defaulted")
			}
		})
	}
}

func TestParseAdmissionPreservesOperatorTrustAndLimits(t *testing.T) {
	legacy := parseAdmissionFixture(t, "", "")
	for _, mode := range admissionTestModes {
		t.Run(mode.name, func(t *testing.T) {
			cfg := parseAdmissionFixture(t, mode.literal, selfServiceDefaultTOML)
			if !reflect.DeepEqual(cfg.AdminKeys, legacy.AdminKeys) ||
				!reflect.DeepEqual(cfg.TrustedProxyPrefixes(), legacy.TrustedProxyPrefixes()) ||
				cfg.ClientIPHeader != legacy.ClientIPHeader || cfg.TLSCert != legacy.TLSCert || cfg.TLSKey != legacy.TLSKey ||
				cfg.Limits != legacy.Limits || cfg.PublicAuthority != legacy.PublicAuthority ||
				cfg.Listen != legacy.Listen || cfg.PrivateListen != legacy.PrivateListen || cfg.DataDir != legacy.DataDir {
				t.Fatal("admission configuration changed existing operator trust, listeners or resource limits")
			}
		})
	}
}

func TestParseAdmissionKeepsTLSAlternativeToExplicitProxy(t *testing.T) {
	for _, mode := range admissionTestModes {
		t.Run(mode.name, func(t *testing.T) {
			input := string(admissionTOML(mode.literal, ""))
			input = replaceAdmissionRoot(input, "trusted_proxies", "")
			input += "tls_cert = '/etc/tls/cert.pem'\ntls_key = '/etc/tls/key.pem'\n"
			cfg, err := Parse([]byte(input + selfServiceDefaultTOML))
			if err != nil {
				t.Fatalf("explicit TLS pair without a trusted proxy rejected: %v", err)
			}
			if cfg.TLSCert != "/etc/tls/cert.pem" || cfg.TLSKey != "/etc/tls/key.pem" || len(cfg.TrustedProxies) != 0 {
				t.Fatal("explicit TLS or proxy configuration was changed")
			}
		})
	}
}

func TestParseAdmissionNeverRelaxesAdminTLSOrProxyValidation(t *testing.T) {
	for _, mode := range admissionTestModes {
		t.Run(mode.name, func(t *testing.T) {
			cfg := parseAdmissionFixture(t, mode.literal, selfServiceDefaultTOML)
			for _, test := range []struct {
				name, field, value, extra string
			}{
				{"missing administrators", "admin_keys", "", ""},
				{"empty administrators", "admin_keys", "[]", ""},
				{"invalid administrator", "admin_keys", "['AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA']", ""},
				{"duplicate administrators", "admin_keys", fmt.Sprintf("['%s', '%s']", cfg.AdminKeys[0], cfg.AdminKeys[0]), ""},
				{"missing TLS and proxy", "trusted_proxies", "", ""},
				{"empty proxy without TLS", "trusted_proxies", "[]", ""},
				{"public proxy wildcard", "trusted_proxies", "['0.0.0.0/0']", ""},
				{"public proxy subnet", "trusted_proxies", "['198.51.100.0/24']", ""},
				{"duplicate proxies", "trusted_proxies", "['127.0.0.1/32', '127.0.0.1/32']", ""},
				{"partial TLS", "trusted_proxies", "['127.0.0.1/32']", "tls_cert = '/etc/tls/cert.pem'\n"},
				{"relative TLS paths", "trusted_proxies", "", "tls_cert = './cert.pem'\ntls_key = './key.pem'\n"},
				{"unsupported IP header", "client_ip_header", "", "client_ip_header = 'Forwarded'\n"},
			} {
				t.Run(test.name, func(t *testing.T) {
					input := replaceAdmissionRoot(string(admissionTOML(mode.literal, "")), test.field, test.value)
					if _, err := Parse([]byte(input + test.extra + selfServiceDefaultTOML)); err == nil {
						t.Fatal("admission mode bypassed an existing administrator or transport trust requirement")
					}
				})
			}
		})
	}
}

func replaceAdmissionRoot(input, field, value string) string {
	lines := strings.Split(input, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, field+" = ") {
			lines[i] = ""
			if value != "" {
				lines[i] = field + " = " + value
			}
		}
	}
	return strings.Join(lines, "\n")
}
