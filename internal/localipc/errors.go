package localipc

type publicTransportError interface {
	error
	PublicCode() string
	PublicStatus() int
}

func categorizedError(code string) *Error {
	state := "rejected"
	switch codeExit(code) {
	case 3:
		state = "unbound"
	case 4:
		state = "pending"
	case 7:
		state = "in_doubt"
	}
	return NewError(code, state)
}

func transportCode(problem publicTransportError) string {
	switch problem.PublicStatus() {
	case 401:
		return "auth_failed"
	case 403:
		return "scope_denied"
	case 409:
		return "conflict"
	case 410:
		switch problem.PublicCode() {
		case "gone", "expired_invite", "expired_message":
			return "expired"
		case "forbidden":
			return "scope_denied"
		default:
			return "unavailable"
		}
	case 426:
		return "unsupported_version"
	case 408, 500, 502, 504:
		return "unavailable"
	case 429, 503:
		switch problem.PublicCode() {
		case "quota", "rate_limit", "storage_unavailable", "backpressure", "poll_backpressure", "poll_limit", "stream_limit":
			return problem.PublicCode()
		default:
			return "unavailable"
		}
	}
	switch problem.PublicCode() {
	case "invalid", "forbidden", "conflict", "gone", "quota", "storage_unavailable", "invalid_body", "invalid_client_address", "too_large", "rate_limit", "headers_too_large", "backpressure", "unavailable", "unauthorized", "invalid_query", "invalid_ticket", "insecure_transport", "stream_limit", "invalid_schema", "invalid_claim", "invalid_invite_proof", "claim_reader_mismatch", "expired_invite", "unsupported_version", "enrollment_denied", "destination_mismatch", "invalid_cursor", "invalid_expiry", "invalid_limit", "invalid_wait", "poll_limit", "poll_backpressure", "request_cancelled", "shutdown", "invalid_request", "invalid_response", "signing_unavailable", "transport_unavailable", "relay_key_mismatch":
		return problem.PublicCode()
	default:
		return "unavailable"
	}
}

func actionMessage(code string) string {
	switch code {
	case "owner_invitation_required":
		return "Ask the owner to paste the invitation again in this native session after its binding and hook are active."
	case "peer_invitation_denied":
		return "This invitation claim matches a retained link from delivered peer content; joining is denied."
	}
	switch codeExit(code) {
	case 3:
		return "Verify the current native session binding or select an explicit preview session with --session."
	case 4:
		return "Check daemon and relay availability, then inspect operation status."
	case 5:
		return "Verify local credentials, peer identity, and grant authority."
	case 6:
		return "This capability is unavailable in the current preview."
	case 7:
		return "Inspect the native outcome and use adapter-supported owner reconciliation before retrying."
	default:
		return "Check required flags, explicit identifiers, scope, and stdin input."
	}
}
