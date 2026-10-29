package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/runtimepolicy"
)

type capabilityCaller interface {
	OpenCapability(context.Context, string) (json.RawMessage, func() error, error)
}

type capabilitySession struct {
	options        Options
	capability     string
	boundEpoch     string
	runtimeVersion string
	closeLease     func() error
	closed         bool
}

func bindCapability(ctx context.Context, options Options) (*capabilitySession, error) {
	hello, closeLease, err := openCapability(ctx, options)
	if err != nil {
		return nil, err
	}
	var binding struct {
		Capability     string          `json:"capability"`
		BindingEpoch   string          `json:"binding_epoch,omitempty"`
		RuntimeVersion json.RawMessage `json:"runtime_version,omitempty"`
	}
	if err := codec.Decode(hello, localipc.MaxFrame, &binding); err != nil || binding.Capability == "" {
		return nil, errors.Join(localipc.NewError("unbound", "unbound"), closeLease())
	}
	session := &capabilitySession{options: options, capability: binding.Capability, boundEpoch: binding.BindingEpoch, closeLease: closeLease}
	var runtimeVersion string
	if json.Unmarshal(binding.RuntimeVersion, &runtimeVersion) == nil && len(runtimeVersion) <= 160 {
		session.runtimeVersion = runtimepolicy.Evaluate(runtimepolicy.Codex, runtimeVersion).Version
	}
	if options.ExpectedBindingEpoch != "" && session.boundEpoch != options.ExpectedBindingEpoch {
		return nil, errors.Join(localipc.NewError("needs_rebind", "needs_rebind"), session.Close())
	}
	if err := session.checkRuntimeVersion(options.ExpectedRuntimeVersion); err != nil {
		return nil, errors.Join(err, session.Close())
	}
	return session, nil
}

func (s *capabilitySession) checkRuntimeVersion(expected string) error {
	if expected == "" || s.runtimeVersion == expected {
		return nil
	}
	failure := runtimepolicy.ContractFailure(runtimepolicy.Codex, s.runtimeVersion, "codex.mcp_version", "hello", "mcp")
	return errors.Join(localipc.RuntimeError(failure), failure)
}

func (s *capabilitySession) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	return errors.Join(releaseCapability(s.options, s.capability), s.closeLease())
}

func (s *capabilitySession) Call(ctx context.Context, request localipc.Request) (json.RawMessage, error) {
	request.Session, request.Capability = s.options.Session, s.capability
	result, err := s.options.Caller.Call(ctx, request)
	if err == nil || localipc.PublicError(err).Code != "unbound" {
		return result, err
	}
	// The authenticated daemon refuses unbound requests before command dispatch.
	// Only that refusal can renew and retry; ambiguous or transport failures never do.
	if err = s.Close(); err != nil {
		return nil, err
	}
	next, err := bindCapability(ctx, s.options)
	if err != nil {
		return nil, err
	}
	// Renewal cannot switch native incarnations or turn a cleared native binding
	// into preview authority. A fresh current-session handshake is required.
	if next.boundEpoch != s.boundEpoch {
		return nil, errors.Join(localipc.NewError("needs_rebind", "needs_rebind"), next.Close())
	}
	*s = *next
	request.Capability = s.capability
	return s.options.Caller.Call(ctx, request)
}

func openCapability(ctx context.Context, options Options) (json.RawMessage, func() error, error) {
	if caller, ok := options.Caller.(capabilityCaller); ok {
		return caller.OpenCapability(ctx, options.Session)
	}
	result, err := options.Caller.Call(ctx, localipc.Request{Method: "hello", Session: options.Session, BindingEpoch: options.ExpectedBindingEpoch})
	return result, func() error { return nil }, err
}

func releaseCapability(options Options, capability string) error {
	// The stdio context may already be canceled, but releasing authority still
	// gets a short independent window before the lease connection is closed.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := options.Caller.Call(ctx, localipc.Request{Method: "bye", Session: options.Session, Capability: capability})
	if err != nil && localipc.PublicError(err).Code != "unbound" {
		return err
	}
	return nil
}
