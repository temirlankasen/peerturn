"""Discover stable upstream candidates; durable claims prevent duplicate dispatch."""
import json
import os
import re
import runpy
import shutil
import subprocess
import sys
from pathlib import Path


C = runpy.run_path(str(Path(__file__).with_name("runtime_common.py")))
GitHub, ROOT = C["GitHub"], C["ROOT"]


def npm(*args):
    binary = shutil.which("npm")
    if not binary:
        raise ValueError("npm required")
    result = subprocess.run([binary, "view", "@anthropic-ai/claude-code" + args[0], *args[1:], "--json"],
                            capture_output=True, timeout=60, check=False)
    if result.returncode or len(result.stdout) > 2_000_000:
        raise ValueError("npm metadata unavailable")
    return json.loads(result.stdout)


def candidates(api, records):
    # Ask explicitly for npm's latest version, but also catch versions published
    # between two scheduled runs. A mutable dist-tag is not artifact identity.
    latest = npm("", "version")
    versions = npm("", "versions")
    if not isinstance(versions, list) or len(versions) > 20000 or latest not in versions:
        raise ValueError("invalid npm version inventory")
    result = []
    for version in versions:
        try:
            unseen = C["eligible"]("claude", version, records)
        except ValueError:
            continue
        if unseen:
            integrity = npm("@" + version, "dist.integrity")
            if not isinstance(integrity, str) or not re.fullmatch(r"sha512-[A-Za-z0-9+/]{86}==", integrity):
                raise ValueError("npm integrity missing")
            result.append({"runtime": "claude", "version": version, "identity": integrity})
    for page in range(1, 101):
        releases = api.request(f"repos/openai/codex/releases?per_page=100&page={page}")
        for release in releases:
            tag = release.get("tag_name", "")
            if release.get("draft") or release.get("prerelease") or not tag.startswith("rust-v"):
                continue
            version = tag[6:]
            try:
                unseen = C["eligible"]("codex", version, records)
            except ValueError:
                continue
            if unseen:
                digest = C["codex_identity"](release)
                result.append({"runtime": "codex", "version": version, "identity": digest})
        if len(releases) < 100:
            break
    else:
        raise ValueError("upstream pagination bound exceeded")
    return sorted(result, key=lambda item: (item["runtime"], C["version_tuple"](item["runtime"], item["version"])))


def dispatch(api, candidate):
    runtime, version = candidate["runtime"], candidate["version"]
    C["version_tuple"](runtime, version)
    C["validate_identity"](runtime, candidate["identity"])
    claim = f"<!-- runtime-watch {C['POLICY']} {candidate['identity']} -->"
    issue = C["issue_for"](api, runtime, version)
    if issue and claim in (issue.get("body") or ""):
        return False
    body = (f"{claim}\n\nQualification queued for {runtime} {version}.\n"
            "A dispatch with an uncertain response is never retried automatically. "
            "Use runtime-qualify workflow_dispatch on main to recover or retry.\n")
    if issue:
        api.put(f"issues/{issue['number']}", {"body": body, "state": "open"}, "PATCH")
    else:
        api.put("issues", {"title": C["title"](runtime, version), "body": body})
    # The durable claim is written BEFORE a single, non-retried mutation. If
    # dispatch fails/crashes, manual recovery is safer than duplicate paid runs.
    api.put("actions/workflows/runtime-qualify.yml/dispatches",
            {"ref": "main", "inputs": {"runtime": runtime, "version": version, "identity": candidate["identity"]}})
    return True


def main():
    C["validate_context"](os.environ)
    api = GitHub()
    repo = api.request(f"repos/{api.repo}")
    if repo["fork"] or repo["default_branch"] != "main":
        raise ValueError("upstream main repository required")
    records = json.loads((ROOT / C["RECORDS"]).read_text())
    found = candidates(api, records)
    # Bound the paid queue per sweep; later sweeps pick up the remaining backlog.
    count = 0
    for candidate in found:
        if dispatch(api, candidate):
            count += 1
            if count == 8:
                break
    print(f"Dispatched {count} candidate qualifications")


if __name__ == "__main__":
    try:
        main()
    except (ValueError, KeyError, C["APIError"], subprocess.SubprocessError):
        print("Runtime detection failed; inspect repository configuration/upstream availability", file=sys.stderr)
        sys.exit(1)
