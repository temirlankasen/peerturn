package localipc

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type transportError struct {
	code   string
	status int
}

func (e *transportError) Error() string      { return "secret response contents" }
func (e *transportError) PublicCode() string { return e.code }
func (e *transportError) PublicStatus() int  { return e.status }

func TestPublicErrorCategoriesAndSanitization(t *testing.T) {
	for _, test := range []struct {
		err  error
		code string
		exit int
	}{
		{&transportError{"unauthorized", 401}, "auth_failed", 5},
		{&transportError{"forbidden", 403}, "scope_denied", 2},
		{&transportError{"conflict", 409}, "conflict", 2},
		{&transportError{"gone", 410}, "expired", 2},
		{&transportError{"expired_message", 410}, "expired", 2},
		{&transportError{"expired_invite", 410}, "expired", 2},
		{&transportError{"forbidden", 410}, "scope_denied", 2},
		{&transportError{"unknown", 410}, "unavailable", 4},
		{&transportError{"unsupported_version", 426}, "unsupported_version", 6},
		{&transportError{"backpressure", 429}, "backpressure", 4},
		{&transportError{"storage_unavailable", 503}, "storage_unavailable", 4},
		{&transportError{"relay_key_mismatch", 0}, "relay_key_mismatch", 5},
		{&transportError{"secret response contents", 400}, "unavailable", 4},
		{errors.New("secret response contents"), "unavailable", 4},
		{context.DeadlineExceeded, "timeout", 4},
		{NewError("in_doubt", "in_doubt"), "in_doubt", 7},
		{NewError("store_unavailable", "pending"), "store_unavailable", 4},
		{NewError("unbound", "unbound"), "unbound", 3},
	} {
		public := PublicError(test.err)
		if public.Code != test.code || ExitCode(test.err) != test.exit || strings.Contains(public.Message, "secret") {
			t.Fatalf("public=%#v exit=%d", public, ExitCode(test.err))
		}
	}
}
