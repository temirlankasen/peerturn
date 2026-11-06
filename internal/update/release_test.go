package update_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/temirlankasen/peerturn/internal/update"
)

const (
	latestPath = "/repos/temirlankasen/peerturn/releases/latest"
	listPath   = "/repos/temirlankasen/peerturn/releases"
)

// apiServer answers the two GitHub endpoints Latest may use and counts requests.
func apiServer(t *testing.T, latest func(http.ResponseWriter), list func(http.ResponseWriter)) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Accept") != "application/vnd.github+json" {
			t.Errorf("Accept = %q", r.Header.Get("Accept"))
		}
		switch r.URL.Path {
		case latestPath:
			latest(w)
		case listPath:
			list(w)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server, &requests
}

func writeBody(body string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		if _, err := io.WriteString(w, body); err != nil {
			return // the client hung up; its own assertions report the outcome
		}
	}
}

func notFound(w http.ResponseWriter) { http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound) }

func TestLatestReadsTagName(t *testing.T) {
	t.Parallel()
	server, requests := apiServer(t, writeBody(`{"tag_name":"v0.2.0-preview","draft":false}`), notFound)
	release, err := update.Latest(t.Context(), server.Client(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if release.Tag != "v0.2.0-preview" || release.Version != "0.2.0-preview" || requests.Load() != 1 {
		t.Fatalf("release = %+v after %d requests", release, requests.Load())
	}
}

// Every PeerTurn release is a GitHub prerelease, which /releases/latest never returns.
func TestLatestFallsBackToReleaseListWhenLatestIsNotFound(t *testing.T) {
	t.Parallel()
	list := writeBody(`[
		{"tag_name":"v0.1.8-preview","draft":false,"prerelease":true},
		{"tag_name":"v0.1.10-preview","draft":false,"prerelease":true},
		{"tag_name":"v0.9.0-preview","draft":true,"prerelease":true},
		{"tag_name":"nightly","draft":false,"prerelease":true},
		{"tag_name":"v0.1.9-preview","draft":false,"prerelease":true}]`)
	server, _ := apiServer(t, notFound, list)
	release, err := update.Latest(t.Context(), server.Client(), server.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	if release.Tag != "v0.1.10-preview" || release.Version != "0.1.10-preview" {
		t.Fatalf("release = %+v, want the highest published tag", release)
	}
}

func TestLatestRejectsUnusableAnswers(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		latest func(http.ResponseWriter)
		list   func(http.ResponseWriter)
		want   string
	}{
		"empty list":       {notFound, writeBody(`[]`), "no published release"},
		"only drafts":      {notFound, writeBody(`[{"tag_name":"v0.2.0","draft":true}]`), "no published release"},
		"list failure":     {notFound, func(w http.ResponseWriter) { http.Error(w, "boom", http.StatusBadGateway) }, "502"},
		"rate limited":     {func(w http.ResponseWriter) { http.Error(w, "limit", http.StatusForbidden) }, notFound, "request limit"},
		"server error":     {func(w http.ResponseWriter) { http.Error(w, "boom", http.StatusInternalServerError) }, notFound, "500"},
		"invalid json":     {writeBody(`<html>`), notFound, "release"},
		"missing tag":      {writeBody(`{}`), notFound, "tag"},
		"traversal tag":    {writeBody(`{"tag_name":"v0.2.0/../../x"}`), notFound, "tag"},
		"tag without v":    {writeBody(`{"tag_name":"0.2.0"}`), notFound, "tag"},
		"non-version tag":  {writeBody(`{"tag_name":"vlatest"}`), notFound, "tag"},
		"oversized answer": {writeBody(`{"tag_name":"v0.2.0","pad":"` + strings.Repeat("x", 5<<20) + `"}`), notFound, "too large"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server, _ := apiServer(t, tt.latest, tt.list)
			_, err := update.Latest(t.Context(), server.Client(), server.URL)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestLatestRefusesPlainHTTP(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	plain := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	t.Cleanup(plain.Close)
	if _, err := update.Latest(t.Context(), plain.Client(), plain.URL); err == nil || hits.Load() != 0 {
		t.Fatalf("err = %v after %d requests, want a refusal before any request", err, hits.Load())
	}
}
