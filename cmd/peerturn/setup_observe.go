package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/store"
)

// These are observations exposed by the pinned hosts, not attestations about
// their executing runtime. Unknown output fails closed rather than guessing.
type setupMarketplaceSource struct {
	SourceType string `json:"sourceType"`
	Source     string `json:"source"`
}

type setupOutput struct {
	buffer   bytes.Buffer
	overflow bool
}

func (b *setupOutput) Write(data []byte) (int, error) {
	if b.overflow || b.buffer.Len()+len(data) > 64*1024 {
		b.overflow = true
		return 0, invalidArguments()
	}
	return b.buffer.Write(data)
}

// Expose only observations: embedding Buffer would let io.Copy use ReadFrom
// and bypass Write's bound when collecting a host subprocess's output.
func (b *setupOutput) Bytes() []byte {
	return b.buffer.Bytes()
}

func (b *setupOutput) String() string {
	return b.buffer.String()
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
	if !filepath.IsAbs(got) || !filepath.IsAbs(want) {
		return false
	}
	if _, err := resolveSetupRoot(got); err != nil {
		return false
	}
	if _, err := resolveSetupRoot(want); err != nil {
		return false
	}
	canonicalGot, err := store.ResolveTrustedPath(got)
	if err != nil {
		return false
	}
	canonicalWant, err := store.ResolveTrustedPath(want)
	return err == nil && canonicalGot == canonicalWant
}

// The public repository is the only accepted remote source. Its ref is not
// trusted: a published marketplace counts only if its files equal the
// verified release inventory.
const (
	setupPublishedRepo = "temirlankasen/peerturn"
	setupPublishedURL  = "https://github.com/temirlankasen/peerturn.git"
)

// setupMarketplace is the observed peerturn marketplace. A published one was
// added by native plugin commands; root is then its host-managed checkout.
// A stale one is PeerTurn's own: an older installer package directory or the
// public repository at contents other than this release. Setup replaces it.
type setupMarketplace struct {
	found, published, stale bool
	root                    string
}

func observeSetupMarketplace(ctx context.Context, host, root string, invoke setupInvoke) error {
	observed, err := inspectSetupMarketplace(ctx, host, root, invoke, false)
	if err == nil && (observed.published || observed.stale) {
		return setupMarketplaceFailure(host)
	}
	return err
}

func inspectSetupMarketplace(ctx context.Context, host, root string, invoke setupInvoke, allowAbsent bool) (setupMarketplace, error) {
	args := []string{"plugin", "marketplace", "list", "--json"}
	var observed setupMarketplace
	count := 0
	if host == "claude" {
		var entries []*struct{ Name, Source, Repo, Path, InstallLocation string }
		if err := querySetup(ctx, host, args, invoke, &entries); err != nil || entries == nil {
			return observed, setupMarketplaceFailure(host)
		}
		for _, entry := range entries {
			if entry == nil || entry.Name == "" {
				return observed, setupMarketplaceFailure(host)
			}
			if entry.Name != "peerturn" {
				continue
			}
			count++
			switch {
			case entry.Source == "directory" && sameSetupPath(entry.Path, root) && sameSetupPath(entry.InstallLocation, root):
			case entry.Source == "directory" && entry.Path == entry.InstallLocation && previousSetupPackage(entry.Path, root):
				observed.stale = true
			case entry.Source == "github" && entry.Repo == setupPublishedRepo:
				observed.published, observed.root = true, entry.InstallLocation
			default:
				return observed, setupMarketplaceFailure(host)
			}
		}
	} else {
		var listing struct {
			Marketplaces []*struct {
				Name, Root        string
				MarketplaceSource setupMarketplaceSource `json:"marketplaceSource"`
			}
		}
		if err := querySetup(ctx, host, args, invoke, &listing); err != nil || listing.Marketplaces == nil {
			return observed, setupMarketplaceFailure(host)
		}
		for _, entry := range listing.Marketplaces {
			if entry == nil || entry.Name == "" {
				return observed, setupMarketplaceFailure(host)
			}
			if entry.Name != "peerturn" {
				continue
			}
			count++
			source := entry.MarketplaceSource
			switch {
			case source.SourceType == "local" && sameSetupPath(entry.Root, root) && sameSetupPath(source.Source, root):
			case source.SourceType == "local" && entry.Root == source.Source && previousSetupPackage(entry.Root, root):
				observed.stale = true
			case source.SourceType == "git" && source.Source == setupPublishedURL:
				observed.published, observed.root = true, entry.Root
			default:
				return observed, setupMarketplaceFailure(host)
			}
		}
	}
	if count != 1 && (!allowAbsent || count != 0) {
		return observed, setupMarketplaceFailure(host)
	}
	observed.found = count == 1
	if observed.published && !matchSetupPublished(host, root, observed.root) {
		observed.published, observed.stale = false, true
	}
	return observed, nil
}

