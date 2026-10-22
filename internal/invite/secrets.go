package invite

import (
	"fmt"
	"log/slog"

	"github.com/temirlankasen/peerturn/internal/fault"
)

// Both pointer layers are intentional: fmt bypasses methods on unexported
// fields, and an invalid verb can restart reflection at depth zero. That restart
// may dereference linkSecrets, but its nested key pointers remain opaque.
type linkSecrets struct {
	signingSeed, encryptionKey *[32]byte
}

func (linkSecrets) String() string                   { return "[redacted]" }
func (s linkSecrets) GoString() string               { return s.String() }
func (s linkSecrets) LogValue() slog.Value           { return slog.StringValue(s.String()) }
func (s linkSecrets) Format(state fmt.State, _ rune) { writeRedacted(state, s.String()) }
func (linkSecrets) MarshalJSON() ([]byte, error)     { return nil, fault.New(fault.InvalidInvite) }
func (linkSecrets) MarshalText() ([]byte, error)     { return nil, fault.New(fault.InvalidInvite) }

// Format redacts Link for every verb, including numeric and hexadecimal output.
func (l Link) Format(state fmt.State, _ rune) { writeRedacted(state, l.String()) }

// LogValue keeps structured loggers from inspecting capability fields.
func (l Link) LogValue() slog.Value { return slog.StringValue(l.String()) }

// MarshalJSON rejects generic serialization; SecretURL is the dedicated export.
func (Link) MarshalJSON() ([]byte, error) { return nil, fault.New(fault.InvalidInvite) }

// MarshalText rejects generic serialization; SecretURL is the dedicated export.
func (Link) MarshalText() ([]byte, error) { return nil, fault.New(fault.InvalidInvite) }

func writeRedacted(state fmt.State, value string) {
	if _, err := state.Write([]byte(value)); err != nil {
		return
	}
}
