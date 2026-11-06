package logfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Rotation keeps the newest lines readable and bounds the directory to the
// requested number of files, so a daemon that runs for months cannot fill
// the disk or lose the line an operator needs.
func TestWriterRotatesAndKeepsBoundedFiles(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "logs", "daemon.log")
	w, err := Open(path, 40, 2)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 10 {
		if _, err = w.Write([]byte(strings.Repeat(string(rune('a'+i)), 15) + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 3 {
		t.Fatalf("rotation kept %v, want the live file and two older ones", names)
	}
	for _, name := range names {
		data, readErr := root.ReadFile(name)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if len(data) > 40 {
			t.Fatalf("%s holds %d bytes, over the limit", name, len(data))
		}
	}
	live, err := root.ReadFile(filepath.Base(path))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(live), "jjjjj") {
		t.Fatalf("live file lost the newest line: %q", live)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("log file mode %v, want private", info.Mode().Perm())
	}
	if _, err = w.Write([]byte("x\n")); err == nil {
		t.Fatal("write after close succeeded")
	}
}

func TestOpenRejectsRelativePathAndBadBounds(t *testing.T) {
	t.Parallel()
	if _, err := Open("relative.log", 10, 1); err == nil {
		t.Fatal("relative path accepted")
	}
	if _, err := Open(filepath.Join(t.TempDir(), "a.log"), 0, 1); err == nil {
		t.Fatal("zero limit accepted")
	}
}
