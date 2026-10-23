package store

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
)

func TestTrustedPathAliasesAndMissingPackages(t *testing.T) {
	base := t.TempDir()
	for _, path := range []string{"package", "deep/package", "deep/child"} {
		if err := os.MkdirAll(filepath.Join(base, path), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(base, "package", "peerturn"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"alias": "package", "binary": "package/peerturn", "nested": "deep/child", "parent": "nested/../package", "loop": "loop"} {
		if err := os.Symlink(target, filepath.Join(base, name)); err != nil {
			t.Fatal(err)
		}
	}
	canonical, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	for name, suffix := range map[string]string{"alias": "package", "binary": "package/peerturn", "alias/removed/peerturn": "package/removed/peerturn", "parent": "deep/package"} {
		got, resolveErr := ResolveTrustedPath(filepath.Join(base, name))
		if want := filepath.Join(canonical, suffix); resolveErr != nil || got != want {
			t.Fatalf("alias %s: got=%s want=%s err=%v", name, got, want, resolveErr)
		}
	}
	if _, err = ResolveTrustedPath(filepath.Join(base, "loop")); !fault.IsCode(err, StorageUnavailable) {
		t.Fatalf("link loop did not fail closed: %v", err)
	}
}

type trustedPathInfo struct {
	ownerInfo
	mode os.FileMode
}

func (i trustedPathInfo) Mode() os.FileMode { return i.mode }

func TestTrustedLinkRequiresTrustedOwnerAndUnwritableParent(t *testing.T) {
	info, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("owner metadata unavailable")
	}
	current := stat.Uid
	for _, uid := range []uint32{0, current, current ^ 0x80000000} {
		for _, mode := range []os.FileMode{0700, 0755, 0777, 0777 | os.ModeSticky} {
			link := trustedPathInfo{ownerInfo{uid}, os.ModeSymlink | 0777}
			parent := trustedPathInfo{ownerInfo{current}, os.ModeDir | mode}
			want := (uid == 0 || uid == current) && mode.Perm()&0022 == 0
			if trustedPathLink(link, parent) != want {
				t.Fatalf("link trust ignored owner or parent permissions: uid=%d mode=%v", uid, mode)
			}
		}
	}
	link := trustedPathInfo{ownerInfo{current}, os.ModeSymlink | 0777}
	parent := trustedPathInfo{ownerInfo{current ^ 0x80000000}, os.ModeDir | 0700}
	if trustedPathLink(link, parent) {
		t.Fatal("foreign parent owner accepted")
	}
}
