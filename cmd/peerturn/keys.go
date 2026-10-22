package main

import (
	"context"
	"encoding/json"
	"io"

	"github.com/temirlankasen/peerturn/internal/daemon"
)

func runKeyIsolation(ctx context.Context, command cliCommand, stdout, stderr io.Writer) int {
	dir, err := stateDir(command)
	if err == nil {
		err = daemon.MigrateAuthoritySecrets(ctx, dir, nil)
	}
	if err != nil {
		return writeFailure(stdout, stderr, err)
	}
	if err = json.NewEncoder(stdout).Encode(map[string]any{"storage_mode": "os_keystore", "authority_secrets_isolated": true}); err != nil {
		return 1
	}
	return 0
}
