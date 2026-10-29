package daemon

import "github.com/temirlankasen/peerturn/internal/runtimepolicy"

// finishRuntimeReport uses observed readiness, never qualification, to advertise
// capabilities. A future version gets every lane whose ordinary checks pass.
func finishRuntimeReport(s *Status) {
	runtime, source := "", ""
	switch s.Binding {
	case "native_claude":
		runtime, source = runtimepolicy.Claude, "session.version"
	case "codex_shared_daemon":
		runtime, source = runtimepolicy.Codex, "app_server.initialize"
	default:
		if s.RuntimePolicy == nil {
			return
		}
		runtime, source = s.RuntimePolicy.Runtime, s.RuntimePolicy.Source
	}
	if s.RuntimePolicy == nil {
		s.RuntimePolicy = runtimepolicy.NewReport(runtime, s.RuntimeVersion)
	}
	report := s.RuntimePolicy
	report.Decision = runtimepolicy.Evaluate(runtime, s.RuntimeVersion)
	report.Source = source
	report.AvailableCapabilities = []string{"local_revoke"}
	for _, capability := range []struct {
		name      string
		available bool
	}{
		{"binding", s.Capabilities.AttachExisting},
		{"coauthor", s.Capabilities.AttachExisting && s.Capabilities.WakeIdle},
		{"owner_invitation", s.Capabilities.InformBoundary},
		{"inform_boundary", s.Capabilities.InformBoundary},
		{"ask", s.Capabilities.ForkResponder},
	} {
		if capability.available {
			report.AvailableCapabilities = append(report.AvailableCapabilities, capability.name)
		}
	}
	found := false
	for i := range report.Contracts {
		contract := &report.Contracts[i]
		if contract.ID == "owner_boundary" {
			found = true
		}
		if contract.Status == "failed" {
			if contract.Code == "" {
				contract.Code = "runtime_contract_changed"
			}
			if s.AdapterReason == "" {
				s.AdapterReason = contract.Code
			}
		}
	}
	if !found {
		status := "not_observed"
		if s.InformBoundary == "observed" {
			status = "passed"
		}
		report.Contracts = append(report.Contracts, runtimepolicy.Contract{ID: "owner_boundary", Phase: "hook", Status: status, Capabilities: []string{"owner_invitation", "inform_boundary"}})
	}
}
