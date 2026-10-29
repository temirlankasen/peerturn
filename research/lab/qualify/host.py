"""Isolated real native host and its observed transcript, never a fake adapter."""
import json
from pathlib import Path
import shutil
import sys

from core import Failure, child_environment, command, private_json, require
from evidence import NativeEvidence, json_lines
from terminal import Terminal


HERE = Path(__file__).resolve().parent


class Host:
    def __init__(self, run, alias, runtime, binary, package, pin, relay, certificate, gateway, model):
        self.run, self.runtime, self.alias = run, runtime, alias
        self.root = run.root / alias
        self.env = child_environment(self.root)
        self.env["SSL_CERT_FILE"] = str(certificate)
        self.env["SSL_CERT_DIR"] = str(run.root / "empty-certs")
        self.project = self.root / "project"
        self.project.mkdir(mode=0o700)
        self.binary, self.native, self.transcript = str(package / "peerturn"), binary, None
        self.env["PATH"] = str(run.root / "tools") + ":" + self.env["PATH"]
        self.native_id = None
        self.daemon_started = False
        run.cleanups.append(self.revoke_all)
        self.env["ANTHROPIC_BASE_URL"] = gateway.url
        self.env["ANTHROPIC_AUTH_TOKEN"] = "qualification-local"
        self.env["ANTHROPIC_API_KEY"] = "qualification-local"
        self.env["OPENAI_API_KEY"] = "qualification-local"
        self.env["QUALIFICATION_LOCAL_KEY"] = "qualification-local"
        shutil.copyfile(HERE / "control.py", self.root / "control.py")
        private_json(self.root / "plan.json", {"runtime": runtime, "alias": alias,
                     "binary": self.binary, "relay": relay})
        if runtime == "claude":
            private_json(Path(self.env["CLAUDE_CONFIG_DIR"]) / "settings.json", {
                "env": {"DISABLE_AUTOUPDATER": "1"},
                "permissions": {"defaultMode": "bypassPermissions"}})
            onboarding = {
                "hasCompletedOnboarding": True, "hasAcknowledgedCostThreshold": True,
                "bypassPermissionsModeAccepted": True,
                "customApiKeyResponses": {"approved": ["qualification-local"], "rejected": []}}
            private_json(Path(self.env["HOME"]) / ".claude.json", onboarding)
            private_json(Path(self.env["CLAUDE_CONFIG_DIR"]) / ".claude.json", onboarding)
            argv = [binary, "--model", model, "--permission-mode", "bypassPermissions"]
        else:
            config = Path(self.env["CODEX_HOME"]) / "config.toml"
            config.write_text('model = ' + json.dumps(model) + '\nmodel_provider = "qualification"\n'
                              'approval_policy = "never"\nsandbox_mode = "danger-full-access"\n'
                              'check_for_update_on_startup = false\n'
                              '[model_providers.qualification]\nname = "Qualification LiteLLM"\n'
                              'base_url = ' + json.dumps(gateway.url + "/v1") + '\n'
                              'env_key = "QUALIFICATION_LOCAL_KEY"\nwire_api = "responses"\n')
            config.chmod(0o600)
            daemon = Path(self.env["CODEX_HOME"]) / "app-server-daemon"
            daemon.mkdir(mode=0o700)
            private_json(daemon / "settings.json", {"updater": {"autoUpdateEnabled": False}})
            argv = [binary, "--no-alt-screen"]
        command([self.binary, "setup", runtime, "--path", str(package), "--sha256", pin],
                self.env, self.project, timeout=180)
        self.terminal = Terminal(run, argv, self.env, self.project)
        self.terminal.startup(runtime, str(self.project))

    def evidence(self):
        self.terminal.drain()
        if self.transcript is None:
            base = Path(self.env["CLAUDE_CONFIG_DIR"]) / "projects" if self.runtime == "claude" else \
                Path(self.env["CODEX_HOME"]) / "sessions"
            files = list(base.glob("**/*.jsonl")) if base.exists() else []
            roots = []
            for path in files:
                rows = json_lines(path)
                if self.runtime == "claude":
                    if any(row.get("type") in ("user", "assistant") and
                           not row.get("isSidechain", False) for row in rows):
                        roots.append(path)
                elif rows and rows[0].get("type") == "session_meta":
                    source = rows[0].get("payload", {}).get("source")
                    if source in ("cli", "vscode", "tui"):
                        roots.append(path)
            require(len(roots) <= 1, "native_contract_changed")
            if roots:
                self.transcript = roots[0]
        rows = json_lines(self.transcript) if self.transcript else []
        return NativeEvidence(self.runtime, rows)

    def owner(self, prompt, predicate=None, timeout=180):
        before = len(self.evidence().rows)
        self.terminal.paste(prompt)
        self.run.wait(lambda: (predicate(self.evidence(), before) if predicate else True) and
                      self.evidence().completed(before), timeout)
        return before

    def control(self, operation):
        path = self.root / "results" / (operation + ".json")
        require(not path.exists())
        self.owner("Run exactly this one trusted synthetic lab command with the shell tool: " +
                   str(Path(sys.executable).resolve()) + " -I -B " + str(self.root / "control.py") +
                   " " + operation + ". Do not inspect, recreate or print its private output file. "
                   "Then respond QUAL_CONTROL_DONE. Do not use any other tool.",
                   lambda _e, _b: path.exists())
        output = json.loads(path.read_text())
        require(output.get("exit") == 0, "native_contract_changed")
        return output["result"]

    def bootstrap(self):
        result = self.control("init")
        require(isinstance(result.get("session"), str) and isinstance(result.get("routing_key"), str))
        self.native_id = result["session"]
        if self.runtime == "codex":
            self.env["CODEX_THREAD_ID"] = self.native_id
            from ownership import NativeDaemon
            self.run.native_daemons.append(NativeDaemon(
                Path(self.env["CODEX_HOME"]) / "app-server-control/app-server-control.sock",
                self.run.root))
        return result["routing_key"]

    def cli(self, args, body=None, allowed=(0,)):
        out = command([self.binary, *args], self.env, self.project, timeout=30,
                      input_data=body.encode() if isinstance(body, str) else body, allowed=allowed)
        try:
            return json.loads(out)
        except ValueError:
            raise Failure("native_contract_changed") from None

    def start_daemon(self):
        self.run.spawn([self.binary, "daemon", "start"], self.env, self.project,
                       stdin=-3, stdout=-3, stderr=-3)
        self.daemon_started = True
        self.run.wait(lambda: self._daemon_ready(), 30)

    def _daemon_ready(self):
        try:
            return self.cli(["daemon", "status", "--json"]).get("session") == self.native_id
        except Failure:
            return False

    def bind(self):
        if self.runtime == "claude":
            self.control("bind")
            # A pre-binding `mcp --current` exits before initialize; the native
            # client must restart that failed server after nonce binding is ready.
            self.terminal.reconnect_claude_mcp()
        self.owner("Call the actual PeerTurn MCP peer_status tool exactly once with {}. "
                   "Do not run CLI commands, create files, reconnect or change settings. "
                   "Respond QUAL_STATUS_DONE after the tool result.",
                   lambda e, b: len([c for c in e.calls("peer_status") if c["index"] >= b]) == 1)
        call = self.evidence().calls("peer_status")[-1]
        result = call["result"]
        expected = "native_claude" if self.runtime == "claude" else "codex_shared_daemon"
        require(result and result.get("binding") == expected and
                result.get("capabilities", {}).get("attach_existing") is True)
        require(result.get("capabilities", {}).get("fork_responder") is False)
        current = self.control("status")
        require(current.get("session") == result.get("session") and
                current.get("binding") == result.get("binding"))

    def invite(self):
        result = self.cli(["invite", "create", "--current", "--scope", "coauthor", "--json"]
                          if self.runtime == "codex" else
                          ["invite", "create", "--session", self.native_id, "--scope", "coauthor", "--json"])
        require(isinstance(result.get("url"), str) and "#" in result["url"])
        return result["url"]

    def query(self, method, *args):
        return self.cli([method, "--session", self.native_id, *args, "--json"])

    def close(self):
        self.terminal.close()

    def revoke_all(self):
        if not self.daemon_started:
            return True
        try:
            status = self.query("status")
            args = ["grants", "list", "--session", self.native_id, "--json"]
            own = [g for g in self.cli(args) if g.get("issuer", {}).get("identity_id") ==
                   status.get("fingerprint")]
            for grant in own:
                if not grant.get("revoked"):
                    self.cli(["grants", "revoke", grant["grant_id"], "--session", self.native_id, "--json"])
            remaining = [g for g in self.cli(args) if g.get("issuer", {}).get("identity_id") ==
                         status.get("fingerprint")]
            return all(g.get("revoked") is True for g in remaining)
        except (Failure, OSError, ValueError, KeyError):
            return False