// checkSetupClaudeEnabled guards the stale-marketplace replacement: Claude's
// marketplace remove also deletes the plugin's enabledPlugins entries in user,
// project and local settings, including an owner's false. The effective state
// hides a disable overridden in one scope, so every editable entry is read.
func checkSetupClaudeEnabled(ctx context.Context, invoke setupInvoke) error {
	problem := localipc.NewError("plugin_disabled", "needs_configuration")
	problem.Message = "The older PeerTurn plugin is disabled in some settings scope, or not installed and enabled here. Replacing its marketplace would delete enabledPlugins entries, including an owner's disable. Inspect claude plugin list --json and the enabledPlugins settings; the owner decides whether to enable it or remove the marketplace with claude plugin marketplace remove peerturn, then reruns peerturn setup claude."
	if !setupClaudeSettingsEnable() {
		return problem
	}
	var entries []struct {
		ID      string
		Enabled *bool
	}
	if err := querySetup(ctx, "claude", []string{"plugin", "list", "--json"}, invoke, &entries); err != nil {
		return setupMarketplaceFailure("claude")
	}
	for _, entry := range entries {
		if entry.ID == "peerturn@peerturn" && entry.Enabled != nil && *entry.Enabled {
			return nil
		}
	}
	return problem
}

// setupClaudeSettingsEnable reports whether no user, project or local settings
// file from the working directory upward holds anything but true for the plugin.
func setupClaudeSettingsEnable() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	config := os.Getenv("CLAUDE_CONFIG_DIR")
	if config == "" {
		config = filepath.Join(home, ".claude")
	}
	// Cowork mode reads cowork_settings.json in place of settings.json; both
	// are checked regardless of the mode this process happens to see.
	paths := []string{filepath.Join(config, "settings.json"), filepath.Join(config, "cowork_settings.json")}
	cwd, err := os.Getwd()
	if err != nil {
		return false
	}
	if cwd, err = filepath.EvalSymlinks(cwd); err != nil {
		return false
	}
	for path := cwd; ; path = filepath.Dir(path) {
		paths = append(paths, filepath.Join(path, ".claude", "settings.json"), filepath.Join(path, ".claude", "settings.local.json"))
		if filepath.Dir(path) == path {
			break
		}
	}
	for _, path := range paths {
		settings, readErr := readSetupClaudeConfig(path)
		if readErr != nil {
			return false
		}
		data, ok := settings["enabledPlugins"]
		if !ok {
			continue
		}
		var plugins map[string]json.RawMessage
		if codec.Decode(data, setupClaudeConfigLimit, &plugins) != nil {
			return false
		}
		if value, found := plugins["peerturn@peerturn"]; found && string(value) != "true" {
			return false
		}
	}
	return true
}

// matchSetupPublished compares the host's checkout of the public repository
// with the verified release. The repository carries no executable, so only
// the Codex plugin's bundled helper may be absent.
func matchSetupPublished(host, root, checkout string) bool {
	manifest, prefix, optional := ".claude-plugin/marketplace.json", "adapters/claude", []string(nil)
	if host == "codex" {
		manifest, prefix, optional = ".agents/plugins/marketplace.json", "adapters/codex/plugin", []string{"bin/peerturn"}
	}
	return matchSetupFile(root, manifest, checkout) && matchSetupPlugin(root, prefix, filepath.Join(checkout, prefix), optional...) == nil
}

func observeSetupPlugin(ctx context.Context, host, root string, marketplace setupMarketplace, invoke setupInvoke) error {
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
				if !entry.Installed || entry.Source.Source != "local" {
					return invalidArguments()
				}
				if !marketplace.published {
					if !sameSetupPath(entry.Source.Path, filepath.Join(root, prefix)) || entry.MarketplaceSource.SourceType != "local" || !sameSetupPath(entry.MarketplaceSource.Source, root) || matchSetupPlugin(root, prefix, entry.Source.Path) != nil {
						return invalidArguments()
					}
					continue
				}
				// The repository carries no executable; its hook falls back to
				// the verified release helper, so only the bundled copy may be absent.
				if !sameSetupPath(entry.Source.Path, filepath.Join(marketplace.root, prefix)) || entry.MarketplaceSource.SourceType != "git" || entry.MarketplaceSource.Source != setupPublishedURL || matchSetupPlugin(root, prefix, entry.Source.Path, "bin/peerturn") != nil {
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
