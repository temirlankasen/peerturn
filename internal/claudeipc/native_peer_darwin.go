//go:build darwin

package claudeipc

import "golang.org/x/sys/unix"

func nativePeerCredentials(fd int) (int, uint32, error) {
	pid, err := unix.GetsockoptInt(fd, unix.SOL_LOCAL, unix.LOCAL_PEERPID)
	if err != nil {
		return 0, 0, err
	}
	cred, err := unix.GetsockoptXucred(fd, unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	if err != nil {
		return 0, 0, err
	}
	return pid, cred.Uid, nil
}

func processIdentity(pid int) (nativeProcess, error) {
	process, _, err := processWithParent(pid)
	return process, err
}

func processWithParent(pid int) (nativeProcess, int, error) {
	if pid <= 0 {
		return nativeProcess{}, 0, rebind()
	}
	process, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || int(process.Proc.P_pid) != pid || process.Proc.P_starttime.Sec <= 0 || process.Proc.P_starttime.Usec < 0 {
		return nativeProcess{}, 0, rebind()
	}
	started := uint64(process.Proc.P_starttime.Sec)*1_000_000 + uint64(process.Proc.P_starttime.Usec)
	return nativeProcess{pid: pid, uid: process.Eproc.Ucred.Uid, started: started}, int(process.Eproc.Ppid), nil
}
