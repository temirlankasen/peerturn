package config

import "errors"

// AdmissionMode selects operator-controlled relay admission policy.
type AdmissionMode string

const (
	// AdmissionClosed restricts new invitations to configured operators.
	AdmissionClosed AdmissionMode = "closed"
	// AdmissionSelfService enables bounded admission for authenticated routing keys.
	AdmissionSelfService AdmissionMode = "self_service"
)

// SelfServiceLimits bounds invitations per routing key and the inviter registry.
// Configuring limits never enables admission; AdmissionMode selects the policy.
type SelfServiceLimits struct {
	ActiveInvites   int `toml:"active_invites"`
	DailyInvites    int `toml:"daily_invites"`
	LifetimeInvites int `toml:"lifetime_invites"`
	Inviters        int `toml:"inviters"`
}

func (m AdmissionMode) validate() error {
	switch m {
	case AdmissionClosed, AdmissionSelfService:
		return nil
	default:
		return errors.New("admission_mode must be closed or self_service")
	}
}

func (l SelfServiceLimits) validate() error {
	if l.ActiveInvites < 1 || l.ActiveInvites > 128 {
		return errors.New("self_service.active_invites must be within 1..128")
	}
	if l.DailyInvites < 1 || l.DailyInvites > 1024 {
		return errors.New("self_service.daily_invites must be within 1..1024")
	}
	if l.LifetimeInvites < 1 || l.LifetimeInvites > 65536 {
		return errors.New("self_service.lifetime_invites must be within 1..65536")
	}
	if l.Inviters < 1 || l.Inviters > 10000 {
		return errors.New("self_service.inviters must be within 1..10000")
	}
	if l.ActiveInvites > l.DailyInvites || l.DailyInvites > l.LifetimeInvites {
		return errors.New("self_service requires active_invites <= daily_invites <= lifetime_invites")
	}
	return nil
}
