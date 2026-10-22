package config

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validTOML() string {
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{5}, ed25519.SeedSize))
	return fmt.Sprintf("public_authority = 'relay.example'\ndata_dir = '/var/lib/peerturn'\ntrusted_proxies = ['127.0.0.1/32']\nadmin_keys = ['%s']\n", base64.RawURLEncoding.EncodeToString(key[ed25519.SeedSize:]))
}

func TestParseAppliesSafeDefaults(t *testing.T) {
	cfg, err := Parse([]byte(validTOML()))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:8443" || cfg.PrivateListen != "127.0.0.1:9090" || cfg.Limits.TotalBytes != 1<<30 || cfg.Limits.MailboxItems != 1000 || cfg.Limits.BodyBytes != 135168 || cfg.Limits.MetadataItems != 100000 {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}
	if len(cfg.TrustedProxyPrefixes()) != 1 {
		t.Fatal("proxy prefix missing")
	}
}

func TestParseRejectsUnknownAndUnsafeConfig(t *testing.T) {
	for name, extra := range map[string]string{
		"unknown":                   "mystery = true\n",
		"relative data":             "data_dir = '../data'\n",
		"public private listener":   "private_listen = '0.0.0.0:9090'\n",
		"hostname private listener": "private_listen = 'localhost:9090'\n",
		"public proxy all":          "trusted_proxies = ['0.0.0.0/0']\n",
		"missing key":               "admin_keys = []\n",
		"invalid key":               "admin_keys = ['AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA']\n",
		"partial tls":               "tls_cert = '/etc/cert.pem'\n",
		"relative bootstrap":        "bootstrap_file = './bootstrap.json'\n",
		"zero quota":                "[limits]\ntotal_bytes = 0\n",
		"large body":                "[limits]\nbody_bytes = 1048577\n",
		"long poll":                 "[limits]\nlong_poll_seconds = 26\n",
		"request shorter than poll": "[limits]\nrequest_timeout_seconds = 25\n",
		"unknown limit":             "[limits]\nmagic = 1\n",
	} {
		t.Run(name, func(t *testing.T) {
			input := validTOML()
			for line := range strings.SplitSeq(extra, "\n") {
				if key, _, ok := strings.Cut(line, " = "); ok && !strings.HasPrefix(extra, "[limits]") {
					lines := strings.Split(input, "\n")
					for i, prior := range lines {
						if strings.HasPrefix(prior, key+" = ") {
							lines[i] = ""
						}
					}
					input = strings.Join(lines, "\n")
				}
			}
			if _, err := Parse([]byte(input + extra)); err == nil {
				t.Fatal("unsafe configuration accepted")
			}
		})
	}
}

func TestTransportMustBeConfigured(t *testing.T) {
	input := strings.Replace(validTOML(), "trusted_proxies = ['127.0.0.1/32']", "", 1)
	if _, err := Parse([]byte(input)); err == nil {
		t.Fatal("plaintext relay accepted without trusted proxy")
	}
	if _, err := Parse([]byte(input + "tls_cert = '/etc/tls/cert.pem'\ntls_key = '/etc/tls/key.pem'\n")); err != nil {
		t.Fatal(err)
	}
}

func TestParseBoundsInputAndRejectsDuplicateAdminKeys(t *testing.T) {
	if _, err := Parse(bytes.Repeat([]byte{'x'}, 65537)); err == nil {
		t.Fatal("oversize config accepted")
	}
	cfg, err := Parse([]byte(validTOML()))
	if err != nil {
		t.Fatal(err)
	}
	cfg.AdminKeys = append(cfg.AdminKeys, cfg.AdminKeys[0])
	if err := cfg.Validate(); err == nil {
		t.Fatal("duplicate administrators accepted")
	}
}

func TestLoadExplicitRelativePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "relay.toml")
	if err := os.WriteFile(path, []byte(validTOML()), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	if _, err := Load("relay.toml"); err != nil {
		t.Fatal(err)
	}
}
