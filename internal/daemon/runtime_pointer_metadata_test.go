package daemon

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

type runtimePointerFileInfo struct {
	os.FileInfo
	native any
}

func (i runtimePointerFileInfo) Sys() any { return i.native }

func TestRuntimePointerMetadataRejectsForeignOwner(t *testing.T) {
	_, _, pointer := runtimePointerFixture(t)
	info, err := os.Lstat(pointer)
	if err != nil {
		t.Fatal(err)
	}
	if !privateRuntimePointer(info) {
		t.Fatal("current owner metadata rejected")
	}
	native, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("missing Unix owner metadata")
	}
	foreign := *native
	foreign.Uid++
	if privateRuntimePointer(runtimePointerFileInfo{FileInfo: info, native: &foreign}) {
		t.Fatal("foreign UID metadata accepted")
	}
	if privateRuntimePointer(runtimePointerFileInfo{FileInfo: info}) {
		t.Fatal("missing owner metadata accepted")
	}
}

func TestRuntimePointerUsesOnlyConfiguredState(t *testing.T) {
	state, runtimeDir, original := runtimePointerFixture(t)
	configured := state + "-configured"
	if err := os.Mkdir(configured, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configured, "runtime.path"), []byte(runtimeDir+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := DefaultConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if err = WriteConfig(config, configured, "https://relay.example", ""); err != nil {
		t.Fatal(err)
	}
	sharedMode := os.FileMode(0644)
	if err = os.Chmod(original, sharedMode); err != nil {
		t.Fatal(err)
	}
	got, err := DefaultRuntimeDir()
	if err != nil || got != runtimeDir {
		t.Fatalf("runtime did not follow selected state: %q %v", got, err)
	}
	if err = os.Remove(filepath.Join(configured, "runtime.path")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", "")
	if _, err = DefaultRuntimeDir(); err == nil {
		t.Fatal("runtime lookup searched an unselected state directory")
	}
}

func TestRuntimePointerPublicationReplacesOnlySafePointer(t *testing.T) {
	state, runtimeDir, pointer := runtimePointerFixture(t)
	before, err := os.Lstat(pointer)
	if err != nil {
		t.Fatal(err)
	}
	published, err := publishRuntimePointer(state, runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := published.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	after, err := os.Lstat(pointer)
	if err != nil || os.SameFile(before, after) || !privateRuntimePointer(after) {
		t.Fatalf("publication did not atomically replace private inode: %v", err)
	}
	if err = published.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(filepath.Join(state, "target"), pointer); err != nil {
		t.Fatal(err)
	}
	if p, err := publishRuntimePointer(state, runtimeDir); err == nil {
		if err := p.Close(); err != nil {
			t.Error(err)
		}
		t.Fatal("daemon publication replaced a hostile pointer")
	}
}
