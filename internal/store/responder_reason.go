package store

// IsResponderRefusal is the bounded, body-free preflight diagnostic vocabulary.
func IsResponderRefusal(reason string) bool {
	switch reason {
	case "protected_path_policy_failed", "authority_file_key", "keystore_unavailable",
		"native_binding_unavailable", "bound_project_unavailable", "runtime_unavailable",
		"managed_policy_present", "managed_policy_unverified", "profile_mismatch",
		"snapshot_unavailable", "model_mismatch", "budget_unavailable", "budget_exceeded", "responder_busy",
		"macos_unqualified", "platform_unqualified", "provider_mismatch", "configuration_unavailable",
		"project_root_unqualified", "responder_failed", "fork_cleanup_failed", "needs_rebind":
		return true
	default:
		return false
	}
}
