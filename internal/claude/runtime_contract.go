package claude

import (
	"github.com/temirlankasen/peerturn/internal/runtimepolicy"
	"slices"
)

// RuntimeContractFailure maps bounded host observations to dependent capabilities.
// The helper and daemon share this allowlist; peer text cannot select scopes.
func RuntimeContractFailure(version, id, phase string) *runtimepolicy.Error {
	var capabilities []string
	switch id {
	case "claude.prompt.submit", "claude.lifecycle":
		capabilities = []string{"coauthor"}
	case "claude.owner_boundary":
		capabilities = []string{"owner_invitation", "inform_boundary"}
	case "claude.clock.sleep":
		capabilities = []string{"coauthor", "owner_invitation", "inform_boundary", "inform_notification"}
	case "claude.ui.toast", "claude.ui.status":
		capabilities = []string{"inform_notification"}
	default:
		return nil
	}
	return runtimepolicy.ContractFailure(runtimepolicy.Claude, version, id, phase, capabilities...)
}

// CapabilityAvailable retains unrelated binding capabilities after contract loss.
func (b *Binding) CapabilityAvailable(capability string) bool {
	if b == nil {
		return false
	}
	for _, failure := range b.ContractFailures {
		if slices.Contains(failure.AffectedCapabilities, capability) {
			return false
		}
	}
	return true
}
