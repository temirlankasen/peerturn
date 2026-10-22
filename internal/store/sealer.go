package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"io"
	"os"
	"path/filepath"

	"github.com/temirlankasen/peerturn/internal/fault"
)

// Sealer protects local blobs with record-specific associated data. Implementations
// must authenticate both inputs; a plaintext implementation is never installed by default.
type Sealer interface {
	Seal(aad, plaintext []byte) ([]byte, error)
	Open(aad, ciphertext []byte) ([]byte, error)
}

type fileSealer struct{ aead cipher.AEAD }

// OpenFileSealer opens an existing key in file-permissions mode. It never creates
// a replacement: losing the external key makes retained bodies unrecoverable.
func OpenFileSealer(path string) (Sealer, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, failure(fault.InvalidEncoding)
	}
	info, err := os.Lstat(path)
	if err != nil || !privateFile(info) {
		return nil, failure(SealingFailed)
	}
	if err = checkPrivateDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	f, err := openLocalFile(path, os.O_RDONLY)
	if err != nil {
		return nil, failure(SealingFailed)
	}
	key, readErr := io.ReadAll(io.LimitReader(f, 33))
	closeErr := f.Close()
	if readErr != nil || closeErr != nil || len(key) != 32 {
		clear(key)
		return nil, failure(SealingFailed)
	}
	sealer, err := NewKeySealer(key)
	clear(key)
	return sealer, err
}

// NewKeySealer copies a 256-bit wrapping key into the standard AES-GCM cipher.
// Callers retain responsibility for clearing their source key bytes.
func NewKeySealer(key []byte) (Sealer, error) {
	if len(key) != 32 {
		return nil, failure(SealingFailed)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, failure(SealingFailed)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, failure(SealingFailed)
	}
	return &fileSealer{aead}, nil
}

func (s *fileSealer) Seal(aad, plain []byte) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, failure(SealingFailed)
	}
	return s.aead.Seal(nonce, nonce, plain, aad), nil
}

func (s *fileSealer) Open(aad, sealed []byte) ([]byte, error) {
	n := s.aead.NonceSize()
	if len(sealed) < n+s.aead.Overhead() {
		return nil, failure(SealingFailed)
	}
	plain, err := s.aead.Open(nil, sealed[:n], sealed[n:], aad)
	if err != nil {
		return nil, fault.New(SealingFailed)
	}
	return plain, nil
}
