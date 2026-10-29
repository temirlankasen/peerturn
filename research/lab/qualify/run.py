"""Paid native qualification entry point; run only in an explicitly authorized isolated job."""
import json
import os
from pathlib import Path
import platform
import re
import runpy
import signal
import sys
import tempfile


HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
from core import (Failure, Run, Summary, child_environment, command, digest, executable,
                  private_json, require, runtime_pair, validate_context)
from fixture import ROOT, Relay, package
from host import Host
from provider import Gateway
import scenarios

N = runpy.run_path(str(ROOT / "scripts/runtime_npm.py"))


def integer_environment(name, default, maximum):
    value = os.environ.get(name, str(default))
    require(re.fullmatch(r"[1-9][0-9]{0,5}", value) and int(value) <= maximum, "invalid_input")
    return int(value)


def model_environment(name):
    model = os.environ.get(name) or "gpt-5.4-mini"
    require(re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.:/-]{0,127}", model), "invalid_input")
    return model


def identities(run, versions, summary):
    binaries = {runtime: executable(runtime) for runtime in versions}
    environment = child_environment(run.root / "identity")
    runtime_identity = {}
    for runtime, binary in binaries.items():
        output = command([binary, "--version"], environment, run.root, timeout=15).decode().strip()
        pattern = (re.escape(versions[runtime]) + r" \(Claude Code\)" if runtime == "claude" else
                   r"codex-cli " + re.escape(versions[runtime]))
        require(re.fullmatch(pattern, output) is not None, "version_mismatch")
        runtime_identity[runtime] = {"version": versions[runtime], "binary_sha256": digest(binary)}
    manifest = os.environ.get("PEERTURN_QUAL_IDENTITY")
    require(manifest is not None, "artifact_mismatch")
    path = Path(manifest).resolve(strict=True)
    require(path.is_file() and path.stat().st_size <= 65536, "artifact_mismatch")
    supplied = json.loads(path.read_text())
    require(isinstance(supplied, dict) and isinstance(supplied.get("runtimes"), dict), "artifact_mismatch")
    artifacts = json.loads(os.environ.get("QUAL_ARTIFACTS", "{}"))
    require(isinstance(artifacts, dict) and isinstance(artifacts.get("claude"), str), "artifact_mismatch")
    try:
        npm = N["verify"](path.parent, versions["claude"], artifacts["claude"],
                          os.environ.get("QUAL_NPM_LOCK"))
    except (OSError, ValueError, KeyError, TypeError):
        raise Failure("artifact_mismatch") from None
    runtime_identity["claude"].update(npm, source_integrity=artifacts["claude"])
    for runtime, expected in runtime_identity.items():
        actual = supplied["runtimes"].get(runtime, {})
        require(all(actual.get(key) == value for key, value in expected.items()), "artifact_mismatch")
    commit = command([executable("git"), "rev-parse", "HEAD"], environment, ROOT).decode().strip()
    require(re.fullmatch(r"[0-9a-f]{40}", commit), "artifact_mismatch")
    require((ROOT / "bin/peerturn").is_file(), "missing_binary")
    summary.identities = {"peerturn_commit": commit, "peerturn_sha256": digest(ROOT / "bin/peerturn"),
                          "runtimes": runtime_identity, "os": platform.system().lower(),
                          "arch": platform.machine().lower(), "manifest_sha256": digest(path)}
    summary.record("artifact_identity", True)
    return binaries


