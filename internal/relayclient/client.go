package relayclient

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"time"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/relay/auth"
)

const maxResponseBytes = 2 << 20

// Client signs each operation afresh and never automatically retries a mutation.
type Client struct {
	base               *url.URL
	key                ed25519.PrivateKey
	http               *http.Client
	expectedKey        string
	consistencyMu      sync.Mutex
	consistencyChecked bool
}

// New accepts one canonical HTTPS origin and a consistent routing private key.
// A supplied HTTP client must use a standard Transport with verified TLS.
func New(opts Options) (*Client, error) {
	u, err := url.Parse(opts.URL)
	if err != nil || u.Scheme != "https" || !auth.ValidAuthority(u.Host) || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.Path != "" && u.Path != "/" || u.RawPath != "" {
		return nil, errors.New("relay requires a canonical HTTPS origin")
	}
	if len(opts.Key) != ed25519.PrivateKeySize || subtle.ConstantTimeCompare(opts.Key, ed25519.NewKeyFromSeed(opts.Key.Seed())) != 1 || !auth.ValidRoutingKey(base64.RawURLEncoding.EncodeToString(opts.Key[ed25519.SeedSize:])) {
		return nil, errors.New("relay requires a consistent Ed25519 routing key")
	}
	if opts.ExpectedRelayKey != "" && !auth.ValidRoutingKey(opts.ExpectedRelayKey) {
		return nil, errors.New("expected relay key must be a canonical Ed25519 public key")
	}
	hc, err := httpClient(opts.HTTPClient, u.Hostname())
	if err != nil {
		return nil, err
	}
	u.Path = ""
	return &Client{base: u, key: append(ed25519.PrivateKey(nil), opts.Key...), http: hc, expectedKey: opts.ExpectedRelayKey}, nil
}

func httpClient(input *http.Client, hostname string) (*http.Client, error) {
	c := http.Client{Timeout: 40 * time.Second}
	if input != nil {
		c = *input
	}
	if c.Timeout <= 0 || c.Timeout > 40*time.Second {
		c.Timeout = 40 * time.Second
	}
	transport := c.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	t, ok := transport.(*http.Transport)
	if !ok || t == nil || t.DialTLSContext != nil {
		return nil, errors.New("relay requires a standard verified TLS transport")
	}
	// Copy supported connection settings into a fresh transport, excluding legacy
	// TLS dial hooks that can return an unverified connection.
	t = &http.Transport{
		Proxy: t.Proxy, DialContext: t.DialContext, ForceAttemptHTTP2: t.ForceAttemptHTTP2,
		TLSClientConfig: t.TLSClientConfig.Clone(), TLSHandshakeTimeout: t.TLSHandshakeTimeout,
		DisableKeepAlives: t.DisableKeepAlives, DisableCompression: t.DisableCompression,
		MaxIdleConns: t.MaxIdleConns, MaxIdleConnsPerHost: t.MaxIdleConnsPerHost,
		MaxConnsPerHost: t.MaxConnsPerHost, IdleConnTimeout: t.IdleConnTimeout,
		ResponseHeaderTimeout: t.ResponseHeaderTimeout, ExpectContinueTimeout: t.ExpectContinueTimeout,
		MaxResponseHeaderBytes: 16 << 10,
	}
	if t.TLSClientConfig == nil {
		t.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	if t.TLSClientConfig.InsecureSkipVerify || t.TLSClientConfig.MaxVersion != 0 && t.TLSClientConfig.MaxVersion < tls.VersionTLS12 {
		return nil, errors.New("relay TLS verification cannot be disabled or downgraded")
	}
	t.TLSClientConfig.MinVersion = max(tls.VersionTLS12, t.TLSClientConfig.MinVersion)
	t.TLSClientConfig.ServerName = hostname
	c.Transport = t
	c.Jar = nil
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &c, nil
}

func (c *Client) request(ctx context.Context, method, path string, query url.Values, body any, signed bool) (*http.Request, error) {
	var data []byte
	if body != nil {
		var err error
		data, err = codec.Canonical(body)
		if err != nil || len(data) > protocol.MaxPacket {
			return nil, invalid()
		}
	}
	u := *c.base
	u.Path, u.RawQuery = path, query.Encode()
	r, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(data))
	if err != nil {
		return nil, invalid()
	}
	r.Header.Set("Accept", "application/json")
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if signed {
		nonce := make([]byte, 32)
		if _, err = rand.Read(nonce); err != nil {
			return nil, &Error{Code: "signing_unavailable"}
		}
		now := time.Now()
		if err = auth.Sign(r, data, c.key, now, now.Add(60*time.Second), base64.RawURLEncoding.EncodeToString(nonce)); err != nil {
			return nil, &Error{Code: "signing_unavailable"}
		}
	}
	return r, nil
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, result any, status int) error {
	if err := c.checkConsistency(ctx); err != nil {
		return err
	}
	return c.exchange(ctx, method, path, query, body, result, status, true)
}

