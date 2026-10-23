package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSetupMCPRecognizesCanonicalAliases(t *testing.T) {
	config := setupMCPHome(t)
	home := filepath.Dir(config)
	packages := filepath.Join(home, ".local", "share", "peerturn")
	root := filepath.Join(packages, "0.2.0")
	previous := filepath.Join(packages, "0.1.0")
	for _, path := range []string{root, previous} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "peerturn"), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(t.TempDir(), "home-alias")
	if err := os.Symlink(home, alias); err != nil {
		t.Fatal(err)
	}
	aliasedPackages := filepath.Join(alias, ".local", "share", "peerturn")
	for _, selectedHome := range []string{home, alias} {
		t.Run(filepath.Base(selectedHome), func(t *testing.T) {
			t.Setenv("HOME", selectedHome)
			for _, version := range []string{"0.2.0", "0.1.0"} {
				t.Run(version, func(t *testing.T) {
					command := filepath.Join(aliasedPackages, version, "peerturn")
					err := checkSetupMCPCommand(command, root)
					if version == "0.2.0" && err != nil {
						t.Fatalf("same package alias refused: %v", err)
					}
					if version == "0.1.0" && !errors.Is(err, errSetupMCPUpgrade) {
						t.Fatalf("previous package under aliased home was not recognized: %v", err)
					}
				})
			}
		})
	}
}

func TestSetupMCPRejectsAliasWithWritableParent(t *testing.T) {
	setupMCPHome(t)
	root := t.TempDir()
	parent := t.TempDir()
	alias := filepath.Join(parent, "package-alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0777|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	if err := checkSetupMCPCommand(filepath.Join(alias, "peerturn"), root); err == nil {
		t.Fatal("MCP registration accepted an unsafe executable alias")
	}
}