def qualify(run, versions, summary):
    binaries = identities(run, versions, summary)
    gateway = Gateway(os.environ.get("PEERTURN_QUAL_LITELLM_BASE", ""),
                      os.environ.get("PEERTURN_QUAL_LITELLM_KEY", ""),
                      integer_environment("PEERTURN_QUAL_MAX_REQUESTS", 100, 200))
    run.provider = gateway
    staged, pin = package(run, binaries)
    relay = Relay(run, staged / "peerturn")
    models = {runtime: model_environment("PEERTURN_QUAL_" + runtime.upper() + "_MODEL")
              for runtime in versions}
    hosts = []
    try:
        for alias, runtime in (("a", "claude"), ("b", "codex"), ("c", "claude"),
                               ("d", "codex"), ("e", "claude"), ("f", "codex")):
            hosts.append(Host(run, alias, runtime, binaries[runtime], staged, pin,
                              relay.url, relay.certificate, gateway, models[runtime]))
        summary.record("setup", True)
        keys = [host.bootstrap() for host in hosts]
        # Discovery/tool listing may precede the daemon; this is a real host tool call.
        unavailable = scenarios.call_prompt(hosts[1], "peer_status")["result"]
        code = unavailable.get("error", {}).get("code", unavailable.get("code"))
        require(code in ("daemon_unavailable", "runtime_unavailable", "unavailable", "unbound"))
        relay.start(keys)
        for host in hosts:
            host.start_daemon()
            host.bind()
            require(host.query("status").get("runtime_version") == versions[host.runtime],
                    "version_mismatch")
        summary.record("native_binding", True)
        summary.record("late_daemon_mcp", True)
        a, b, c, d, e, f = hosts
        scenarios.join(a, b, summary, "owner_invite_claude_to_codex")
        scenarios.join(d, c, summary, "owner_invite_codex_to_claude")
        scenarios.selected_handle(f, e, summary)
        scenarios.authorize_effects(a)
        scenarios.authorize_effects(b)
        scenarios.coauthor(a, b, "QUAL_EFFECT_C2X", summary, "coauthor_claude_to_codex")
        scenarios.coauthor(b, a, "QUAL_EFFECT_X2C", summary, "coauthor_codex_to_claude")
        # Unused fresh capabilities stay entirely outside owner prompts in A/B.
        scenarios.peer_invite(a, b, d.invite(), summary, "peer_invite_refused_codex")
        scenarios.peer_invite(b, a, d.invite(), summary, "peer_invite_refused_claude")
        scenarios.review(a, b, d.invite(), summary)
        scenarios.inform(a, b, summary)
        scenarios.revoke(a, b, summary)
    finally:
        # Terminal descriptors are local resources; process groups are stopped by Run.close.
        for host in hosts:
            host.close()


def main():
    os.umask(0o077)
    validate_context(os.environ)
    for name in ("PEERTURN_QUAL_LITELLM_KEY", "PEERTURN_QUAL_LITELLM_BASE"):
        value = os.environ.get(name, "")
        if value:
            escaped = value.replace("%", "%25").replace("\r", "%0D").replace("\n", "%0A")
            print("::add-mask::" + escaped, flush=True)
    require(len(sys.argv) == 3, "invalid_input")
    runtime, version = sys.argv[1:]
    versions = runtime_pair(runtime, version)
    require(platform.system() in ("Linux", "Darwin") and
            platform.machine().lower() in ("arm64", "aarch64", "x86_64"), "unsupported_platform")
    summary = Summary(runtime, version)
    def interrupted(_number, _frame):
        raise Failure("interrupted")
    for number in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
        signal.signal(number, interrupted)
    temporary = tempfile.TemporaryDirectory(prefix="pq-", dir="/tmp")
    try:
        root = Path(temporary.name).resolve()
        root.chmod(0o700)
        run = Run(root, integer_environment("PEERTURN_QUAL_MAX_SECONDS", 1800, 3600))
        try:
            qualify(run, versions, summary)
        except Failure as error:
            summary.failure = error.reason
        except (OSError, ValueError, KeyError, TypeError, RecursionError):
            summary.failure = "internal_error"
        finally:
            try:
                cleaned = run.close()
            except (Failure, OSError):
                cleaned = False
                summary.failure = "teardown_failed"
    finally:
        try:
            temporary.cleanup()
        except OSError:
            cleaned = False
    try:
        summary.record("teardown", cleaned)
    except Failure:
        summary.failure = "teardown_failed"
    private_json(ROOT / "qualification-summary.json", summary.payload())
    passed = summary.payload()["passed"] and summary.failure is None
    print("Native runtime qualification: " + ("passed" if passed else "failed; candidate remains unverified"))
    return 0 if passed else 1


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (Failure, OSError, ValueError, KeyError):
        print("Native runtime qualification: invalid or incomplete inputs", file=sys.stderr)
        raise SystemExit(2) from None
