package main

// setupContractText is the full activation contract: what setup verified,
// what it did not, and the exact steps for a host whose hooks need trust.
// It is printed on request so the default output stays readable.
func setupContractText(host string) []string {
	lines := []string{
		"Host-reported marketplace source and observable plugin files matched the installer inventory. This is a local integrity match, not independent publisher authentication or proof of activation/current-session binding. Review and accept the host's actual hook trust prompt; never bypass it.",
	}
	if host == "claude" {
		lines = append(lines, "peerturn_binary is configured to the inventory-matched package executable. Run /reload-plugins in the selected session; if necessary resume the same session. The helper then binds the session itself when it was bound before; a first binding happens through peerturn connect or peerturn session bind --current from its tool child. Do not create a replacement session or change its permission mode/model.")
	} else {
		lines = append(lines, "If the running session has not activated the plugin, restart/resume that same session and accept hook trust. Initialize with peerturn init --codex-current and trusted relay options from its tool child. Shared daemon only; embedded Codex is unsupported. Never change the owner's permission mode/model.")
	}
	return append(lines,
		"A running daemon keeps the code it started with. peerturn upgrade restarts it; by hand, run peerturn daemon stop and start it again (Claude: peerturn connect from the selected session; Codex: peerturn daemon start). The profile, peers and grants are kept.",
		"MCP registration checked; this is not proof of activation or binding. Keep the daemon running in the same private runtime environment. Call peer_status with {}: binding must be native_claude (Claude) or codex_shared_daemon (Codex), with capabilities.attach_existing=true, and owner_invitation_hook must be active before pasting a link. Codex host MCP binds on its first tool call using host metadata, not CODEX_THREAD_ID at startup; tool listing alone supplies no authority. If unavailable, report the typed failure. The owner can run peerturn join --current --link-stdin in that session's tool child, supplying stdin or an owner-prepared private file; never embed an invitation in a tool command or configure a test label, native UUID or token as a fallback.")
}
