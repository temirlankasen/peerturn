//go:build darwin

package localipc

import "golang.org/x/sys/unix"

func socketPID(fd int) (int, error) {
	return unix.GetsockoptInt(fd, unix.SOL_LOCAL, unix.LOCAL_PEERPID)
}

func socketUID(fd int) (uint32, error) {
	cred, err := unix.GetsockoptXucred(fd, unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	if err != nil {
		return 0, err
	}
	return cred.Uid, nil
}
