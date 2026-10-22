package localipc

import (
	"errors"
	"net"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func privateDirectory(dir string, create bool) (*os.File, error) {
	if create {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, NewError("unavailable", "unavailable")
		}
	}
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil, NewError("unavailable", "offline")
		}
		return nil, NewError("unsafe_runtime", "rejected")
	}
	file := os.NewFile(uintptr(fd), filepath.Clean(dir))
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || int64(stat.Uid) != int64(os.Getuid()) || stat.Mode&0o777 != 0o700 {
		if closeErr := file.Close(); closeErr != nil {
			return nil, NewError("unavailable", "unavailable")
		}
		return nil, NewError("unsafe_runtime", "rejected")
	}
	return file, nil
}

func lockDirectory(dir *os.File, name string) (*os.File, error) {
	fd, err := unix.Openat(int(dir.Fd()), name, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, NewError("unsafe_runtime", "rejected")
	}
	file := os.NewFile(uintptr(fd), name)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || int64(stat.Uid) != int64(os.Getuid()) || stat.Mode&0o777 != 0o600 || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1 {
		if closeErr := file.Close(); closeErr != nil {
			return nil, NewError("unavailable", "unavailable")
		}
		return nil, NewError("unsafe_runtime", "rejected")
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if closeErr := file.Close(); closeErr != nil {
			return nil, NewError("unavailable", "unavailable")
		}
		return nil, NewError("already_running", "running")
	}
	return file, nil
}

func verifyUID(conn *net.UnixConn, expected int) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return NewError("uid_mismatch", "rejected")
	}
	var uid uint32
	var credErr error
	if err := raw.Control(func(fd uintptr) { uid, credErr = socketUID(int(fd)) }); err != nil || credErr != nil || int64(uid) != int64(expected) {
		return NewError("uid_mismatch", "rejected")
	}
	return nil
}

func privateSocket(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	var native unix.Stat_t
	if err := unix.Lstat(path, &native); err != nil || int64(native.Uid) != int64(os.Getuid()) {
		return nil, NewError("unsafe_runtime", "rejected")
	}
	if info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0o600 {
		return nil, NewError("unsafe_runtime", "rejected")
	}
	return info, nil
}
