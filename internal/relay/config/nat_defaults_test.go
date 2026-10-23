package config

import (
	"fmt"
	"testing"
)

func TestDefaultsAllowSharedNAT(t *testing.T) {
	cfg, err := Parse([]byte(validTOML()))
	if err != nil {
		t.Fatal(err)
	}
	limits := cfg.Limits
	if limits.RequestsPerIP != 32 || limits.RatePerMinute != 600 || limits.RateBurst != 120 {
		t.Fatalf("shared NAT defaults: requests_per_ip=%d rate_per_minute=%d rate_burst=%d; want 32/600/120",
			limits.RequestsPerIP, limits.RatePerMinute, limits.RateBurst)
	}
}

func TestNATLimitsRetainValidationBounds(t *testing.T) {
	for _, tc := range []struct {
		name   string
		limits string
		valid  bool
	}{
		{"minimum", "requests_per_ip = 1\nrate_per_minute = 1\nrate_burst = 1", true},
		{"maximum", "requests_per_ip = 64\nrate_per_minute = 100000\nrate_burst = 100000", true},
		{"operator override", "requests_per_ip = 8\nrate_per_minute = 120\nrate_burst = 30", true},
		{"zero requests", "requests_per_ip = 0", false},
		{"negative requests", "requests_per_ip = -1", false},
		{"excess requests", "requests_per_ip = 65", false},
		{"zero rate", "rate_per_minute = 0", false},
		{"negative rate", "rate_per_minute = -1", false},
		{"excess rate", "rate_per_minute = 100001", false},
		{"zero burst", "rate_burst = 0", false},
		{"negative burst", "rate_burst = -1", false},
		{"burst exceeds rate", "rate_burst = 601", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(fmt.Appendf(nil, "%s[limits]\n%s\n", validTOML(), tc.limits))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t, error=%v", tc.valid, err)
			}
		})
	}
}
