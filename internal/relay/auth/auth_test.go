package auth

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

var fixtureTime = time.Unix(1_800_000_000, 0)

func fixture(t *testing.T, body []byte) (*http.Request, ed25519.PrivateKey) {
	t.Helper()
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, ed25519.SeedSize))
	r := httptest.NewRequest(http.MethodPost, "https://relay.example/v1/mailboxes/m/messages?after=7", bytes.NewReader(body))
	r.RequestURI = r.URL.RequestURI()
	manualSign(r, body, key, fixtureTime.Unix(), fixtureTime.Unix()+60, "MDEyMzQ1Njc4OWFiY2RlZg")
	return r, key
}

func manualSign(r *http.Request, body []byte, key ed25519.PrivateKey, created, expires int64, nonce string) {
	pub := key[ed25519.SeedSize:]
	kid := base64.RawURLEncoding.EncodeToString(pub)
	digest := sha256.Sum256(body)
	contentDigest := "sha-256=:" + base64.StdEncoding.EncodeToString(digest[:]) + ":"
	params := fmt.Sprintf(`("@method" "@authority" "@target-uri" "content-digest");created=%d;expires=%d;nonce="%s";keyid="%s";alg="ed25519"`, created, expires, nonce, kid)
	input := fmt.Sprintf("\"@method\": %s\n\"@authority\": %s\n\"@target-uri\": https://%s%s\n\"content-digest\": %s\n\"@signature-params\": %s", r.Method, r.Host, r.Host, r.URL.RequestURI(), contentDigest, params)
	r.Header.Set("Content-Digest", contentDigest)
	r.Header.Set("Signature-Input", "sig1="+params)
	r.Header.Set("Signature", "sig1=:"+base64.StdEncoding.EncodeToString(ed25519.Sign(key, []byte(input)))+":")
}

func verifier(t *testing.T, replay Replay) *Verifier {
	t.Helper()
	v, err := NewVerifier(Options{PublicAuthority: "relay.example", Now: func() time.Time { return fixtureTime }, Replay: replay})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestVerifyAuthenticatesExactRequestAndRecordsNonce(t *testing.T) {
	for _, body := range [][]byte{nil, []byte("opaque ciphertext")} {
		r, key := fixture(t, body)
		calls := 0
		v := verifier(t, func(_ context.Context, kid, nonce string, expiry int64) error {
			calls++
			if kid != base64.RawURLEncoding.EncodeToString(key[ed25519.SeedSize:]) || nonce != "MDEyMzQ1Njc4OWFiY2RlZg" || expiry < fixtureTime.Unix()+120 {
				t.Fatal("nonce cache did not receive authenticated principal and minimum retention")
			}
			return nil
		})
		got, err := v.Verify(t.Context(), r, body)
		if err != nil || got == "" || calls != 1 {
			t.Fatalf("Verify = %q, %v, calls=%d", got, err, calls)
		}
	}
}

func TestVerifyRejectsMutationBeforeNonceWrite(t *testing.T) {
	tests := map[string]func(*http.Request){
		"method":              func(r *http.Request) { r.Method = http.MethodDelete },
		"authority":           func(r *http.Request) { r.Host = "other.example" },
		"path":                func(r *http.Request) { r.URL.Path = "/v1/invites/other"; r.RequestURI = r.URL.RequestURI() },
		"query":               func(r *http.Request) { r.URL.RawQuery = "after=8"; r.RequestURI = r.URL.RequestURI() },
		"missing digest":      func(r *http.Request) { r.Header.Del("Content-Digest") },
		"duplicate signature": func(r *http.Request) { r.Header.Add("Signature", r.Header.Get("Signature")) },
		"duplicate parameter": func(r *http.Request) {
			r.Header.Set("Signature-Input", r.Header.Get("Signature-Input")+`;created=1800000000`)
		},
		"remote key id": func(r *http.Request) {
			r.Header.Set("Signature-Input", strings.Replace(r.Header.Get("Signature-Input"), `keyid="`, `keyid="https://evil.example/`, 1))
		},
		"unsigned extra component": func(r *http.Request) {
			r.Header.Set("Signature-Input", strings.Replace(r.Header.Get("Signature-Input"), `"content-digest"`, `"accept"`, 1))
		},
		"http":           func(r *http.Request) { r.TLS = nil; r.URL.Scheme = "http" },
		"host ambiguity": func(r *http.Request) { r.Header.Set("Host", "evil.example") },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			r, _ := fixture(t, nil)
			mutate(r)
			v := verifier(t, func(context.Context, string, string, int64) error {
				t.Fatal("invalid request consumed nonce")
				return nil
			})
			if _, err := v.Verify(t.Context(), r, nil); !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("Verify error = %v", err)
			}
		})
	}
	for _, mismatch := range []struct{ signed, supplied []byte }{{nil, []byte("x")}, {[]byte("x"), nil}} {
		r, _ := fixture(t, mismatch.signed)
		v := verifier(t, func(context.Context, string, string, int64) error { t.Fatal("bad body consumed nonce"); return nil })
		if _, err := v.Verify(t.Context(), r, mismatch.supplied); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("body mismatch error = %v", err)
		}
	}
}

