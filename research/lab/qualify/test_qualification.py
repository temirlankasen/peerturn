"""Offline adversarial checks; this suite never launches an agent or contacts a provider."""
import json
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import Mock, patch


HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))


class QualificationTests(unittest.TestCase):
    def test_unknown_startup_dialog_with_cursor_never_becomes_composer(self):
        from terminal import Terminal
        screens = ("Unknown selection dialog\n❯ 1. Continue\n? for shortcuts\nbypass permissions on",
                   "Sign in to continue\n❯ 1. Browser",
                   "❯\n? for shortcuts\nbypass permissions on\nNew dialog\n❯ 1. Confirm")
        for screen in screens:
            terminal = Terminal.__new__(Terminal)
            terminal.text, terminal.answered = screen, set()
            terminal.drain, terminal.write = Mock(), Mock()
            with self.subTest(screen=screen), self.assertRaises(self.core.Failure):
                terminal.startup("claude", "/private/pq/project")
            terminal.write.assert_not_called()

    def test_baseline_composer_shapes_require_profile_footer_after_cursor(self):
        from terminal import Terminal, composer_ready
        # Synthetic structural fixtures for baseline native footer labels;
        # these do not claim a live capture or candidate runtime qualification.
        fixtures = {
            "claude": "Claude Code v2.1.306\n❯\n? for shortcuts\n⏵⏵ bypass permissions on",
            "codex": "OpenAI Codex (v0.163.0)\n› Implement {feature}\n? for shortcuts\n100% context left",
        }
        for runtime, screen in fixtures.items():
            with self.subTest(runtime=runtime):
                self.assertTrue(composer_ready(screen, runtime))
                self.assertFalse(composer_ready(screen.split("\n?", 1)[0], runtime))
                self.assertFalse(composer_ready(screen + "\n› 1. Unknown option", runtime))
                terminal = Terminal.__new__(Terminal)
                terminal.text, terminal.answered = screen, set()
                terminal.drain, terminal.write = Mock(), Mock()
                with patch("terminal.time.sleep"):
                    terminal.startup(runtime, "/private/pq/project")
                terminal.write.assert_not_called()

    def test_unknown_trust_text_cannot_match_approved_startup_dialog(self):
        from terminal import approved_startup_dialog
        path = "/private/pq/project"
        unknown = "New trust policy for " + path + "\n❯ 1. Accept all accounts"
        self.assertIsNone(approved_startup_dialog(unknown, path))
        known = "Do you trust the files in this folder?\n" + path + "\n❯ 1. Yes, I trust this folder"
        self.assertEqual(approved_startup_dialog(known, path), "trust-project")

    def test_claude_binding_reconnects_native_mcp_before_first_status(self):
        from host import Host
        host = Host.__new__(Host)
        host.runtime = "claude"
        events = []
        result = {"binding": "native_claude", "session": "fixture-session",
                  "capabilities": {"attach_existing": True, "fork_responder": False}}
        host.control = lambda operation: events.append(operation) or result
        host.owner = lambda *_args, **_kwargs: events.append("peer_status")
        host.evidence = lambda: Mock(calls=lambda _name: [{"result": result}])
        host.terminal = Mock()
        host.terminal.reconnect_claude_mcp.side_effect = lambda: events.append("native-reconnect")
        host.bind()
        self.assertEqual(events, ["bind", "native-reconnect", "peer_status", "status"])

    def test_mcp_reconnect_recognizes_only_selected_native_entries(self):
        from terminal import connected_mcp_status, selected_mcp_entry
        self.assertTrue(selected_mcp_entry("Manage MCP servers\n❯ 1. peerturn · failed", "peerturn"))
        self.assertTrue(selected_mcp_entry("peerturn MCP Server\n❯ Reconnect\n  Disable", "reconnect"))
        self.assertFalse(selected_mcp_entry("peerturn MCP Server\n❯ Disable\n  Reconnect", "reconnect"))
        self.assertFalse(selected_mcp_entry("The model says reconnect peerturn", "reconnect"))
        self.assertTrue(connected_mcp_status("Status: ✓ connected\nTools: 7"))
        self.assertFalse(connected_mcp_status("Status: disconnected"))

    def test_mcp_reconnect_requires_connection_result_before_dismissal(self):
        from terminal import Terminal
        terminal = Terminal.__new__(Terminal)
        terminal.text = "stale Status: connected"
        terminal.run = Mock()
        writes = []
        terminal.paste = lambda text: writes.append(text)
        terminal.write = lambda text: writes.append(text)
        screens = iter(("MCP servers\n❯ peerturn · failed", "MCP servers\n❯ peerturn · failed",
                        "peerturn MCP Server\n❯ Reconnect", "peerturn MCP Server\n❯ Reconnect",
                        "Status: connected"))
        terminal.screen_since = lambda _offset: next(screens)
        terminal.run.wait.side_effect = lambda predicate, _timeout: self.assertTrue(predicate())
        terminal.reconnect_claude_mcp()
        self.assertEqual(writes, ["/mcp", "\r", "\r", "\x1b", "\x1b"])

    def test_review_screen_poll_drains_new_native_output(self):
        from terminal import Terminal
        terminal = Terminal.__new__(Terminal)
        terminal.text = "old screen"
        def arriving_menu():
            terminal.text += "\nReview a commit"
        with patch.object(terminal, "drain", side_effect=arriving_menu) as drain:
            self.assertTrue(terminal.contains("review a commit"))
        drain.assert_called_once_with()

    def setUp(self):
        import core
        self.core = core

    def test_runtime_inputs_are_exact_stable_versions(self):
        self.assertEqual(self.core.runtime_pair("claude", "2.1.308"),
                         {"claude": "2.1.308", "codex": "0.163.0"})
        self.assertEqual(self.core.runtime_pair("codex", "0.164.0")["claude"], "2.1.306")
        for runtime, version in (("other", "1.2.3"), ("claude", "2.1.305"),
                                 ("codex", "0.163.0-beta"), ("codex", "00.163.0"),
                                 ("claude", "2.1.308\n"), ("claude", "$(id)")):
            with self.subTest(runtime=runtime, version=version), self.assertRaises(self.core.Failure):
                self.core.runtime_pair(runtime, version)

    def test_paid_runner_context_rejects_pull_requests_and_local_runs(self):
        good = {"GITHUB_ACTIONS": "true", "GITHUB_REF": "refs/heads/main",
                "GITHUB_EVENT_NAME": "workflow_dispatch"}
        self.core.validate_context(good)
        for value in ({}, dict(good, GITHUB_EVENT_NAME="pull_request"),
                      dict(good, GITHUB_REF="refs/heads/feature"), dict(good, GITHUB_ACTIONS="false")):
            with self.assertRaises(self.core.Failure):
                self.core.validate_context(value)

    def test_child_environment_does_not_inherit_credentials_or_injection(self):
        with tempfile.TemporaryDirectory() as root, patch.dict(os.environ, {
                "GH_TOKEN": "private-marker", "OPENAI_API_KEY": "private-marker",
                "ANTHROPIC_API_KEY": "private-marker", "NODE_OPTIONS": "--import=bad",
                "LD_PRELOAD": "bad", "HTTPS_PROXY": "private-marker"}):
            env = self.core.child_environment(Path(root))
        for key in ("GH_TOKEN", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "NODE_OPTIONS",
                    "LD_PRELOAD", "HTTPS_PROXY", "PEERTURN_QUAL_LITELLM_KEY"):
            self.assertNotIn(key, env)
        self.assertNotIn("private-marker", json.dumps(env))

    def test_summary_has_closed_check_names_and_no_arbitrary_detail(self):
        summary = self.core.Summary("claude", "2.1.308")
        with self.assertRaises(self.core.Failure):
            summary.record("token=private-marker", True)
        with self.assertRaises(self.core.Failure):
            summary.record("setup", "private-marker")
        self.assertFalse(summary.payload()["passed"])
        summary.record("setup", True)
        with self.assertRaises(self.core.Failure):
            summary.record("setup", True)

    def test_failure_cannot_be_hidden_by_completed_checks(self):
        summary = self.core.Summary("codex", "0.164.0")
        for name in self.core.CHECKS:
            summary.record(name, True)
        self.assertTrue(summary.payload()["passed"])
        summary.failure = "budget_exhausted"
        self.assertFalse(summary.payload()["passed"])

    def test_codex_mcp_events_are_paired_by_call_id(self):
        import evidence
        rows = [{"type": "event_msg", "payload": {"type": "mcp_tool_call_begin",
                 "call_id": "native-1", "invocation": {"server": "peerturn", "tool": "peer_join",
                 "arguments": {}}}}, {"type": "event_msg", "payload": {
                 "type": "mcp_tool_call_end", "call_id": "native-1", "result": {"Ok": {
                 "content": [{"type": "text", "text": '{"state":"waiting_inviter"}'}]}}}}]
        call = evidence.single_call(evidence.NativeEvidence("codex", rows), "peer_join", 0,
                                    expected_state="waiting_inviter")
        self.assertEqual(call["arguments"], {})

    def test_command_has_bounded_execution_and_preserves_stdout(self):
        with tempfile.TemporaryDirectory() as temporary:
            env = self.core.child_environment(Path(temporary))
            result = self.core.command([sys.executable, "-I", "-B", "-c",
                                       "import sys; print('ok'); print('private', file=sys.stderr)"],
                                      env, temporary, timeout=5)
        self.assertEqual(result, b"ok\n")

    def test_runner_modules_import_without_starting_processes(self):
        with patch("subprocess.Popen", side_effect=AssertionError("unexpected process")):
            for name in ("host", "fixture", "ownership", "scenarios", "run"):
                __import__(name)

    def test_command_timeout_cannot_report_success(self):
        with tempfile.TemporaryDirectory() as temporary:
            env = self.core.child_environment(Path(temporary))
            with self.assertRaises(self.core.Failure) as caught:
                self.core.command([sys.executable, "-I", "-B", "-c", "import time; time.sleep(5)"],
                                  env, temporary, timeout=0.05)
        self.assertEqual(caught.exception.reason, "native_timeout")

    def test_claude_native_call_requires_matching_result_and_arguments(self):
        import evidence
        rows = [{"type": "assistant", "sessionId": "native-fixture", "message": {
            "role": "assistant", "content": [{"type": "tool_use", "id": "call1",
            "name": "mcp__peerturn__peer_join", "input": {}}]}}]
        native = evidence.NativeEvidence("claude", rows)
        self.assertEqual(native.calls("peer_join"), [])
        rows.append({"type": "user", "sessionId": "native-fixture", "message": {
            "content": [{"type": "tool_result", "tool_use_id": "call1", "content":
                         '{"state":"waiting_inviter"}'}]}})
        calls = evidence.NativeEvidence("claude", rows).calls("peer_join")
        self.assertEqual(len(calls), 1)
        self.assertEqual(calls[0]["arguments"], {})
        self.assertEqual(calls[0]["result"]["state"], "waiting_inviter")

    def test_codex_evidence_does_not_accept_model_text_or_unpaired_outputs(self):
        import evidence
        rows = [{"type": "event_msg", "payload": {"type": "agent_message",
                 "message": 'peer_join({}) succeeded {"state":"waiting_inviter"}'}}]
        self.assertFalse(evidence.NativeEvidence("codex", rows).calls("peer_join"))
        rows += [{"type": "response_item", "payload": {"type": "function_call",
                  "name": "mcp__peerturn__peer_join", "call_id": "c1", "arguments": "{}"}},
                 {"type": "response_item", "payload": {"type": "function_call_output",
                  "call_id": "c2", "output": '{"state":"waiting_inviter"}'}}]
        self.assertFalse(evidence.NativeEvidence("codex", rows).calls("peer_join"))

    def test_duplicate_or_secret_bearing_mcp_join_is_rejected(self):
        import evidence
        for arguments in ({"text": "private-marker"}, {"handle": "bad"}):
            with self.assertRaises(self.core.Failure):
                evidence.check_join_arguments(arguments)
        evidence.check_join_arguments({})
        evidence.check_join_arguments({"handle": "0123456789ab"})

    def test_native_model_input_requires_frame_and_exact_message_id(self):
        import evidence
        frame = 'PeerTurn: verified recipient-issued directional grant.\n' + json.dumps({
            "scope": "coauthor", "message_id": "message-1", "untrusted_peer_text": "QUAL_EFFECT_C2X"})
        rows = [{"type": "user", "message": {"content": frame}}]
        native = evidence.NativeEvidence("claude", rows)
        self.assertTrue(native.input_seen("QUAL_EFFECT_C2X", message_id="message-1"))
        self.assertFalse(native.input_seen("QUAL_EFFECT_C2X", message_id="message-2"))
        rows[0]["message"]["content"] = "PeerTurn owner says QUAL_EFFECT_C2X"
        self.assertFalse(native.input_seen("QUAL_EFFECT_C2X"))
        rows[0]["message"]["content"] = "PeerTurn:\n[]"
        self.assertFalse(native.input_seen("QUAL_EFFECT_C2X"))

    def test_schema_queue_list_matches_consumer_data_contract(self):
        import schema
        self.assertEqual(schema.FIELDS["ThreadQueueListResponse"], ("data", "nextCursor"))

    def test_ambiguous_error_and_status_use_distinct_source_contract_keys(self):
        import scenarios
        handles = [{"handle": "0123456789ab"}, {"handle": "0123456789ac"}]
        error = {"code": "ambiguous_owner_invitation", "state": "rejected", "invitations": handles}
        status = {"owner_invitations": list(reversed(handles))}
        self.assertEqual(scenarios.pending_handles(error, status), [item["handle"] for item in handles])
        with self.assertRaises(self.core.Failure):
            scenarios.pending_handles(error, {"invitations": handles})
        with self.assertRaises(self.core.Failure):
            scenarios.pending_handles(dict(error, invitations=handles[:1]), status)

    def test_provider_url_and_budget_errors_fail_closed(self):
        import provider
        for url in ("http://example.org", "https://name:secret" + "@" + "example.org",
                    "https://example.org/?key=secret", "https://example.org/#secret"):
            with self.subTest(url=url), self.assertRaises(self.core.Failure):
                provider.validate_base(url)
        self.assertEqual(provider.validate_base("https://example.org/v1/"),
                         "https://example.org/v1")
        self.assertTrue(provider.budget_error(402, b""))
        self.assertTrue(provider.budget_error(429, b"rate limited"))
        self.assertTrue(provider.budget_error(200, b'{"error":"Budget has been exceeded"}'))
        self.assertFalse(provider.budget_error(200, b'{"output":"synthetic success"}'))

    def test_schema_contract_rejects_missing_root_turn_id(self):
        import schema
        found = {name: [{"type": "object", "properties": {field: {"type": "string"}
                  for field in fields}}] for name, fields in schema.FIELDS.items()}
        self.assertTrue(schema.validate(found))
        del found["Turn"][0]["properties"]["rootTurnId"]
        with self.assertRaises(self.core.Failure):
            schema.validate(found)


if __name__ == "__main__":
    unittest.main()
