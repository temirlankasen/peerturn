package auth

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"
)

func TestForeignAuthorityIsRejectedBeforeReplay(t *testing.T) {
	for _, proxy := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct TLS", true: "trusted proxy"}[proxy], func(t *testing.T) {
			r, key := fixture(t, nil)
			r.Host, r.URL.Host = "foreign.example", "foreign.example"
			manualSign(r, nil, key, fixtureTime.Unix(), fixtureTime.Unix()+60, "MDEyMzQ1Njc4OWFiY2RlZg")
			opts := Options{PublicAuthority: "relay.example", Now: func() time.Time { return fixtureTime }, Replay: func(context.Context, string, string, int64) error {
				t.Fatal("foreign relay authority consumed nonce")
				return nil
			}}
			if proxy {
				proxyAddress := netip.AddrFrom4([4]byte{10, 0, 0, 1})
				opts.TrustedProxies = []netip.Prefix{netip.PrefixFrom(proxyAddress, 32)}
				r.TLS, r.URL.Scheme, r.URL.Host, r.Host = nil, "", "", "internal:8080"
				r.RemoteAddr = netip.AddrPortFrom(proxyAddress, 1234).String()
				r.Header.Set("X-Forwarded-Proto", "https")
				r.Header.Set("X-Forwarded-Host", "foreign.example")
			}
			v, err := NewVerifier(opts)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := v.Verify(t.Context(), r, nil); !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("foreign authority accepted: %v", err)
			}
			if err := v.ValidateTransport(r); !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("foreign authority accepted on ticket lane: %v", err)
			}
		})
	}
}

func TestNewVerifierRequiresAuthority(t *testing.T) {
	if _, err := NewVerifier(Options{Replay: func(context.Context, string, string, int64) error { return nil }}); err == nil {
		t.Fatal("missing public authority accepted")
	}
}
