package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
)

// These are observations exposed by the pinned hosts, not attestations about
// their executing runtime. Unknown output fails closed rather than guessing.
type setupMarketplaceSource struct {
	SourceType string `json:"sourceType"`
	Source     string `json:"source"`
}

type setupOutput struct{ bytes.Buffer }

func (b *setupOutput) Write(data []byte) (int, error) {
	if b.Len()+len(data) > 64*1024 {
		return 0, invalidArguments()
	}
	return b.Buffer.Write(data)
}

func querySetup(ctx context.Context, host string, args []string, invoke setupInvoke, output any) error {
	var data setupOutput
	if err := invoke(ctx, host, args, &data); err != nil {
		return invalidArguments()
	}
	if json.Unmarshal(data.Bytes(), output) != nil {
		return invalidArguments()
	}
	return nil
}

func sameSetupPath(got, want string) bool {
	return filepath.IsAbs(got) && filepath.Clean(got) == filepath.Clean(want)
}

func observeSetupMarketplace(ctx context.Context, host, root string, invoke setupInvoke) error {
	args := []string{"plugin", "marketplace", "list", "--json"}
	count := 0
	if host == "claude" {
		var entries []struct{ Name, Source, Path, InstallLocation string }
		if err := querySetup(ctx, host, args, invoke, &entries); err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.Name == "peerturn" {
				count++
				if entry.Source != "directory" || !sameSetupPath(entry.Path, root) || !sameSetupPath(entry.InstallLocation, root) {
					return invalidArguments()
				}
			}
		}
	} else {
		var listing struct {
			Marketplaces []struct {
				Name, Root        string
				MarketplaceSource setupMarketplaceSource `json:"marketplaceSource"`
			}
		}
		if err := querySetup(ctx, host, args, invoke, &listing); err != nil {
			return err
		}
		for _, entry := range listing.Marketplaces {
			if entry.Name == "peerturn" {
				count++
				if !sameSetupPath(entry.Root, root) || entry.MarketplaceSource.SourceType != "local" || !sameSetupPath(entry.MarketplaceSource.Source, root) {
					return invalidArguments()
				}
			}
		}
	}
	if count != 1 {
		return invalidArguments()
	}
	return nil
}

func observeSetupPlugin(ctx context.Context, host, root string, invoke setupInvoke) error {
	args := []string{"plugin", "list", "--json"}
	prefix, count := "adapters/claude", 0
	if host == "claude" {
		var entries []struct{ ID, InstallPath, ReadFromFolder string }
		if err := querySetup(ctx, host, args, invoke, &entries); err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.ID == "peerturn@peerturn" {
				count++
				if matchSetupPlugin(root, prefix, entry.InstallPath) != nil {
					return invalidArguments()
				}
				if entry.ReadFromFolder != "" && (!sameSetupPath(entry.ReadFromFolder, filepath.Join(root, prefix)) || matchSetupPlugin(root, prefix, entry.ReadFromFolder) != nil) {
					return invalidArguments()
				}
			}
		}
	} else {
		prefix = "adapters/codex/plugin"
		var listing struct {
			Installed []struct {
				Name, MarketplaceName string
				Installed             bool
				Source                struct{ Source, Path string }
				MarketplaceSource     setupMarketplaceSource `json:"marketplaceSource"`
			}
		}
		if err := querySetup(ctx, host, args, invoke, &listing); err != nil {
			return err
		}
		for _, entry := range listing.Installed {
			if entry.Name == "peerturn" && entry.MarketplaceName == "peerturn" {
				count++
				if !entry.Installed || entry.Source.Source != "local" || !sameSetupPath(entry.Source.Path, filepath.Join(root, prefix)) || entry.MarketplaceSource.SourceType != "local" || !sameSetupPath(entry.MarketplaceSource.Source, root) || matchSetupPlugin(root, prefix, entry.Source.Path) != nil {
					return invalidArguments()
				}
			}
		}
	}
	if count != 1 {
		return invalidArguments()
	}
	return nil
}
