//go:build darwin || linux

package claude

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func configureResponderProcess(command *exec.Cmd) bool {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.WaitDelay = time.Second
	command.Cancel = func() error {
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	return true
}
