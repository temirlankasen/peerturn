package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/temirlankasen/peerturn/internal/adapters/codex"
	"github.com/temirlankasen/peerturn/internal/localipc"
)

func qualifiedSetupVersion(host string) string {
	if host == "codex" {
		return codex.SupportedVersion
	}
	return "2.1.306"
}

func setupRuntimeFailure(host, detected string) *localipc.Error {
	problem := localipc.NewError("unsupported_version", "unsupported")
	problem.Message = fmt.Sprintf("Qualified %s runtime: %s; detected: %s. Install/select the qualified runtime on PATH, then rerun peerturn setup %s. Resume the same selected session with that runtime.", host, qualifiedSetupVersion(host), detected, host)
	return problem
}

func checkSetupRuntime(ctx context.Context, host string, invoke setupInvoke) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var output setupOutput
	detected := "unknown"
	if err := invoke(ctx, host, []string{"--version"}, &output); err == nil {
		text := strings.TrimSpace(output.String())
		if host == "claude" {
			text = strings.TrimSuffix(text, " (Claude Code)")
		} else {
			text = strings.TrimPrefix(text, "codex-cli ")
		}
		// Only a bounded numeric version can cross the diagnostic boundary.
		parts := strings.Split(text, ".")
		if len(text) <= 32 && len(parts) == 3 && parts[0] != "" && parts[1] != "" && parts[2] != "" && strings.Trim(text, "0123456789.") == "" {
			detected = text
		}
	}
	if detected != qualifiedSetupVersion(host) {
		return setupRuntimeFailure(host, detected)
	}
	return nil
}
