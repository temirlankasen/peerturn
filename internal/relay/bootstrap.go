package relay

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/invite"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/relay/store"
)

type jsonPacket = json.RawMessage

type bootstrap struct {
	Mailboxes []struct {
		ID      string `json:"id"`
		ReadKey string `json:"read_key"`
	} `json:"mailboxes"`
	RevokedInvites []string `json:"revoked_invites"`
	Invites        []struct {
		ID        string          `json:"id"`
		ProofKey  string          `json:"proof_key"`
		Packet    json.RawMessage `json:"packet"`
		ExpiresAt int64           `json:"expires_at"`
		Voucher   string          `json:"voucher"`
	} `json:"invites"`
}

// Bootstrap provisions local ciphertext state alongside signed admin enrollment.
func (s *Server) bootstrap() error {
	if err := s.loadRelayKey(); err != nil {
		return err
	}
	if s.cfg.BootstrapFile == "" {
		return nil
	}
	f, err := os.Open(s.cfg.BootstrapFile)
	if err != nil {
		return err
	}
	data, readErr := io.ReadAll(io.LimitReader(f, 1024*1024+1))
	if err = errors.Join(readErr, f.Close()); err != nil {
		return err
	}
	var b bootstrap
	if err = codec.Decode(data, 1024*1024, &b); err != nil {
		return err
	}
	for _, m := range b.Mailboxes {
		if !protocol.ValidToken(m.ID) || !validKey(m.ReadKey) {
			return errors.New("invalid bootstrap mailbox")
		}
		err = s.db.CreateMailbox(s.ctx, store.Mailbox{ID: m.ID, ReadKey: m.ReadKey}, "")
		if err != nil && s.db.CheckReader(s.ctx, m.ID, m.ReadKey) != nil {
			return err
		}
	}
	for _, i := range b.Invites {
		packet, decodeErr := invite.DecodePacket(i.Packet)
		if decodeErr != nil {
			return decodeErr
		}
		if i.ID != packet.InviteID || !validKey(i.ProofKey) || len(i.Voucher) < 32 || len(i.Voucher) > 256 {
			return errors.New("invalid bootstrap invite")
		}
		canonical, canonicalErr := codec.Canonical(packet)
		if canonicalErr != nil {
			return canonicalErr
		}
		if err = s.db.PutInvite(s.ctx, store.Invite{ID: i.ID, ProofKey: i.ProofKey, Packet: canonical, ExpiresAt: time.Unix(i.ExpiresAt, 0), Voucher: i.Voucher}); err != nil {
			return err
		}
	}
	for _, id := range b.RevokedInvites {
		if !protocol.ValidToken(id) {
			return errors.New("invalid revoked invite")
		}
		if err = s.db.RevokeInvite(s.ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) loadRelayKey() error {
	root, err := os.OpenRoot(s.cfg.DataDir)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			return
		}
	}()
	data, err := root.ReadFile("relay-signing.key")
	if errors.Is(err, os.ErrNotExist) {
		_, privateKey, keyErr := ed25519.GenerateKey(rand.Reader)
		if keyErr != nil {
			return keyErr
		}
		f, createErr := root.OpenFile("relay-signing.key", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if createErr != nil {
			return createErr
		}
		_, writeErr := f.Write(privateKey.Seed())
		err = errors.Join(writeErr, f.Sync(), f.Close())
		if err != nil {
			return err
		}
		directory, dirErr := root.Open(".")
		if dirErr != nil {
			return dirErr
		}
		if err = errors.Join(directory.Sync(), directory.Close()); err != nil {
			return err
		}
		data = privateKey.Seed()
	} else if err != nil {
		return err
	}
	if len(data) != ed25519.SeedSize {
		return errors.New("invalid relay key file")
	}
	privateKey := ed25519.NewKeyFromSeed(data)
	s.relayKey = base64.RawURLEncoding.EncodeToString(privateKey[ed25519.SeedSize:])
	return nil
}
