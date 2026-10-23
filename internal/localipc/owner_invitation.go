package localipc

// OwnerInvitation is a local selector with a truncated public routing hint.
type OwnerInvitation struct {
	Handle      string `json:"handle"`
	RelayHost   string `json:"relay_host"`
	RoutingHint string `json:"routing_hint"`
	AgeSeconds  int64  `json:"age_seconds"`
}
