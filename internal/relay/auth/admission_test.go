package auth

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestAuthorizationRunsAfterSignatureBeforeReplay(t *testing.T) {
	r, key := fixture(t, nil)
	kid := base64.RawURLEncoding.EncodeToString(key[ed25519.SeedSize:])
	authorized, replayed := false, false
	v, err := NewVerifier(Options{
		PublicAuthority: "relay.example", Now: func() time.Time { return fixtureTime },
		Authorize: func(ctx context.Context, request *http.Request, principal string) error {
			if ctx != t.Context() || request != r || principal != kid || replayed {
				t.Fatal("authorization received wrong context, request, principal or order")
			}
			authorized = true
			return nil
		},
		Replay: func(context.Context, string, string, int64) error {
			if !authorized {
				t.Fatal("nonce written before endpoint authorization")
			}
			replayed = true
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := v.Verify(t.Context(), r, nil); err != nil || got != kid || !replayed {
		t.Fatalf("Verify = %q, %v; replayed=%v", got, err, replayed)
	}
	authorized = false
	r.Header.Set("Signature", "sig1=:"+base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))+":")
	if _, err := v.Verify(t.Context(), r, nil); !errors.Is(err, ErrUnauthorized) || authorized {
		t.Fatal("invalid signature reached authorization")
	}
}

func TestAuthorizationRejectionsDoNotConsumeNonce(t *testing.T) {
	storageFailure := errors.New("storage read failed")
	for name, result := range map[string]struct{ returned, want error }{
		"forbidden": {ErrForbidden, ErrForbidden},
		"limited":   {ErrRateLimited, ErrRateLimited},
		"storage":   {storageFailure, ErrUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			r, _ := fixture(t, nil)
			v, err := NewVerifier(Options{
				PublicAuthority: "relay.example", Now: func() time.Time { return fixtureTime },
				Authorize: func(context.Context, *http.Request, string) error { return result.returned },
				Replay: func(context.Context, string, string, int64) error {
					t.Fatal("rejected endpoint authorization consumed nonce")
					return nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if key, err := v.Verify(t.Context(), r, nil); key != "" || !errors.Is(err, result.want) || !errors.Is(err, result.returned) {
				t.Fatalf("Verify = %q, %v", key, err)
			}
		})
	}
}
