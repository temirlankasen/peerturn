package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"

	"github.com/temirlankasen/peerturn/internal/claudeipc"
	"github.com/temirlankasen/peerturn/internal/daemon"
	"github.com/temirlankasen/peerturn/internal/localipc"
)

func newConnector() connector {
	runtime := &connectRuntime{}
	return connector{
		getenv:     os.Getenv,
		environ:    os.Environ,
		executable: os.Executable,
		readConfig: readDefaultConfig,
		pinProfile: func(config daemon.Config) { runtime.profileID = daemon.ProfileID(config.StateDir, config.Relay) },
		label:      randomSessionLabel,
		initialize: initializeDefaultProfile,
		call:       runtime.call,
		spawn:      spawnDetached,
		resolve:    runtime.resolve,
		bind:       runtime.bind,
		wait:       sleepContext,
	}
}

func readDefaultConfig() (daemon.Config, error) {
	path, err := daemon.DefaultConfigPath()
	if err != nil {
		return daemon.Config{}, err
	}
	return daemon.ReadConfig(path)
}

// Labels must not disclose owner names, native UUIDs or paths.
func randomSessionLabel() (string, error) {
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", err
	}
	return "claude-" + hex.EncodeToString(suffix[:]), nil
}

func initializeDefaultProfile(ctx context.Context, relay, label string) error {
	dir, err := daemon.ConfiguredStateDir()
	if err != nil {
		return err
	}
	path, err := daemon.DefaultConfigPath()
	if err != nil {
		return err
	}
	_, err = daemon.Initialize(ctx, daemon.InitOptions{StateDir: dir, ConfigPath: path, RelayURL: relay, SessionLabel: label, FilePermissions: true})
	return err
}

type connectRuntime struct{ dir, profileID string }

func (r *connectRuntime) client() (localipc.Client, error) {
	dir, err := daemon.DefaultRuntimeDir()
	if err != nil {
		return localipc.Client{}, err
	}
	if r.dir != "" && r.dir != dir {
		return localipc.Client{}, localipc.NewError("needs_rebind", "needs_rebind")
	}
	return localipc.Client{RuntimeDir: dir, ProfileID: r.profileID}, nil
}

func (r *connectRuntime) call(ctx context.Context, request localipc.Request) (json.RawMessage, error) {
	client, err := r.client()
	if err != nil {
		return nil, err
	}
	result, err := client.Call(ctx, request)
	if err == nil {
		r.dir = client.RuntimeDir
	}
	return result, err
}

func (r *connectRuntime) resolve(ctx context.Context) (claudeipc.CurrentEvidence, error) {
	client, err := r.client()
	if err != nil {
		return claudeipc.CurrentEvidence{}, err
	}
	return claudeipc.ResolveCurrentEvidence(ctx, client)
}

func (r *connectRuntime) bind(ctx context.Context) (string, error) {
	client, err := r.client()
	if err != nil {
		return "", err
	}
	return claudeipc.BindCurrentWhenReady(ctx, client)
}
