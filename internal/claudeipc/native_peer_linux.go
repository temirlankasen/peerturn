//go:build linux

package claudeipc

import (
	"os"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func nativePeerCredentials(fd int) (int, uint32, error) {
	cred, err := unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return 0, 0, err
	}
	return int(cred.Pid), cred.Uid, nil
}

func processIdentity(pid int) (nativeProcess, error) {
	if pid <= 0 {
		return nativeProcess{}, rebind()
	}
	// Reads stay inside /proc even if the numeric name were ever malformed;
	// the peer process identity must never be read from another tree.
	proc, err := os.OpenRoot("/proc")
	if err != nil {
		return nativeProcess{}, rebind()
	}
	name := strconv.Itoa(pid)
	info, statErr := proc.Stat(name)
	data, readErr := proc.ReadFile(name + "/stat")
	if closeErr := proc.Close(); statErr != nil || readErr != nil || closeErr != nil || len(data) > 4096 {
		return nativeProcess{}, rebind()
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nativeProcess{}, rebind()
	}
	// The executable name is parenthesized and can itself contain whitespace or
	// parentheses. Field 22 is starttime; fields after the last ')' start at 3.
	end := strings.LastIndexByte(string(data), ')')
	if end < 0 {
		return nativeProcess{}, rebind()
	}
	fields := strings.Fields(string(data[end+1:]))
	if len(fields) < 20 {
		return nativeProcess{}, rebind()
	}
	started, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil || started == 0 {
		return nativeProcess{}, rebind()
	}
	return nativeProcess{pid: pid, uid: owner.Uid, started: started}, nil
}
