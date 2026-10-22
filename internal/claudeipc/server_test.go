package claudeipc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestBridgeRejectsUnauthenticatedBrowserAndOversize(t *testing.T) {
	b := &bridge{capability: strings.Repeat("a", 64)}
	for _, test := range []struct {
		auth, origin, host, body string
		want                     int
	}{
		{"", "", "127.0.0.1:1234", "{}", http.StatusForbidden},
		{"Bearer " + b.capability, "https://example.invalid", "127.0.0.1:1234", "{}", http.StatusForbidden},
		{"Bearer " + b.capability, "", "evil.invalid", "{}", http.StatusForbidden},
		{"Bearer " + b.capability, "", "127.0.0.1:1234", strings.Repeat("x", 128*1024+1), http.StatusBadRequest},
	} {
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:1234/result", strings.NewReader(test.body))
		r.Host = test.host
		r.Header.Set("Authorization", test.auth)
		r.Header.Set("Origin", test.origin)
		w := httptest.NewRecorder()
		b.ServeHTTP(w, r)
		if w.Code != test.want {
			t.Fatalf("got %d want %d", w.Code, test.want)
		}
		if strings.Contains(w.Body.String(), b.capability) {
			t.Fatal("capability leaked")
		}
	}
}

func TestBridgeNativeResultEvidenceAndUnknownOutcome(t *testing.T) {
	dir, err := shortRuntimeDirectory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if removeErr := os.RemoveAll(dir); removeErr != nil {
			t.Error(removeErr)
		}
	})
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	s, err := localipc.Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	requests := make(chan localipc.Request, 4)
	done := make(chan error, 1)
	go func() {
		done <- s.Serve(ctx, func(_ context.Context, r localipc.Request) (any, error) {
			requests <- r
			return map[string]bool{"ok": true}, nil
		})
	}()
	t.Cleanup(func() {
		cancel()
		if err = <-done; err != nil {
			t.Error(err)
		}
		if err = s.Close(); err != nil {
			t.Error(err)
		}
	})
	b := &bridge{capability: strings.Repeat("a", 64), instance: "instance", options: Options{Client: localipc.Client{RuntimeDir: dir}}}
	for _, test := range []struct{ result, outcome string }{
		{`{"text":"framed","origin":{"kind":"plugin","name":"peerturn"}}`, "accepted"},
		{`{"drop":"stopped"}`, "drop"},
		{`{"id":"fake"}`, "in_doubt"},
		{`{"text":"framed","origin":{"kind":"composer"}}`, "in_doubt"},
	} {
		body := `{"message_id":"message","attempt_id":"attempt","result":` + test.result + `}`
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:1234/result", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+b.capability)
		w := httptest.NewRecorder()
		b.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatal(w.Code, w.Body.String())
		}
		request := <-requests
		if request.Outcome != test.outcome {
			t.Fatal(request.Outcome, test.outcome)
		}
		if request.Outcome == "accepted" && len(request.Nonce) != 64 {
			t.Fatal("no content digest")
		}
		encoded, encodeErr := json.Marshal(request)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		if strings.Contains(string(encoded), "framed") {
			t.Fatal("raw native result escaped helper")
		}
	}
}

// Darwin's native socket path limit is shorter than long Go test temp paths.
func shortRuntimeDirectory() (string, error) { return os.MkdirTemp("/tmp", "pt-m3-") }

