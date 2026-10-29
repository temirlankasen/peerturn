package claudeipc

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/runtimepolicy"
)

func TestClaudeHelperRefusesVersionBeforeNativeAccess(t *testing.T) {
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "/not/a/native/socket")
	for _, tc := range []struct{ version, code string }{
		{"2.1.305", "unsupported_version"},
		{"private-marker", "runtime_version_unknown"},
		{"2.1.308", ""},
	} {
		err := Serve(t.Context(), io.Discard, Options{RuntimeVersion: tc.version})
		if runtimepolicy.ErrorCode(err) != tc.code {
			t.Fatalf("%s: %v", tc.version, err)
		}
		if strings.Contains(err.Error(), "private-marker") {
			t.Fatal("raw version leaked")
		}
	}
}

func TestClaudeBridgeContractFailureAllowlist(t *testing.T) {
	requests := make(chan localipc.Request, 1)
	b := testBridge(t, func(_ context.Context, r localipc.Request) (any, error) {
		requests <- r
		return map[string]bool{"recorded": true}, nil
	})
	result := bridgeRequest(b, http.MethodPost, "/contract", `{"contract_id":"claude.lifecycle"}`)
	if result.Code != http.StatusOK {
		t.Fatalf("contract rejected: %d %s", result.Code, result.Body.String())
	}
	r := <-requests
	if r.Method != "adapter.contract" || r.Kind != "claude.lifecycle" || r.Instance != "instance" {
		t.Fatal("contract lost exact helper incarnation", r)
	}
	result = bridgeRequest(b, http.MethodPost, "/contract", `{"contract_id":"private-marker"}`)
	if result.Code != http.StatusBadRequest || strings.Contains(result.Body.String(), "private-marker") {
		t.Fatal("unbounded contract became public", result.Code, result.Body.String())
	}
}
