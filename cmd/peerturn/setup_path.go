package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func resolveSetupRoot(path string) (string, error) {
	if path == "" || len(path) > 4096 || strings.ContainsAny(path, "\x00\r\n") {
		return "", setupPathFailure()
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", setupPathFailure()
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", setupPathFailure()
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return "", setupPathFailure()
	}
	return canonical, nil
}

func setupPathFailure() *localipc.Error {
	problem := localipc.NewError("setup_path_invalid", "needs_configuration")
	problem.Message = "The release package directory could not be resolved. Select an existing accessible directory with --path, repair any broken symbolic links, and rerun peerturn setup with the same trusted --sha256."
	return problem
}

func setupMarketplaceFailure(host string) *localipc.Error {
	problem := localipc.NewError("marketplace_conflict", "needs_configuration")
	problem.Message = fmt.Sprintf("Inspect %s plugin marketplace list --json. The peerturn marketplace must report the same existing local release directory as --path, after resolving symbolic links. Resolve any conflicting or unobservable entry, then rerun peerturn setup %s with the same verified package options.", host, host)
	return problem
}
