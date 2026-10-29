package relay

import (
	"bytes"
	"io"
	"net/http"
	"testing"
)

func TestInvitePageOnboardingWithoutRedemption(t *testing.T) {
	f := newFixture(t)
	r, err := http.NewRequestWithContext(t.Context(), "GET", f.h.URL+"/i/not-a-real-invite", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := f.h.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	page, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("invite page: read=%v close=%v status=%d", readErr, closeErr, response.StatusCode)
	}
	for name, want := range map[string]string{
		"Cache-Control": "no-store", "Referrer-Policy": "no-referrer",
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'",
	} {
		if got := response.Header.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	for _, instruction := range []string{"unauthenticated", "pinned tag", "official README", "signing identity", "verification key", "peer_join({})", "owner-prompt hook", "handle", "fragment", "coauthor", "strong trust", "provider"} {
		if !bytes.Contains(page, []byte(instruction)) {
			t.Errorf("missing instruction %q", instruction)
		}
	}
	for _, forbidden := range []string{"curl | sh", "--link URL", "<script", "not-a-real-invite", "no published release", "before publication", "local lab", "claude plugin", "codex plugin", "peerturn setup", "peerturn join", "<pre>", "--lab", "--verify-key", "PEERTURN_RELEASE_BASE_URL"} {
		if bytes.Contains(page, []byte(forbidden)) {
			t.Errorf("page contains %q", forbidden)
		}
	}
}
