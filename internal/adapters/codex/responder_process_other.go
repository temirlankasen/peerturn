//go:build !darwin && !linux

package codex

import "os/exec"

func isolateResponderProcess(_ *exec.Cmd) error {
	return &ResponderError{Reason: "sandbox_unavailable"}
}
func stopResponderProcess(_ int) error { return nil }
