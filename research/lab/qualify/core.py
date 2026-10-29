"""Closed inputs, private execution profiles and sanitized qualification output."""
import hashlib
import json
import os
from pathlib import Path
import re
import selectors
import shutil
import signal
import subprocess
import time


BASELINES = {"claude": "2.1.306", "codex": "0.163.0"}
CHECKS = ("artifact_identity", "setup", "native_binding", "late_daemon_mcp",
          "owner_invite_claude_to_codex", "owner_invite_codex_to_claude",
          "selected_handle", "peer_invite_refused_claude", "peer_invite_refused_codex",
          "codex_review_refused", "coauthor_claude_to_codex", "coauthor_codex_to_claude",
          "inform_no_wake", "inform_notice", "inform_body", "revoke", "teardown")
REASONS = {"invalid_input", "missing_binary", "version_mismatch", "artifact_mismatch",
           "missing_evidence", "native_contract_changed", "native_timeout", "process_failed",
           "output_limit", "provider_unavailable", "budget_exhausted", "unsupported_platform",
           "teardown_failed", "interrupted", "internal_error"}


class Failure(Exception):
    def __init__(self, reason="missing_evidence"):
        self.reason = reason if reason in REASONS else "internal_error"
        super().__init__(self.reason)


def require(value, reason="missing_evidence"):
    if not value:
        raise Failure(reason)


def version_tuple(value):
    require(isinstance(value, str) and len(value) <= 32 and
            re.fullmatch(r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)", value),
            "invalid_input")
    parsed = tuple(map(int, value.split(".")))
    require(max(parsed) <= 4294967295, "invalid_input")
    return parsed


def runtime_pair(runtime, version):
    require(runtime in BASELINES, "invalid_input")
    require(version_tuple(version) >= version_tuple(BASELINES[runtime]), "invalid_input")
    return dict(BASELINES, **{runtime: version})


def digest(path):
    checksum = hashlib.sha256()
    with Path(path).open("rb") as source:
        for chunk in iter(lambda: source.read(131072), b""):
            checksum.update(chunk)
    return checksum.hexdigest()


def validate_context(env):
    require(env.get("GITHUB_ACTIONS") == "true" and env.get("GITHUB_REF") == "refs/heads/main" and
            env.get("GITHUB_EVENT_NAME") in ("schedule", "workflow_dispatch"), "invalid_input")


def private_json(path, value):
    path = Path(path)
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    temporary = path.with_suffix(".pending")
    with temporary.open("w", encoding="utf-8", opener=lambda p, f: os.open(p, f, 0o600)) as out:
        json.dump(value, out, sort_keys=True)
        out.write("\n")
    temporary.replace(path)


def child_environment(root):
    root = Path(root).resolve()
    env = {"PATH": os.environ.get("PATH", "/usr/bin:/bin"), "LANG": "en_US.UTF-8",
           "LC_ALL": "en_US.UTF-8", "TERM": "xterm-256color", "NO_COLOR": "1"}
    for key, subdir in (("HOME", "home"), ("TMPDIR", "tmp"), ("CODEX_HOME", "home/.codex"),
                        ("CLAUDE_CONFIG_DIR", "home/.claude"), ("npm_config_cache", "npm-cache"),
                        ("CLAUDE_CODE_TMPDIR", "tmp/claude"), ("XDG_RUNTIME_DIR", "runtime"),
                        ("XDG_CONFIG_HOME", "home/.config"), ("XDG_STATE_HOME", "home/.local/state")):
        directory = root / subdir
        directory.mkdir(mode=0o700, parents=True, exist_ok=True)
        env[key] = str(directory)
    env.update({"DISABLE_AUTOUPDATER": "1", "DISABLE_NON_ESSENTIAL_MODEL_CALLS": "1",
                "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "DO_NOT_TRACK": "1",
                "DISABLE_TELEMETRY": "1", "CLAUDE_CODE_DISABLE_FEEDBACK_SURVEY": "1"})
    return env


def executable(name):
    path = shutil.which(name)
    require(path is not None, "missing_binary")
    path = Path(path).resolve(strict=True)
    require(path.is_file() and os.access(path, os.X_OK), "missing_binary")
    return str(path)


