//go:build !darwin && !linux

package claude

import "os/exec"

// Unsupported process supervision is unavailable rather than a weaker fallback.
func configureResponderProcess(_ *exec.Cmd) bool { return false }
