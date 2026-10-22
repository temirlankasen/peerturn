package claude

import (
	"crypto/rand"
	"encoding/hex"
	"io/fs"
	"os"
	"strings"
)

// ForkCleanupFailed suppresses an answer when private fork artifacts remain.
const ForkCleanupFailed ResponderFailure = "fork_cleanup_failed"

func newResponderSessionID() string {
	var data [16]byte
	rand.Read(data[:])
	data[6] = data[6]&0x0f | 0x40
	data[8] = data[8]&0x3f | 0x80
	value := hex.EncodeToString(data[:])
	return value[:8] + "-" + value[8:12] + "-" + value[12:16] + "-" + value[16:20] + "-" + value[20:]
}

// CleanupResponderArtifacts removes only a distinct local fork UUID's artifacts
// after its process has finished. Recovery callers must establish that no child
// process is still writing. The bound parent's history is never rewritten.
func CleanupResponderArtifacts(options ResponderOptions, parent, child string) (cleanupErr error) {
	failure := &ResponderError{Code: ForkCleanupFailed}
	if !validNativeSessionID(parent) || !validNativeSessionID(child) || parent == child ||
		!validResponderPath(responderConfigDirectory(options)) {
		return failure
	}
	root, err := os.OpenRoot(responderConfigDirectory(options))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return failure
	}
	defer func() {
		if root.Close() != nil {
			cleanupErr = failure
		}
	}()
	paths := []string{"debug/" + child + ".txt", "tasks/" + child, "session-env/" + child, "file-history/" + child}
	failed := false
	projects, err := responderArtifactEntries(root, "projects")
	if err != nil {
		failed = true
	}
	for _, project := range projects {
		if project.IsDir() || project.Type()&os.ModeSymlink != 0 {
			base := "projects/" + project.Name() + "/" + child
			paths = append(paths, base+".jsonl", base)
		}
	}
	todos, err := responderArtifactEntries(root, "todos")
	if err != nil {
		failed = true
	}
	for _, todo := range todos {
		if strings.HasPrefix(todo.Name(), child+"-") && strings.HasSuffix(todo.Name(), ".json") {
			paths = append(paths, "todos/"+todo.Name())
		}
	}
	for _, path := range paths {
		if root.RemoveAll(path) != nil {
			failed = true
		}
	}
	if failed {
		return failure
	}
	return nil
}

func responderArtifactEntries(root *os.Root, directory string) ([]fs.DirEntry, error) {
	entries, err := fs.ReadDir(root.FS(), directory)
	if os.IsNotExist(err) {
		return nil, nil
	}
	return entries, err
}