func TestBridgeTwoPhaseNoticeAndReleaseContracts(t *testing.T) {
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "/native/socket")
	requests := make(chan localipc.Request, 3)
	b := testBridge(t, func(_ context.Context, r localipc.Request) (any, error) {
		requests <- r
		return map[string]string{"notice_id": "notice-1", "context": "fixed notice"}, nil
	})
	for _, test := range []struct{ path, body, method string }{
		{"/owner-boundary", `{"session":"session-a"}`, "adapter.boundary"},
		{"/boundary-attached", `{"session":"session-a","notice_id":"notice-1"}`, "adapter.boundary-attached"},
		{"/release", `{"message_id":"message","attempt_id":"attempt"}`, "adapter.release"},
	} {
		w := bridgeRequest(b, http.MethodPost, test.path, test.body)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", test.path, w.Code, w.Body.String())
		}
		r := <-requests
		if r.Method != test.method || r.Instance != "instance" {
			t.Fatal(r)
		}
		if test.path != "/release" && (r.NativeSession != "session-a" || r.NativeSocket != "/native/socket") {
			t.Fatal("missing boundary binding", r)
		}
		if test.path == "/boundary-attached" && r.Nonce != "notice-1" {
			t.Fatal("missing attachment handle", r)
		}
		if test.path == "/release" && (r.MessageID != "message" || r.AttemptID != "attempt") {
			t.Fatal("missing attempt handle", r)
		}
	}
	w := bridgeRequest(b, http.MethodPost, "/owner-boundary", `{"session":"session-a","text":"private owner prompt"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatal("owner text accepted", w.Code)
	}
}

func TestBridgeCloseAndReplacedInstanceAlwaysExit(t *testing.T) {
	for _, path := range []string{"/close", "/poll"} {
		t.Run(path, func(t *testing.T) {
			b := testBridge(t, func(context.Context, localipc.Request) (any, error) {
				return nil, rebind()
			})
			closed := make(chan struct{}, 1)
			b.shutdown = func() { closed <- struct{}{} }
			method := http.MethodPost
			if path == "/poll" {
				method = http.MethodGet
			}
			w := bridgeRequest(b, method, path, "{}")
			if w.Code != http.StatusConflict {
				t.Fatal(w.Code)
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("obsolete helper remains running")
			}
		})
	}
}

func TestBridgeCloseExitsAfterOtherDaemonFailure(t *testing.T) {
	b := testBridge(t, func(context.Context, localipc.Request) (any, error) {
		return nil, localipc.NewError("timeout", "pending")
	})
	closed := make(chan struct{}, 1)
	b.shutdown = func() { closed <- struct{}{} }
	w := bridgeRequest(b, http.MethodPost, "/close", "{}")
	if w.Code != http.StatusConflict {
		t.Fatal(w.Code)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("failed daemon close kept helper alive")
	}
}

func TestBridgeInactivityRequiresAuthenticatedRequests(t *testing.T) {
	b := &bridge{capability: strings.Repeat("a", 64)}
	closed := false
	b.shutdown = func() { closed = true }
	b.startIdleTimeout(time.Hour)
	t.Cleanup(b.stopIdleTimeout)
	stale := time.Now().Add(-2 * time.Hour)
	b.lastAuthenticated = stale
	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:1234/poll", nil)
	b.ServeHTTP(httptest.NewRecorder(), r)
	if b.lastAuthenticated != stale {
		t.Fatal("unauthenticated request refreshed helper lifetime")
	}
	bridgeRequest(b, http.MethodGet, "/invalid", "")
	if !b.lastAuthenticated.After(stale) {
		t.Fatal("authenticated request did not refresh helper lifetime")
	}
	b.expireIdle()
	if closed {
		t.Fatal("active helper expired")
	}
	b.lastAuthenticated = stale
	b.expireIdle()
	if !closed {
		t.Fatal("orphan helper did not expire")
	}
}

func TestBridgeLostNativeIncarnationStopsBeforeDaemonCall(t *testing.T) {
	b := &bridge{capability: strings.Repeat("a", 64), nativeGuard: func(context.Context) error { return rebind() }}
	closed := make(chan struct{}, 1)
	b.shutdown = func() { closed <- struct{}{} }
	w := bridgeRequest(b, http.MethodGet, "/poll", "")
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "needs_rebind") {
		t.Fatal("native failure was not fenced", w.Code, w.Body.String())
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("lost native incarnation kept helper alive")
	}
}

func TestBridgeAuthenticatedMalformedCloseStillExits(t *testing.T) {
	b := &bridge{capability: strings.Repeat("a", 64)}
	closed := make(chan struct{}, 1)
	b.shutdown = func() { closed <- struct{}{} }
	w := bridgeRequest(b, http.MethodPost, "/close", "malformed")
	if w.Code != http.StatusBadRequest {
		t.Fatal(w.Code)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("malformed close kept helper alive")
	}
}

func bridgeRequest(b *bridge, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:1234"+path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+b.capability)
	w := httptest.NewRecorder()
	b.ServeHTTP(w, r)
	return w
}

func testBridge(t *testing.T, handler func(context.Context, localipc.Request) (any, error)) *bridge {
	t.Helper()
	dir, err := shortRuntimeDirectory()
	if err != nil {
		t.Fatal(err)
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	s, err := localipc.Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, handler) }()
	t.Cleanup(func() {
		cancel()
		if serveErr := <-done; serveErr != nil {
			t.Error(serveErr)
		}
		if closeErr := s.Close(); closeErr != nil {
			t.Error(closeErr)
		}
		if removeErr := os.RemoveAll(dir); removeErr != nil {
			t.Error(removeErr)
		}
	})
	return &bridge{instance: "instance", capability: strings.Repeat("a", 64), options: Options{Client: localipc.Client{RuntimeDir: dir}}}
}
