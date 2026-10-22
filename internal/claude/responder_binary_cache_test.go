package claude

import (
	"io"
	"os"
	"sync"
	"testing"
)

func TestResponderBinaryHashReusesUnchangedImage(t *testing.T) {
	options, evidence := configurationFixture(t)
	file, err := openResponderFile(options.Executable)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	hash, err := configurationBinaryFileHash(file)
	if err != nil || hash != evidence.BinaryHash {
		t.Fatal("cached image fingerprint changed", err)
	}
	offset, err := file.Seek(0, io.SeekCurrent)
	if err != nil || offset != 0 {
		t.Fatal("unchanged executable was read again", err)
	}
}

func TestResponderBinaryHashInvalidatesSameSizePreservedMtime(t *testing.T) {
	options, evidence := configurationFixture(t)
	info, err := os.Stat(options.Executable)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(options.Executable, []byte("changed-native-binary"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(options.Executable, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	hash, err := configurationBinaryHash(options.Executable)
	if err != nil || hash == evidence.BinaryHash {
		t.Fatal("binary mutation hidden by preserved size and mtime", err)
	}
}

func TestResponderBinaryHashConcurrentQualification(t *testing.T) {
	options, evidence := configurationFixture(t)
	var group sync.WaitGroup
	for range 16 {
		group.Go(func() {
			hash, err := configurationBinaryHash(options.Executable)
			if err != nil || hash != evidence.BinaryHash {
				t.Error("concurrent image fingerprint changed", err)
			}
		})
	}
	group.Wait()
}
