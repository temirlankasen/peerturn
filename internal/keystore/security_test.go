package keystore

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestSecuritySecretIsOnlyTransferredOnStdin(t *testing.T) {
	key := bytes.Repeat([]byte{42}, 32)
	encoded := hex.EncodeToString(key)
	b := securityBackend{run: func(_ context.Context, args []string, stdin []byte) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), encoded) {
			t.Fatal("key on process argv")
		}
		if !slices.Equal(args, []string{"-i"}) || !bytes.Contains(stdin, []byte(encoded)) {
			t.Fatal("secret missing from isolated stdin")
		}
		return nil, nil
	}}
	if err := b.Store(t.Context(), strings.Repeat("a", 32), key); err != nil {
		t.Fatal(err)
	}
}

func TestSecurityReadValidatesKeyAndSanitizesErrors(t *testing.T) {
	for _, raw := range [][]byte{[]byte("short"), bytes.Repeat([]byte("0"), 65)} {
		b := securityBackend{run: func(context.Context, []string, []byte) ([]byte, error) { return raw, nil }}
		if _, err := b.Load(t.Context(), strings.Repeat("b", 32)); !errors.Is(err, ErrUnavailable) {
			t.Fatal("invalid key accepted")
		}
	}
	b := securityBackend{run: func(context.Context, []string, []byte) ([]byte, error) {
		return nil, errors.New("private service diagnostic")
	}}
	if err := b.Store(t.Context(), strings.Repeat("b", 32), make([]byte, 32)); !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "private") {
		t.Fatal("unsafe error")
	}
}
