// Package auth implements the relay's fixed RFC 9421 Ed25519 request profile.
package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"time"

	"github.com/dunglas/httpsfv"
	peercrypto "github.com/temirlankasen/peerturn/internal/crypto"
)

var (
	// ErrUnauthorized reports invalid signatures, freshness or replay.
	ErrUnauthorized = errors.New("request authentication failed")
	// ErrUnavailable reports unavailable replay or endpoint-admission storage.
	ErrUnavailable = errors.New("request authentication unavailable")
	// ErrForbidden reports a verified key without endpoint authority.
	ErrForbidden = errors.New("request authorization failed")
	// ErrRateLimited reports an admission limit before a durable nonce write.
	ErrRateLimited = errors.New("request admission rate limited")
	// ErrReplay is returned by the durable cache for an already claimed nonce.
	ErrReplay = errors.New("request nonce already used")
)

// Replay atomically inserts (key, nonce) until expires, returning ErrReplay if
// already present. Mutations require a durable commit before nil; endpoint policy
// may use a separate bounded volatile cache for reads without custody effects.
type Replay func(ctx context.Context, key, nonce string, expires int64) error

// Options binds verification to one public authority and injects admission policy.
type Options struct {
	PublicAuthority string
	Now             func() time.Time
	Replay          Replay
	TrustedProxies  []netip.Prefix
	// Authorize runs after signature verification and before the replay commit.
	Authorize func(context.Context, *http.Request, string) error
}

// Verifier checks signatures and endpoint admission before committing replay state.
type Verifier struct {
	now       func() time.Time
	replay    Replay
	proxies   []netip.Prefix
	authority string
	authorize func(context.Context, *http.Request, string) error
}

// NewVerifier requires an exact public authority and endpoint replay policy.
func NewVerifier(opts Options) (*Verifier, error) {
	if opts.Replay == nil {
		return nil, errors.New("request replay policy is required")
	}
	if !ValidAuthority(opts.PublicAuthority) {
		return nil, errors.New("canonical public authority is required")
	}
	for _, prefix := range opts.TrustedProxies {
		if !ValidTrustedProxyPrefix(prefix) {
			return nil, errors.New("trusted proxies require canonical private or loopback CIDRs")
		}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Verifier{now: opts.Now, replay: opts.Replay, proxies: append([]netip.Prefix(nil), opts.TrustedProxies...), authority: opts.PublicAuthority, authorize: opts.Authorize}, nil
}

// ValidRoutingKey accepts only canonical raw-base64url Ed25519 public keys.
// keyid is the public key itself; verification never fetches a remote key.
func ValidRoutingKey(key string) bool {
	if len(key) != 43 {
		return false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(key)
	return err == nil && base64.RawURLEncoding.EncodeToString(raw) == key && peercrypto.ValidSigningKey(raw)
}

// ValidateTransport enforces the same HTTPS and proxy boundary on the notify
// ticket lane, which has separate single-use authentication without a signature.
func (v *Verifier) ValidateTransport(r *http.Request) error {
	if v == nil {
		return ErrUnavailable
	}
	_, _, err := requestTarget(r, v.proxies, false, v.authority)
	return err
}

// Verify authenticates the exact supplied body, including an empty body. The
// caller must bound and completely read it before invoking this method.
func (v *Verifier) Verify(ctx context.Context, r *http.Request, body []byte) (string, error) {
	if v == nil || v.replay == nil {
		return "", ErrUnavailable
	}
	authority, uri, err := requestTarget(r, v.proxies, false, v.authority)
	if err != nil {
		return "", ErrUnauthorized
	}
	profile, err := readProfile(r)
	if err != nil || !fresh(profile.created, profile.expires, v.now().Unix()) {
		return "", ErrUnauthorized
	}
	digest := bodyDigest(body)
	if subtle.ConstantTimeCompare([]byte(digest), []byte(profile.digest)) != 1 {
		return "", ErrUnauthorized
	}
	public, err := base64.RawURLEncoding.Strict().DecodeString(profile.key)
	if err != nil || !ed25519.Verify(public, signatureBase(r.Method, authority, uri, digest, profile.params), profile.signature) {
		return "", ErrUnauthorized
	}
	if v.authorize != nil {
		if err := v.authorize(ctx, r, profile.key); err != nil {
			if errors.Is(err, ErrForbidden) || errors.Is(err, ErrRateLimited) {
				return "", err
			}
			return "", fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
	}
	// Retain beyond the complete accepted skew window and for at least 2 minutes.
	expires := max(profile.expires+60, v.now().Unix()+120)
	if err := v.replay(ctx, profile.key, profile.nonce, expires); err != nil {
		if errors.Is(err, ErrReplay) {
			return "", ErrUnauthorized
		}
		return "", fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	return profile.key, nil
}

// Sign sets the three canonical HTTP signature fields for one HTTPS request.
// nonce must encode 16..96 CSPRNG bytes in canonical unpadded base64url form.
func Sign(r *http.Request, body []byte, key ed25519.PrivateKey, created, expires time.Time, nonce string) error {
	if len(key) != ed25519.PrivateKeySize || !validNonce(nonce) || created.Unix() <= 0 || expires.Unix() <= created.Unix() || expires.Unix()-created.Unix() > 60 {
		return ErrUnauthorized
	}
	if subtle.ConstantTimeCompare(key, ed25519.NewKeyFromSeed(key.Seed())) != 1 {
		return ErrUnauthorized
	}
	kid := base64.RawURLEncoding.EncodeToString(key[ed25519.SeedSize:])
	if !ValidRoutingKey(kid) {
		return ErrUnauthorized
	}
	authority, uri, err := requestTarget(r, nil, true, "")
	if err != nil {
		return ErrUnauthorized
	}
	params := httpsfv.NewParams()
	params.Add("created", created.Unix())
	params.Add("expires", expires.Unix())
	params.Add("nonce", nonce)
	params.Add("keyid", kid)
	params.Add("alg", "ed25519")
	inner := httpsfv.InnerList{Params: params}
	for _, component := range components {
		inner.Items = append(inner.Items, httpsfv.NewItem(component))
	}
	serialized, err := httpsfv.Marshal(httpsfv.List{inner})
	if err != nil {
		return ErrUnauthorized
	}
	digest := bodyDigest(body)
	signature := ed25519.Sign(key, signatureBase(r.Method, authority, uri, digest, serialized))
	if r.Header == nil {
		r.Header = make(http.Header)
	}
	r.Header.Set("Content-Digest", digest)
	r.Header.Set("Signature-Input", "sig1="+serialized)
	r.Header.Set("Signature", "sig1=:"+base64.StdEncoding.EncodeToString(signature)+":")
	return nil
}

func fresh(created, expires, now int64) bool {
	return created > 0 && expires > created && expires-created <= 60 && created <= now+60 && created >= now-120 && expires >= now-60
}

func validNonce(nonce string) bool {
	if len(nonce) < 22 || len(nonce) > 128 {
		return false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(nonce)
	return err == nil && len(raw) >= 16 && base64.RawURLEncoding.EncodeToString(raw) == nonce
}

func bodyDigest(body []byte) string {
	digest := sha256.Sum256(body)
	return "sha-256=:" + base64.StdEncoding.EncodeToString(digest[:]) + ":"
}

func signatureBase(method, authority, uri, digest, params string) []byte {
	return []byte(fmt.Sprintf("\"@method\": %s\n\"@authority\": %s\n\"@target-uri\": %s\n\"content-digest\": %s\n\"@signature-params\": %s", method, authority, uri, digest, params))
}
