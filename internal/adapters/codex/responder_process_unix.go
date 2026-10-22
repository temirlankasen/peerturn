//go:build darwin || linux

package codex

import (
	"errors"
	"os/exec"
	"syscall"
)

func isolateResponderProcess(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}

func stopResponderProcess(processID int) error {
	if processID <= 0 {
		return &ResponderError{Reason: "responder_failed"}
	}
	if err := syscall.Kill(-processID, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}
