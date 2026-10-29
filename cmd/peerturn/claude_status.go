package main

import (
	"errors"
	"io"
	"os"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func writeClaudeFailure(stderr io.Writer, err error) int {
	code := "adapter_unavailable"
	switch localipc.PublicError(err).Code {
	case "unsupported_version":
		code = "runtime_unsupported"
	case "runtime_version_unknown", "runtime_contract_changed":
		code = localipc.PublicError(err).Code
	case "unavailable", "runtime_unavailable", "timeout", "store_unavailable", "storage_unavailable":
		if _, ok := errors.AsType[*localipc.Error](err); ok {
			code = "daemon_unavailable"
		}
	case "unbound", "binding_error", "binding_mismatch", "needs_rebind":
		code = "needs_rebind"
	case "unsafe_runtime", "scope_denied":
		code = "permission_denied"
	case "uid_mismatch", "needs_verify":
		code = "peer_identity_failed"
	case "auth_failed":
		code = "auth_failed"
	}
	if errors.Is(err, os.ErrPermission) {
		code = "permission_denied"
	}
	// The mod accepts only this fixed vocabulary, never error text or metadata.
	if _, writeErr := io.WriteString(stderr, "peerturn: "+code+"\n"); writeErr != nil {
		return 1
	}
	return localipc.ExitCode(err)
}