func TestVerifyRejectsTimeAndNonceViolations(t *testing.T) {
	for name, values := range map[string]struct {
		created, expires int64
		nonce            string
	}{
		"old":               {-121, -61, "MDEyMzQ1Njc4OWFiY2RlZg"},
		"future":            {61, 121, "MDEyMzQ1Njc4OWFiY2RlZg"},
		"long lifetime":     {0, 61, "MDEyMzQ1Njc4OWFiY2RlZg"},
		"negative lifetime": {1, 0, "MDEyMzQ1Njc4OWFiY2RlZg"},
		"short nonce":       {0, 60, "tiny"},
	} {
		t.Run(name, func(t *testing.T) {
			r, key := fixture(t, nil)
			manualSign(r, nil, key, fixtureTime.Unix()+values.created, fixtureTime.Unix()+values.expires, values.nonce)
			v := verifier(t, func(context.Context, string, string, int64) error {
				t.Fatal("bad freshness consumed nonce")
				return nil
			})
			if _, err := v.Verify(t.Context(), r, nil); !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("Verify error = %v", err)
			}
		})
	}
}

func TestReplayAndUnavailableDiffer(t *testing.T) {
	r, _ := fixture(t, nil)
	for _, entry := range []struct{ storage, want error }{{ErrReplay, ErrUnauthorized}, {errors.New("disk full"), ErrUnavailable}} {
		v := verifier(t, func(context.Context, string, string, int64) error { return entry.storage })
		if _, err := v.Verify(t.Context(), r, nil); !errors.Is(err, entry.want) {
			t.Fatalf("Verify = %v, want %v", err, entry.want)
		}
	}
	if _, err := NewVerifier(Options{}); err == nil {
		t.Fatal("missing durable replay store accepted")
	}
}

func TestTrustedProxyUsesOnlyImmediateAllowlistedAddress(t *testing.T) {
	proxy := netip.AddrFrom4([4]byte{10, 0, 0, 1})
	v, err := NewVerifier(Options{PublicAuthority: "relay.example", Now: func() time.Time { return fixtureTime }, Replay: func(context.Context, string, string, int64) error { return nil }, TrustedProxies: []netip.Prefix{netip.PrefixFrom(proxy, 32)}})
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range []netip.Addr{proxy, netip.AddrFrom4([4]byte{10, 0, 0, 2})} {
		r, _ := fixture(t, nil)
		r.TLS = nil
		r.URL.Scheme = ""
		r.URL.Host = ""
		r.Host = "internal:8080"
		r.RemoteAddr = netip.AddrPortFrom(address, 1234).String()
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-Forwarded-Host", "relay.example")
		_, err := v.Verify(t.Context(), r, nil)
		if address == proxy && err != nil {
			t.Fatal(err)
		}
		if address != proxy && !errors.Is(err, ErrUnauthorized) {
			t.Fatal("spoofed proxy accepted")
		}
		r.Header.Add("X-Forwarded-Host", "evil.example")
		if _, err := v.Verify(t.Context(), r, nil); !errors.Is(err, ErrUnauthorized) {
			t.Fatal("ambiguous proxy header accepted")
		}
	}
}

func TestSignProducesRFC9421Profile(t *testing.T) {
	r, key := fixture(t, []byte("ciphertext"))
	wantInput, wantSignature := r.Header.Get("Signature-Input"), r.Header.Get("Signature")
	r.Header = make(http.Header)
	if err := Sign(r, []byte("ciphertext"), key, fixtureTime, fixtureTime.Add(time.Minute), "MDEyMzQ1Njc4OWFiY2RlZg"); err != nil {
		t.Fatal(err)
	}
	if r.Header.Get("Signature-Input") != wantInput || r.Header.Get("Signature") != wantSignature {
		t.Fatal("signature differs from independent RFC9421 fixture")
	}
}
