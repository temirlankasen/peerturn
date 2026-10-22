package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestLabelsCanBeRelabeledButMustMatch(t *testing.T) {
	r, _ := fixture(t, nil)
	v := verifier(t, func(context.Context, string, string, int64) error { return nil })
	r.Header.Set("Signature-Input", strings.Replace(r.Header.Get("Signature-Input"), "sig1=", "renamed=", 1))
	r.Header.Set("Signature", strings.Replace(r.Header.Get("Signature"), "sig1=", "renamed=", 1))
	if _, err := v.Verify(t.Context(), r, nil); err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Signature", strings.Replace(r.Header.Get("Signature"), "renamed=", "mismatch=", 1))
	if _, err := v.Verify(t.Context(), r, nil); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("mismatched signature labels accepted")
	}
}

func TestRejectsNoncanonicalAuthorityAndMalformedStructuredFields(t *testing.T) {
	for _, authority := range []string{"RELAY.example", "relay.example:443", "relay.example:0444", "relay.example.", "relay.example,evil.example", "user" + "@" + "relay.example", "relay.example:", "[192.0.2.1]"} {
		t.Run(authority, func(t *testing.T) {
			r, _ := fixture(t, nil)
			r.Host, r.URL.Host = authority, authority
			v := verifier(t, func(context.Context, string, string, int64) error {
				t.Fatal("invalid authority consumed nonce")
				return nil
			})
			if _, err := v.Verify(t.Context(), r, nil); !errors.Is(err, ErrUnauthorized) {
				t.Fatal("noncanonical authority accepted")
			}
		})
	}
	for _, field := range []string{"Signature", "Signature-Input"} {
		for _, value := range []string{"", "sig1=", "sig1=%\"x\"", "sig1=@", "sig1=:AA==:,sig1=:AA==:", "sig1=(\"@method\");created=1;created=2"} {
			r, _ := fixture(t, nil)
			r.Header.Set(field, value)
			v := verifier(t, func(context.Context, string, string, int64) error {
				t.Fatal("malformed fields consumed nonce")
				return nil
			})
			if _, err := v.Verify(t.Context(), r, nil); !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("malformed %s accepted", field)
			}
		}
	}
}

func TestBoundarySkewAndLifetimeAccepted(t *testing.T) {
	for _, offset := range []int64{-120, 60} {
		r, key := fixture(t, nil)
		manualSign(r, nil, key, fixtureTime.Unix()+offset, fixtureTime.Unix()+offset+60, "MDEyMzQ1Njc4OWFiY2RlZg")
		v := verifier(t, func(context.Context, string, string, int64) error { return nil })
		if _, err := v.Verify(t.Context(), r, nil); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDirectTLSDoesNotTrustClientProxyHeaders(t *testing.T) {
	r, _ := fixture(t, nil)
	r.Header.Set("X-Forwarded-Host", "evil.example")
	r.Header.Set("X-Forwarded-Proto", "http")
	v := verifier(t, func(context.Context, string, string, int64) error { return nil })
	if _, err := v.Verify(t.Context(), r, nil); err != nil {
		t.Fatal(err)
	}
	r.Method = http.MethodGet
	if _, err := v.Verify(t.Context(), r, nil); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("method transfer accepted")
	}
}

func TestValidateTransportForSignatureFreeNotifyTicket(t *testing.T) {
	r, _ := fixture(t, nil)
	v := verifier(t, func(context.Context, string, string, int64) error {
		t.Fatal("transport validation consumed nonce")
		return nil
	})
	r.Header = make(http.Header)
	if err := v.ValidateTransport(r); err != nil {
		t.Fatal(err)
	}
	r.TLS = nil
	if err := v.ValidateTransport(r); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("cleartext notify ticket accepted")
	}
}
