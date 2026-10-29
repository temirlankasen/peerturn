"""Bounded, model-free bookkeeping shared by trusted main-only automation."""
import json
import hashlib
import os
from pathlib import Path
import re
import runpy
import time
import urllib.error
import urllib.request


ROOT = Path(__file__).resolve().parent.parent
E = runpy.run_path(str(Path(__file__).with_name("runtime_evidence.py")))
MINIMUM = {"claude": (2, 1, 306), "codex": (0, 163, 0)}
POLICY = "2026-10-25.3"
RECORDS = "internal/runtimepolicy/verified.json"
RELEASES = ".github/runtime-releases.json"
PIN_FILES = {"scripts/install.sh", "install.ps1", "scripts/install_test.py", "docs/install.md",
             "adapters/claude/skills/peerturn/SKILL.md", "adapters/codex/plugin/skills/peerturn/SKILL.md",
             "internal/relay/invite_page.go", "SECURITY.md", "SUPPORT.md", "GOVERNANCE.md",
             "docs/README.md", "docs/m3.md", "docs/install-lab.md"}
EDITABLE = {RECORDS, RELEASES, "README.md", "CHANGELOG.md"} | PIN_FILES
STABLE = re.compile(r"(?:0|[1-9][0-9]{0,9})\.(?:0|[1-9][0-9]{0,9})\.(?:0|[1-9][0-9]{0,9})")


def version_tuple(runtime, version):
    if runtime not in MINIMUM or not isinstance(version, str) or not STABLE.fullmatch(version):
        raise ValueError("invalid runtime/version")
    result = tuple(int(part) for part in version.split("."))
    if max(result) > 4294967295 or result < MINIMUM[runtime]:
        raise ValueError("runtime below minimum or oversized version")
    return result


def eligible(runtime, version, records):
    version_tuple(runtime, version)
    return not any(entry["version"] == version for entry in records[runtime])


def codex_identity(release):
    identities = sorted((a["name"], a["id"], a.get("digest")) for a in release["assets"])
    return hashlib.sha256(json.dumps([release["id"], release["tag_name"], identities]).encode()).hexdigest()


def validate_identity(runtime, identity):
    if not isinstance(identity, str) or (identity and not re.fullmatch(
            r"sha512-[A-Za-z0-9+/]{86}==" if runtime == "claude" else r"[0-9a-f]{64}", identity)):
        raise ValueError("invalid upstream artifact identity")


def validate_context(env):
    if env.get("GITHUB_REF") != "refs/heads/main" or env.get("GITHUB_EVENT_NAME") not in {
        "schedule", "workflow_dispatch"
    }:
        raise ValueError("qualification requires main schedule/workflow_dispatch")


def next_tag(tags):
    patches = [1]
    for tag in tags:
        match = re.fullmatch(r"v0\.1\.(0|[1-9][0-9]{0,8})-preview", tag)
        if match:
            patches.append(int(match[1]))
    return f"v0.1.{max(patches) + 1}-preview"


def update_records(root, runtime, version, evidence, platforms=None):
    version_tuple(runtime, version)
    E["validate_platforms"](platforms)
    repo = os.environ.get("GITHUB_REPOSITORY", "temirlankasen/peerturn")
    if not re.fullmatch(r"https://github\.com/" + re.escape(repo) + r"/actions/runs/[1-9][0-9]{0,19}", evidence):
        raise ValueError("invalid qualification reference")
    paths = [root / name for name in (RECORDS, "README.md", "CHANGELOG.md")]
    records = json.loads(paths[0].read_text())
    changed = False
    for platform in platforms:
        entry = next((item for item in records[runtime] if item["version"] == version
                      and all(item.get(key) == value for key, value in platform.items())), None)
        if entry and evidence in entry["reference"].split("; "):
            continue
        if entry:
            historical = entry["reference"].split("; https://github.com/", 1)[0]
            entry["reference"] = evidence if historical.startswith("https://github.com/") else historical + "; " + evidence
        else:
            records[runtime].append(dict(version=version, reference=evidence, **platform))
        changed = True
    if not changed:
        return False
    readme, changelog = (path.read_text() for path in paths[1:])
    marker = "<!-- runtime-verification -->"
    if readme.count(marker) != 1 or changelog.count("## Unreleased\n") != 1:
        raise ValueError("verification documentation markers missing")
    if len(records[runtime]) > 256 or len(json.dumps(records, indent=2).encode()) > 65535:
        raise ValueError("packaged verification capacity exceeded")
    announcement = f"Verified with {runtime} {version} ([qualification]({evidence}))."
    readme_pattern = re.compile(r"^Verified with " + re.escape(runtime) + r" [^\r\n]+$", re.MULTILINE)
    if readme_pattern.search(readme):
        readme = readme_pattern.sub(announcement, readme)
    else:
        readme = readme.replace(marker, announcement + "\n\n" + marker)
    change_pattern = re.compile(r"^- Verified with " + re.escape(runtime + " " + version) + r" [^\r\n]+$", re.MULTILINE)
    if change_pattern.search(changelog):
        changelog = change_pattern.sub("- " + announcement, changelog)
    else:
        changelog = changelog.replace("## Unreleased\n", "## Unreleased\n\n- " + announcement, 1)
    content = [json.dumps(records, indent=2) + "\n",
               readme, changelog]
    # No repository mutation happens until every input/document has been checked.
    # The actual GitHub update uses one tree/commit, never separate contents writes.
    for path, value in zip(paths, content):
        path.write_text(value)
    return True


