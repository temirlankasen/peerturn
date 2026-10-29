"""Read bounded sanitized qualification receipts; platform names are never inferred from runner labels."""
import io
import json
import re
import urllib.error
import urllib.parse
import urllib.request
import zipfile


ADAPTER_REVISION = "2026-10-25.2"
PROFILE = "native-preview/v1"
CHECKS = {"artifact_identity", "setup", "native_binding", "late_daemon_mcp",
          "owner_invite_claude_to_codex", "owner_invite_codex_to_claude", "selected_handle",
          "peer_invite_refused_claude", "peer_invite_refused_codex", "codex_review_refused",
          "coauthor_claude_to_codex", "coauthor_codex_to_claude", "inform_no_wake",
          "inform_notice", "inform_body", "revoke", "teardown"}


class ArtifactRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        parsed = urllib.parse.urlsplit(newurl)
        if (urllib.parse.urlsplit(req.full_url).hostname != "api.github.com" or parsed.scheme != "https"
                or not re.fullmatch(r"productionresultssa[0-9]+\.blob\.core\.windows\.net", parsed.hostname or "")
                or parsed.username or parsed.password or parsed.port not in (None, 443)):
            raise ValueError("untrusted artifact redirect")
        # The signed storage URL needs no GitHub credential. A second redirect is rejected.
        return urllib.request.Request(newurl)


def download_artifact(api, artifact_id):
    if type(artifact_id) is not int or not 0 < artifact_id < 2**63:
        raise ValueError("invalid artifact identifier")
    request = urllib.request.Request(f"https://api.github.com/repos/{api.repo}/actions/artifacts/{artifact_id}/zip",
        headers={"Authorization": "Bearer " + api.token, "Accept": "application/vnd.github+json",
                 "X-GitHub-Api-Version": "2022-11-28"})
    try:
        with urllib.request.build_opener(ArtifactRedirect).open(request, timeout=30) as response:
            data = response.read(1_000_001)
    except (urllib.error.URLError, TimeoutError):
        raise ValueError("qualification receipt unavailable") from None
    if len(data) > 1_000_000:
        raise ValueError("oversized qualification archive")
    return data


def unique_fields(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate qualification field")
        result[key] = value
    return result


def summary(data):
    if not isinstance(data, bytes) or len(data) > 1_000_000:
        raise ValueError("invalid qualification archive")
    try:
        with zipfile.ZipFile(io.BytesIO(data)) as archive:
            members = archive.infolist()
            if len(members) != 1 or members[0].filename != "qualification-summary.json" or members[0].file_size > 65536:
                raise ValueError("invalid qualification archive contents")
            receipt = json.loads(archive.read(members[0]), object_pairs_hook=unique_fields)
            if not isinstance(receipt, dict):
                raise ValueError("invalid qualification summary")
            return receipt
    except (zipfile.BadZipFile, UnicodeDecodeError, RuntimeError, NotImplementedError):
        raise ValueError("invalid qualification archive") from None


def validate_platforms(platforms):
    if not isinstance(platforms, list) or not 1 <= len(platforms) <= 16:
        raise ValueError("missing qualified platforms")
    seen = set()
    for entry in platforms:
        if (not isinstance(entry, dict) or set(entry) != {"os", "arch", "adapter_revision", "profile"}
                or entry["os"] not in ("linux", "darwin") or entry["arch"] not in ("amd64", "arm64")
                or entry["adapter_revision"] != ADAPTER_REVISION or entry["profile"] != PROFILE):
            raise ValueError("invalid qualified platform")
        identity = (entry["os"], entry["arch"])
        if identity in seen:
            raise ValueError("duplicate qualified platform")
        seen.add(identity)


def qualified_platforms(api, run, runtime, version):
    artifacts = api.pages(f"actions/runs/{run['id']}/artifacts", "artifacts")
    if not isinstance(artifacts, list) or len(artifacts) > 256 or any(not isinstance(item, dict) for item in artifacts):
        raise ValueError("invalid qualification artifacts")
    platforms = []
    npm_identity = None
    for runner, os_name in (("ubuntu-latest", "linux"), ("macos-latest", "darwin")):
        name = f"qualification-{runtime}-{version}-{runner}"
        matches = [item for item in artifacts if item.get("name") == name]
        if len(matches) != 1:
            raise ValueError("qualification receipt missing or ambiguous")
        artifact = matches[0]
        origin = artifact.get("workflow_run", {})
        if (not isinstance(origin, dict) or artifact.get("expired") is not False or type(artifact.get("size_in_bytes")) is not int
                or not 0 < artifact["size_in_bytes"] <= 1_000_000 or origin.get("id") != run["id"]
                or origin.get("head_sha") != run["head_sha"] or origin.get("head_branch") != "main"):
            raise ValueError("qualification receipt origin mismatch")
        receipt = summary(api.artifact(artifact["id"]))
        if (not isinstance(receipt, dict) or set(receipt) != {"schema", "runtime", "version", "passed", "checks", "identities"}
                or type(receipt["schema"]) is not int or receipt["schema"] != 1 or receipt["runtime"] != runtime
                or receipt["version"] != version or receipt["passed"] is not True
                or not isinstance(receipt["checks"], dict) or set(receipt["checks"]) != CHECKS
                or any(value is not True for value in receipt["checks"].values())):
            raise ValueError("qualification receipt incomplete")
        identities = receipt["identities"]
        if (not isinstance(identities, dict) or identities.get("peerturn_commit") != run["head_sha"]
                or identities.get("os") != os_name or not isinstance(identities.get("runtimes"), dict)
                or not isinstance(identities.get("arch"), str)
                or not isinstance(identities["runtimes"].get(runtime), dict)
                or identities["runtimes"].get(runtime, {}).get("version") != version):
            raise ValueError("qualification receipt identity mismatch")
        claude = identities["runtimes"].get("claude")
        keys = ("npm_lock_sha256", "npm_tree_sha256")
        if not isinstance(claude, dict) or any(
                not isinstance(claude.get(key), str) or not re.fullmatch(r"[0-9a-f]{64}", claude[key])
                for key in keys + ("npm_installed_tree_sha256",)):
            raise ValueError("qualification npm dependency identity missing")
        current = tuple(claude[key] for key in keys)
        if npm_identity is not None and npm_identity != current:
            raise ValueError("platforms qualified different npm dependency trees")
        npm_identity = current
        arch = {"aarch64": "arm64", "arm64": "arm64", "x86_64": "amd64"}.get(identities.get("arch"))
        platforms.append({"os": os_name, "arch": arch, "adapter_revision": ADAPTER_REVISION, "profile": PROFILE})
    validate_platforms(platforms)
    return platforms
