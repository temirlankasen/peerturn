package claude

import (
	"os"
	"strconv"
	"syscall"
	"time"
)

func responderBinaryFileStamp(info os.FileInfo) (responderBinaryStamp, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return responderBinaryStamp{}, false
	}
	return responderBinaryStamp{device: strconv.FormatUint(stat.Dev, 10), inode: stat.Ino, size: info.Size(),
		modified: info.ModTime(), changed: time.Unix(stat.Ctim.Sec, stat.Ctim.Nsec), mode: info.Mode()}, true
}
