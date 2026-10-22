//go:build !darwin && !linux

package claude

import "os"

func responderBinaryFileStamp(os.FileInfo) (responderBinaryStamp, bool) {
	return responderBinaryStamp{}, false
}
