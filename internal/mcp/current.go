package mcp

import (
	"context"
	"encoding/hex"
	"encoding/json"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/localipc"
)

// CurrentBinding is read-only native proof, not an authorization capability.
type CurrentBinding struct {
	Session      string
	BindingEpoch string
	Caller       Caller
}

func (s *sessionBinding) callCurrent(ctx context.Context, command localipc.Request, meta json.RawMessage) (json.RawMessage, error) {
	if len(meta) != 0 {
		var informational map[string]json.RawMessage
		if codec.Decode(meta, maxToolMeta, &informational) != nil || informational == nil {
			return nil, localipc.NewError("invalid_encoding", "rejected")
		}
	}
	current, err := s.options.ResolveCurrent(ctx)
	if err != nil {
		return nil, err
	}
	epoch, err := hex.DecodeString(current.BindingEpoch)
	validEpoch := err == nil && len(epoch) == 16 && hex.EncodeToString(epoch) == current.BindingEpoch
	validProof := current.Session != "" && len(current.Session) <= 160 && current.Caller != nil && validEpoch
	if !validProof {
		return nil, localipc.NewError("needs_rebind", "needs_rebind")
	}
	if s.options.ExpectedBindingEpoch == "" {
		// Pin proof before hello, including a refused capability handshake.
		s.options.Session = current.Session
		s.options.ExpectedBindingEpoch = current.BindingEpoch
		s.options.Caller = current.Caller
	} else if current.Session != s.options.Session || current.BindingEpoch != s.options.ExpectedBindingEpoch {
		return nil, localipc.NewError("needs_rebind", "needs_rebind")
	}
	if s.bound == nil {
		s.bound, err = bindCapability(ctx, s.options)
		if err != nil {
			return nil, err
		}
	}
	return s.bound.Call(ctx, command)
}
