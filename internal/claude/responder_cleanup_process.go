package claude

import (
	"io"
	"io/fs"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// ResponderForkActive checks for a same-user Linux process with the exact
// reserved --session-id before recovery deletes its artifacts. Errors prohibit
// cleanup; this observation is not a process lock or a permission to kill it.
func ResponderForkActive(child string) (bool, error) {
	if runtime.GOOS != "linux" || !validNativeSessionID(child) {
		return false, &ResponderError{Code: ForkCleanupFailed}
	}
	return responderForkActiveIn(os.DirFS("/proc"), child, responderForkProcessOwner)
}

func responderForkActiveIn(processes fs.FS, child string, ownProcess func(fs.FileInfo) (bool, error)) (bool, error) {
	failure := &ResponderError{Code: ForkCleanupFailed}
	entries, err := fs.ReadDir(processes, ".")
	if err != nil {
		return false, failure
	}
	for _, entry := range entries {
		pid, parseErr := strconv.Atoi(entry.Name())
		if parseErr != nil || pid <= 0 || !entry.IsDir() {
			continue
		}
		info, statErr := fs.Stat(processes, entry.Name())
		if os.IsNotExist(statErr) {
			continue
		}
		if statErr != nil {
			return false, failure
		}
		owned, ownerErr := ownProcess(info)
		if ownerErr != nil {
			return false, failure
		}
		if !owned {
			continue
		}
		arguments, readErr := responderForkArguments(processes, entry.Name()+"/cmdline")
		if os.IsNotExist(readErr) {
			continue
		}
		if readErr != nil {
			return false, failure
		}
		for index, argument := range arguments {
			if argument == "--session-id="+child || argument == "--session-id" && index+1 < len(arguments) && arguments[index+1] == child {
				return true, nil
			}
		}
	}
	return false, nil
}

func responderForkArguments(processes fs.FS, path string) ([]string, error) {
	file, err := processes.Open(path)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(file, (32<<10)+1))
	closeErr := file.Close()
	if err != nil || closeErr != nil || len(data) > 32<<10 || len(data) > 0 && data[len(data)-1] != 0 {
		return nil, &ResponderError{Code: ForkCleanupFailed}
	}
	// Linux zombies and kernel threads have no command line and cannot write a
	// native transcript. Disappearing processes are handled by the caller.
	if len(data) == 0 {
		return nil, nil
	}
	return strings.Split(string(data[:len(data)-1]), "\x00"), nil
}