def bump_pins(root, tag):
    if not re.fullmatch(r"v0\.1\.(0|[1-9][0-9]{0,8})-preview", tag):
        raise ValueError("invalid release pin")
    match = re.search(r"^version=(0\.1\.(?:0|[1-9][0-9]*)-preview)$",
                      (root / "scripts/install.sh").read_text(), re.MULTILINE)
    if not match:
        raise ValueError("installer pin missing")
    old, new = match[1], tag[1:]
    changes = {}
    for name in PIN_FILES | {"README.md"}:
        text = (root / name).read_text()
        if old not in text:
            raise ValueError("inconsistent release pins")
        changes[name] = text.replace(old, new)
    for name, text in changes.items():
        (root / name).write_text(text)


class APIError(Exception):
    """Public errors carry status only, never provider/GitHub response text."""
    def __init__(self, status):
        super().__init__(f"GitHub API status {status}")
        self.status = status


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise APIError(code)


class GitHub:
    def __init__(self):
        self.repo = os.environ["GITHUB_REPOSITORY"]
        if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", self.repo):
            raise ValueError("invalid repository")
        self.token = os.environ["GH_TOKEN"]
        self.opener = urllib.request.build_opener(NoRedirect)

    def request(self, path, data=None, method=None):
        if not path.startswith(("repos/", "graphql")) or ".." in path:
            raise ValueError("invalid API path")
        payload = None if data is None else json.dumps(data).encode()
        verb = method or ("GET" if data is None else "POST")
        request = urllib.request.Request("https://api.github.com/" + path, payload, method=verb,
            headers={"Authorization": "Bearer " + self.token, "Accept": "application/vnd.github+json",
                     "X-GitHub-Api-Version": "2022-11-28", "Content-Type": "application/json"})
        for attempt in range(3 if verb == "GET" else 1):
            try:
                with self.opener.open(request, timeout=30) as response:
                    body = response.read(8_000_001)
                    if len(body) > 8_000_000:
                        raise ValueError("oversized API response")
                    return json.loads(body) if body else None
            except urllib.error.HTTPError as error:
                if verb != "GET" or error.code not in {429, 500, 502, 503, 504} or attempt == 2:
                    raise APIError(error.code) from None
            except (TimeoutError, urllib.error.URLError):
                if verb != "GET" or attempt == 2:
                    raise APIError("unavailable") from None
            time.sleep(2 ** attempt)
        raise APIError("unavailable")

    def get(self, suffix):
        return self.request(f"repos/{self.repo}/{suffix}")

    def artifact(self, artifact_id):
        return E["download_artifact"](self, artifact_id)

    def put(self, suffix, data, method="POST"):
        return self.request(f"repos/{self.repo}/{suffix}", data, method)

    def pages(self, suffix, key=None, max_pages=100):
        result = []
        for page in range(1, max_pages + 1):
            separator = "&" if "?" in suffix else "?"
            data = self.get(f"{suffix}{separator}per_page=100&page={page}")
            values = data[key] if key else data
            result.extend(values)
            if len(values) < 100:
                return result
        raise ValueError("pagination bound exceeded")


def title(runtime, version):
    version_tuple(runtime, version)
    return f"Runtime qualification: {runtime} {version}"


def issue_for(api, runtime, version):
    name = title(runtime, version)
    return next((item for item in api.pages("issues?state=all&creator=github-actions%5Bbot%5D", max_pages=10)
                 if "pull_request" not in item and item["title"] == name
                 and item["user"]["login"] == "github-actions[bot]"), None)


def output(name, value):
    with open(os.environ["GITHUB_OUTPUT"], "a") as stream:
        stream.write(f"{name}={value}\n")
