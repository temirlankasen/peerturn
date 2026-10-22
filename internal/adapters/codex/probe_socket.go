package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// DefaultSocketPath selects the current user's existing shared-daemon endpoint.
// It neither creates directories nor starts or recovers a daemon.
func DefaultSocketPath() (string, error) {
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", failure("unsupported")
		}
		home = filepath.Join(userHome, ".codex")
	}
	if !filepath.IsAbs(home) {
		return "", failure("invalid_input")
	}
	return filepath.Join(home, "app-server-control", "app-server-control.sock"), nil
}

func probeSocket(path string) (string, string, error) {
	if !filepath.IsAbs(path) || len(path) > 4096 {
		return "", "", failure("invalid_input")
	}
	// The pinned daemon advertises an alias to a socket in a protected directory.
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", "", failure("unsupported")
	}
	dir, err := os.Lstat(filepath.Dir(resolved))
	if err != nil || !dir.IsDir() || dir.Mode().Perm() != 0700 || !ownedSocketFile(dir) {
		return "", "", failure("unsupported")
	}
	info, err := os.Lstat(resolved)
	if err != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0600 || !ownedSocketFile(info) {
		return "", "", failure("unsupported")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", "", failure("unsupported")
	}
	identity := fmt.Sprintf("unix:%d:%d:%d", stat.Dev, stat.Ino, info.ModTime().UnixNano())
	return resolved, identity, nil
}

func ownedSocketFile(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int64(stat.Uid) == int64(os.Geteuid())
}
