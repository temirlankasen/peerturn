package config

import (
	"net/netip"
	"strings"
	"testing"
)

func TestPublicAuthorityIsRequired(t *testing.T) {
	input := strings.ReplaceAll(validTOML(), "public_authority = 'relay.example'\n", "")
	if _, err := Parse([]byte(input)); err == nil {
		t.Fatal("missing public authority accepted")
	}
}

func TestPublicProxyTrustIsRejected(t *testing.T) {
	for _, prefix := range []string{"0.0.0.0/1", "128.0.0.0/1", "198.51.100.0/24", netip.PrefixFrom(netip.AddrFrom4([4]byte{10, 0, 0, 0}), 7).String(), "::/1", "2001:db8::/32"} {
		t.Run(prefix, func(t *testing.T) {
			input := strings.Replace(validTOML(), "127.0.0.1/32", prefix, 1)
			if _, err := Parse([]byte(input)); err == nil {
				t.Fatal("public proxy trust accepted")
			}
		})
	}
}

func TestCanonicalAuthorityAndPrivateProxyBoundaries(t *testing.T) {
	for _, authority := range []string{"", "HTTPS://relay.example", "RELAY.example", "relay.example:443", "relay.example.", "relay.example:0444", "user" + "@" + "relay.example", "relay.example/path"} {
		t.Run("authority "+authority, func(t *testing.T) {
			input := strings.Replace(validTOML(), "relay.example", authority, 1)
			if _, err := Parse([]byte(input)); err == nil {
				t.Fatal("noncanonical authority accepted")
			}
		})
	}
	for _, boundary := range []netip.Prefix{
		netip.MustParsePrefix("127.0.0.1/32"),
		netip.PrefixFrom(netip.AddrFrom4([4]byte{10, 0, 0, 0}), 8),
		netip.PrefixFrom(netip.AddrFrom4([4]byte{172, 16, 0, 0}), 12),
		netip.PrefixFrom(netip.AddrFrom4([4]byte{192, 168, 0, 0}), 16),
		netip.MustParsePrefix("::1/128"), netip.MustParsePrefix("fc00::/7"),
	} {
		prefix := boundary.String()
		t.Run("proxy "+prefix, func(t *testing.T) {
			input := strings.Replace(validTOML(), "127.0.0.1/32", prefix, 1)
			if _, err := Parse([]byte(input)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestResourceDefaultsAndExplicitLimits(t *testing.T) {
	cfg, err := Parse([]byte(validTOML()))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ClientIPHeader != "X-Forwarded-For" || cfg.Limits.IPv6PrefixBits != 64 || cfg.Limits.RequestsPerIP != 32 || cfg.Limits.BodyReadSeconds != 5 || cfg.Limits.PollsPerKey != 4 || cfg.Limits.PollsPerMailbox != 4 || cfg.Limits.StreamsPerMailbox != 4 || cfg.Limits.MaxConnections != 512 {
		t.Fatalf("unexpected resource defaults: %#v", cfg)
	}
	input := validTOML() + "client_ip_header = 'X-Real-IP'\n[limits]\nipv6_prefix_bits = 48\nrequests_per_ip = 3\nbody_read_seconds = 2\npolls_per_key = 2\npolls_per_mailbox = 3\nstreams_per_mailbox = 5\nmax_connections = 100\n"
	cfg, err = Parse([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ClientIPHeader != "X-Real-IP" || cfg.Limits.IPv6PrefixBits != 48 || cfg.Limits.RequestsPerIP != 3 || cfg.Limits.BodyReadSeconds != 2 || cfg.Limits.PollsPerKey != 2 || cfg.Limits.PollsPerMailbox != 3 || cfg.Limits.StreamsPerMailbox != 5 || cfg.Limits.MaxConnections != 100 {
		t.Fatalf("explicit resources not loaded: %#v", cfg)
	}
}

func TestInvalidResourceLimitsAreRejected(t *testing.T) {
	for name, mutate := range map[string]func(*Config){
		"unknown client IP header": func(c *Config) { c.ClientIPHeader = "Forwarded" },
		"case altered header":      func(c *Config) { c.ClientIPHeader = "x-forwarded-for" },
		"IPv6 too broad":           func(c *Config) { c.Limits.IPv6PrefixBits = 47 },
		"IPv6 too narrow":          func(c *Config) { c.Limits.IPv6PrefixBits = 129 },
		"no per IP cap":            func(c *Config) { c.Limits.RequestsPerIP = 0 },
		"large per IP cap":         func(c *Config) { c.Limits.RequestsPerIP = 65 },
		"no body deadline":         func(c *Config) { c.Limits.BodyReadSeconds = 0 },
		"long body deadline":       func(c *Config) { c.Limits.BodyReadSeconds = 11 },
		"no per key polls":         func(c *Config) { c.Limits.PollsPerKey = 0 },
		"large per key polls":      func(c *Config) { c.Limits.PollsPerKey = 33 },
		"no per box polls":         func(c *Config) { c.Limits.PollsPerMailbox = 0 },
		"large per box polls":      func(c *Config) { c.Limits.PollsPerMailbox = 33 },
		"no per box streams":       func(c *Config) { c.Limits.StreamsPerMailbox = 0 },
		"large per box streams":    func(c *Config) { c.Limits.StreamsPerMailbox = c.Limits.MaxStreams + 1 },
		"no connection cap":        func(c *Config) { c.Limits.MaxConnections = 0 },
		"large connection cap":     func(c *Config) { c.Limits.MaxConnections = 100001 },
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := Parse([]byte(validTOML()))
			if err != nil {
				t.Fatal(err)
			}
			mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("invalid resource limit accepted")
			}
		})
	}
}
