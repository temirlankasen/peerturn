package runtimepolicy

// Contract is a model-free observation, not semantic qualification of the host.
type Contract struct {
	ID           string   `json:"id"`
	Phase        string   `json:"phase"`
	Status       string   `json:"status"`
	Code         string   `json:"code,omitempty"`
	Capabilities []string `json:"capabilities"`
	Action       string   `json:"action,omitempty"`
}

// Report keeps runtime verification separate from observed capability readiness.
type Report struct {
	Decision
	Source                string     `json:"source"`
	Contracts             []Contract `json:"contracts"`
	AvailableCapabilities []string   `json:"available_capabilities"`
}

// NewReport starts with no claimed live capabilities; adapters supply observations.
func NewReport(runtime, raw string) *Report {
	return &Report{Decision: Evaluate(runtime, raw), Contracts: []Contract{}, AvailableCapabilities: []string{}}
}
