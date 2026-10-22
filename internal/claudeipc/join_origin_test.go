package claudeipc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/invite"
	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestClaudeBoundaryReducesOwnerLinksToHashes(t *testing.T) {
	dir, err := shortRuntimeDirectory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if removeErr := os.RemoveAll(dir); removeErr != nil {
			t.Error(removeErr)
		}
	})
	s, err := localipc.Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	requests := make(chan localipc.Request, 1)
	done := make(chan error, 1)
	go func() {
		done <- s.Serve(ctx, func(_ context.Context, r localipc.Request) (any, error) {
			requests <- r
			return map[string]string{}, nil
		})
	}()
	t.Cleanup(func() {
		cancel()
		if serveErr := <-done; serveErr != nil {
			t.Error(serveErr)
		}
		if closeErr := s.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	b := &bridge{capability: strings.Repeat("a", 64), instance: "helper", options: Options{Client: localipc.Client{RuntimeDir: dir}}}
	link := "https://relay.example/i/AAAAAAAAAAAAAAAAAAAAAA#v=1&ik=" + strings.Repeat("A", 43) + "&ek=" + strings.Repeat("A", 43) + "&fp=" + strings.Repeat("a", 64) + "&mh=" + strings.Repeat("b", 64)
	body, err := json.Marshal(map[string]any{"session": "native", "invitation_links": []string{link}})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:1234/owner-boundary", strings.NewReader(string(body)))
	r.Header.Set("Authorization", "Bearer "+b.capability)
	w := httptest.NewRecorder()
	b.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatal(w.Code)
	}
	request := <-requests
	want := invite.PromptHashes(link)
	if request.Method != "adapter.boundary" || request.Instance != "helper" || request.NativeSession != "native" || len(request.OwnerInvites) != 1 || request.OwnerInvites[0] != want[0] || request.Body != "" || request.Link != "" {
		t.Fatal("owner link or provenance lost at the private helper boundary")
	}
}