def command(argv, env, cwd, timeout=120, input_data=None, allowed=(0,)):
    require(Path(argv[0]).is_absolute(), "invalid_input")
    with subprocess.Popen(argv, cwd=cwd, env=env, stdin=subprocess.PIPE,
                          stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                          start_new_session=True) as proc:
        selector, buffers = selectors.DefaultSelector(), {proc.stdout: bytearray(), proc.stderr: bytearray()}
        deadline = time.monotonic() + timeout
        try:
            require(input_data is None or len(input_data) <= 8192, "invalid_input")
            if input_data:
                proc.stdin.write(input_data)
            proc.stdin.close()
            for stream in buffers:
                selector.register(stream, selectors.EVENT_READ)
            while selector.get_map():
                require(time.monotonic() < deadline, "native_timeout")
                for key, _events in selector.select(timeout=0.1):
                    chunk = os.read(key.fileobj.fileno(), 65536)
                    if chunk:
                        buffers[key.fileobj].extend(chunk)
                        require(sum(map(len, buffers.values())) <= 8 * 1024 * 1024, "output_limit")
                    else:
                        selector.unregister(key.fileobj)
            proc.wait(timeout=max(0.1, deadline - time.monotonic()))
            require(proc.returncode in allowed, "process_failed")
            return bytes(buffers[proc.stdout])
        except (Failure, subprocess.TimeoutExpired) as error:
            try:
                os.killpg(proc.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            proc.wait(timeout=5)
            if isinstance(error, Failure):
                raise
            raise Failure("native_timeout") from None
        finally:
            selector.close()


class Summary:
    def __init__(self, runtime, version):
        self.runtime, self.version, self.checks = runtime, version, {}
        self.identities, self.failure = {}, None

    def record(self, name, passed):
        require(name in CHECKS and type(passed) is bool and name not in self.checks, "invalid_input")
        self.checks[name] = passed
        require(passed)

    def payload(self):
        value = {"schema": 1, "runtime": self.runtime, "version": self.version,
                 "passed": self.failure is None and all(self.checks.get(name) is True for name in CHECKS),
                 "checks": {name: self.checks.get(name, False) for name in CHECKS}}
        if self.identities:
            value["identities"] = self.identities
        if self.failure:
            value["failure"] = self.failure if self.failure in REASONS else "internal_error"
        return value


class Run:
    def __init__(self, root, seconds=1800):
        self.root, self.processes, self.native_daemons = Path(root).resolve(), [], []
        self.cleanups = []
        self.deadline = time.monotonic() + seconds
        self.provider = None

    def check(self):
        require(time.monotonic() < self.deadline, "native_timeout")
        if self.provider:
            self.provider.check()

    def wait(self, predicate, timeout=120):
        deadline = min(self.deadline, time.monotonic() + timeout)
        while time.monotonic() < deadline:
            self.check()
            result = predicate()
            if result:
                return result
            time.sleep(0.2)
        raise Failure("native_timeout")

    def spawn(self, argv, env, cwd, **kwargs):
        self.check()
        proc = subprocess.Popen(argv, env=env, cwd=cwd, start_new_session=True, **kwargs)
        self.processes.append(proc)
        return proc

    def close(self):
        if self.provider:
            self.provider.close()
        cleaned = True
        for cleanup in self.cleanups:
            try:
                cleaned = cleanup() and cleaned
            except (Failure, OSError, ValueError, KeyError, TypeError):
                cleaned = False
        for proc in reversed(self.processes):
            try:
                os.killpg(proc.pid, signal.SIGTERM)
            except ProcessLookupError:
                pass
        deadline = time.monotonic() + 8
        for proc in reversed(self.processes):
            try:
                proc.wait(timeout=max(0.1, deadline - time.monotonic()))
            except subprocess.TimeoutExpired:
                try:
                    os.killpg(proc.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                proc.wait(timeout=5)
        native_closed = True
        for daemon in self.native_daemons:
            try:
                native_closed = daemon.close() and native_closed
            except (Failure, OSError, ValueError):
                native_closed = False
        return cleaned and native_closed and all(proc.poll() is not None for proc in self.processes)
