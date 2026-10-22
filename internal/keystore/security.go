package keystore

import (
	"bytes"
	"context"
	"encoding/hex"
	"io"
	"os/exec"
	"time"
)

const service = "org.peerturn.wrapping-key.v1"

type securityBackend struct {
	run func(context.Context, []string, []byte) ([]byte, error)
}

func (b securityBackend) Load(ctx context.Context, id string) ([]byte, error) {
	if !validID(id) {
		return nil, ErrUnavailable
	}
	input := []byte("find-generic-password -a " + id + " -s " + service + " -w\n")
	raw, err := b.run(ctx, []string{"-i"}, input)
	defer clear(raw)
	if err != nil {
		return nil, ErrUnavailable
	}
	encoded := bytes.TrimSpace(raw)
	key := make([]byte, hex.DecodedLen(len(encoded)))
	n, err := hex.Decode(key, encoded)
	if err != nil || n != 32 {
		clear(key)
		return nil, ErrUnavailable
	}
	return key, nil
}

func (b securityBackend) Store(ctx context.Context, id string, key []byte) error {
	if !validID(id) || len(key) != 32 {
		return ErrUnavailable
	}
	// Only fixed tokens, a hexadecimal identifier and a hexadecimal secret enter
	// security's own command parser. No shell, history, file or secret argv exists.
	input := []byte("add-generic-password -a " + id + " -s " + service + " -w " + hex.EncodeToString(key) + "\n")
	defer clear(input)
	raw, err := b.run(ctx, []string{"-i"}, input)
	clear(raw)
	if err != nil {
		return ErrUnavailable
	}
	return nil
}

type cappedOutput struct{ bytes.Buffer }

func (b *cappedOutput) Write(p []byte) (int, error) {
	if len(p) > 512-b.Len() {
		return 0, ErrUnavailable
	}
	return b.Buffer.Write(p)
}

func runSecurity(ctx context.Context, _ []string, input []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/security", "-i")
	cmd.Stdin = bytes.NewReader(input)
	cmd.Stderr = io.Discard
	var out cappedOutput
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		clear(out.Bytes())
		return nil, ErrUnavailable
	}
	return out.Bytes(), nil
}
