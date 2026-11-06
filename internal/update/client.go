package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
)

const (
	connectTimeout = 15 * time.Second
	overallTimeout = 10 * time.Minute
	maxRedirects   = 5
	userAgent      = "peerturn-update"
	maxTailRunes   = 240
)

// NewClient returns the HTTP client for release lookups and downloads: HTTPS
// only (also across redirects), 15 s to connect and to receive headers, and
// 10 minutes in total.
func NewClient() *http.Client {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: connectTimeout}).DialContext,
		TLSHandshakeTimeout:   connectTimeout,
		ResponseHeaderTimeout: connectTimeout,
		ForceAttemptHTTP2:     true,
	}
	return &http.Client{Transport: transport, Timeout: overallTimeout, CheckRedirect: checkRedirect}
}

// checkRedirect keeps a redirect from downgrading to plain HTTP, which would
// let a network attacker swap the unsigned parts of a download.
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return errors.New("too many redirects")
	}
	if req.URL.Scheme != "https" {
		return errors.New("refusing a redirect to a non-HTTPS address")
	}
	return nil
}

type statusError struct {
	status int
	target string
}

func (e *statusError) Error() string {
	text := fmt.Sprintf("%s answered HTTP %d", e.target, e.status)
	if e.status == http.StatusForbidden || e.status == http.StatusTooManyRequests {
		// Anonymous GitHub API access is limited per address; waiting is the fix.
		text += " (probably GitHub's request limit; retry in a few minutes)"
	}
	return text
}

// get downloads one HTTPS resource of at most limit bytes.
func get(ctx context.Context, client *http.Client, rawURL string, limit int64, accept string) ([]byte, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return nil, fmt.Errorf("refusing %q: only plain https addresses are allowed", shorten(rawURL))
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid address %q: %w", shorten(rawURL), err)
	}
	request.Header.Set("User-Agent", userAgent)
	if accept != "" {
		request.Header.Set("Accept", accept)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("could not fetch %s: %w", parsed.Host+parsed.Path, err)
	}
	body, readErr := readBounded(response, limit, parsed.Host+parsed.Path)
	closeErr := response.Body.Close()
	if readErr != nil {
		return nil, fmt.Errorf("%s: %w", parsed.Host+parsed.Path, readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("could not finish reading %s: %w", parsed.Path, closeErr)
	}
	return body, nil
}

func readBounded(response *http.Response, limit int64, target string) ([]byte, error) {
	// A client without a redirect policy could still have been sent to plain HTTP.
	if response.Request != nil && response.Request.URL.Scheme != "https" {
		return nil, errors.New("the final address is not HTTPS")
	}
	if response.StatusCode != http.StatusOK {
		return nil, &statusError{status: response.StatusCode, target: target}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("download interrupted: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("download is too large (over %d bytes)", limit)
	}
	return body, nil
}

// Tail returns the last non-empty line of tool output with control characters
// removed and the length bounded, so foreign output is safe to show in a message.
func Tail(output []byte, maxRunes int) string {
	lines := strings.Split(strings.TrimRight(string(output), "\r\n \t"), "\n")
	line := strings.TrimSpace(lines[len(lines)-1])
	line = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, line)
	if runes := []rune(line); len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "…"
	}
	return line
}

func shorten(text string) string { return Tail([]byte(text), maxTailRunes) }