func (c *Client) exchange(ctx context.Context, method, path string, query url.Values, body, result any, status int, signed bool) error {
	r, err := c.request(ctx, method, path, query, body, signed)
	if err != nil {
		return err
	}
	response, err := c.http.Do(r)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &Error{Code: "transport_unavailable"}
	}
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil {
			return
		}
	}()
	if response.StatusCode != status {
		return responseError(response)
	}
	media, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return malformed()
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || codec.Decode(data, maxResponseBytes, result) != nil {
		return malformed()
	}
	return nil
}

func (c *Client) checkConsistency(ctx context.Context) error {
	if c.expectedKey == "" {
		return nil
	}
	c.consistencyMu.Lock()
	defer c.consistencyMu.Unlock()
	if c.consistencyChecked {
		return nil
	}
	if _, err := c.Discovery(ctx); err != nil {
		return err
	}
	c.consistencyChecked = true
	return nil
}

// Discovery compares the optional expected key with unsigned advertised metadata.
// This does not prove relay-key possession; relay authentication uses TLS/WebPKI.
func (c *Client) Discovery(ctx context.Context) (Discovery, error) {
	var response struct {
		Discovery
		AdmissionMode json.RawMessage `json:"admission_mode"`
	}
	if err := c.exchange(ctx, "GET", "/.well-known/peerturn", nil, nil, &response, 200, false); err != nil {
		return Discovery{}, err
	}
	d := response.Discovery
	// Raw presence keeps an omitted legacy hint distinct from empty or null.
	if response.AdmissionMode != nil {
		if err := codec.Decode(response.AdmissionMode, maxResponseBytes, &d.AdmissionMode); err != nil || d.AdmissionMode != AdmissionSelfService {
			return Discovery{}, malformed()
		}
	}
	if !auth.ValidRoutingKey(d.RelayKey) || d.BackupEpoch <= 0 || len(d.WireVersions) == 0 || len(d.WireVersions) > 16 || d.Limits.CiphertextBytes <= 0 || d.Limits.BatchItems <= 0 || d.Limits.MailboxItems <= 0 || d.Limits.MailboxBytes <= 0 || d.Limits.LongPollSeconds < 0 {
		return Discovery{}, malformed()
	}
	if c.expectedKey != "" && subtle.ConstantTimeCompare([]byte(c.expectedKey), []byte(d.RelayKey)) != 1 {
		return Discovery{}, &Error{Code: "relay_key_mismatch"}
	}
	if !slices.Contains(d.WireVersions, protocol.WireVersion) {
		return Discovery{}, &Error{Code: "unsupported_version"}
	}
	return d, nil
}

func responseError(response *http.Response) error {
	result := &Error{Status: response.StatusCode, Code: "invalid_response"}
	media, _, mediaErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if mediaErr != nil || media != "application/json" {
		return result
	}
	var body struct {
		Error string `json:"error"`
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	if err == nil && codec.Decode(data, 4096, &body) == nil && knownCode(body.Error) {
		result.Code = body.Error
	}
	return result
}

func knownCode(code string) bool {
	switch code {
	case "not_found":
		return true
	case "invalid", "forbidden", "conflict", "gone", "expired_message", "quota", "storage_unavailable", "invalid_body", "invalid_client_address", "too_large", "rate_limit", "headers_too_large", "backpressure", "unavailable", "unauthorized", "invalid_query", "invalid_ticket", "insecure_transport", "stream_limit", "invalid_schema", "invalid_claim", "invalid_invite_proof", "claim_reader_mismatch", "expired_invite", "unsupported_version", "enrollment_denied", "destination_mismatch", "invalid_cursor", "invalid_expiry", "invalid_limit", "invalid_wait", "poll_limit", "poll_backpressure", "request_cancelled", "shutdown":
		return true
	}
	return false
}

func invalid() error   { return &Error{Code: "invalid_request"} }
func malformed() error { return &Error{Code: "invalid_response"} }
