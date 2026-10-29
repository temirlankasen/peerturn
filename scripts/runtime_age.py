"""Upstream publication age gates the verified label, never runtime admission."""
from datetime import datetime, timedelta, timezone
import json
import shutil
import subprocess


MINIMUM_AGE = timedelta(hours=72)


def old_enough(published, now=None):
    if not isinstance(published, str) or len(published) > 64:
        raise ValueError("missing upstream publication timestamp")
    timestamp = datetime.fromisoformat(published.replace("Z", "+00:00"))
    if timestamp.tzinfo is None:
        raise ValueError("upstream publication timestamp requires timezone")
    return (now or datetime.now(timezone.utc)) - timestamp >= MINIMUM_AGE


def npm_time():
    binary = shutil.which("npm")
    if not binary:
        raise ValueError("npm required for publication age")
    result = subprocess.run([binary, "view", "@anthropic-ai/claude-code", "time", "--json",
                             "--registry=https://registry.npmjs.org"],
                            capture_output=True, timeout=60, check=False)
    if result.returncode or len(result.stdout) > 2_000_000:
        raise ValueError("npm publication time unavailable")
    data = json.loads(result.stdout)
    if not isinstance(data, dict):
        raise ValueError("invalid npm publication time inventory")
    return data


def mature(api, runtime, version, now=None):
    if runtime == "claude":
        published = npm_time().get(version)
    elif runtime == "codex":
        release = api.request(f"repos/openai/codex/releases/tags/rust-v{version}")
        if release["draft"] or release["prerelease"] or release["tag_name"] != "rust-v" + version:
            raise ValueError("invalid upstream release publication")
        published = release.get("published_at")
    else:
        raise ValueError("invalid runtime")
    return old_enough(published, now)
