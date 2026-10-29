"""Install exact public runtime artifacts before any provider credential exists."""
import hashlib
import io
import json
import os
from pathlib import Path
import platform
import runpy
import subprocess
import sys
import tarfile
import tempfile
import urllib.request
import urllib.parse


C = runpy.run_path(str(Path(__file__).with_name("runtime_common.py")))
N = runpy.run_path(str(Path(__file__).with_name("runtime_npm.py")))


def download(url, hosts, limit=150_000_000):
    class Redirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, req, fp, code, msg, headers, newurl):
            validate(newurl)
            return super().redirect_request(req, fp, code, msg, headers, newurl)

    def validate(value):
        parsed = urllib.parse.urlsplit(value)
        if parsed.scheme != "https" or parsed.hostname not in hosts or parsed.username or parsed.password:
            raise ValueError("untrusted artifact URL")

    validate(url)
    with urllib.request.build_opener(Redirect).open(url, timeout=120) as response:
        data = response.read(limit + 1)
    if len(data) > limit:
        raise ValueError("oversized upstream artifact")
    return data


def command(argv, cwd=None):
    environment = {key: value for key, value in os.environ.items()
                   if key in {"PATH", "HOME", "TMPDIR", "TMP", "TEMP", "LANG", "LC_ALL"}}
    result = subprocess.run(argv, env=environment, cwd=cwd, capture_output=True, timeout=180, check=False)
    if result.returncode:
        raise ValueError("runtime installation/version probe failed")
    return result.stdout.decode().strip()


def source_identity(api, runtime, version):
    C["version_tuple"](runtime, version)
    if runtime == "claude":
        metadata = json.loads(download("https://registry.npmjs.org/@anthropic-ai%2fclaude-code/" + version,
                                       {"registry.npmjs.org"}, 2_000_000))
        if metadata["name"] != "@anthropic-ai/claude-code" or metadata["version"] != version:
            raise ValueError("package identity mismatch")
        identity = metadata["dist"]["integrity"]
    else:
        release = api.request(f"repos/openai/codex/releases/tags/rust-v{version}")
        if release["draft"] or release["prerelease"] or release["tag_name"] != "rust-v" + version:
            raise ValueError("invalid CLI release")
        identity = C["codex_identity"](release)
    C["validate_identity"](runtime, identity)
    return identity


def install(runtime, version, root, api, expected=None, npm_lock=None):
    C["version_tuple"](runtime, version)
    if runtime == "claude":
        tree_identity = N["install"](version, expected, npm_lock, root, command)
        binary = root / "node_modules/.bin/claude"
        (root / "bin/claude").symlink_to(binary.resolve())
        source = expected
    else:
        os_name = {"Linux": "unknown-linux-musl", "Darwin": "apple-darwin"}[platform.system()]
        arch = {"x86_64": "x86_64", "amd64": "x86_64", "arm64": "aarch64", "aarch64": "aarch64"}[platform.machine()]
        name = f"codex-{arch}-{os_name}.tar.gz"
        release = api.request(f"repos/openai/codex/releases/tags/rust-v{version}")
        if release["draft"] or release["prerelease"] or release["tag_name"] != "rust-v" + version:
            raise ValueError("invalid CLI release")
        if expected is not None and C["codex_identity"](release) != expected:
            raise ValueError("detected CLI release identity changed")
        assets = [asset for asset in release["assets"] if asset["name"] == name]
        if len(assets) != 1:
            raise ValueError("runtime asset missing/ambiguous")
        asset = assets[0]
        data = download(asset["browser_download_url"], {"github.com", "release-assets.githubusercontent.com"})
        source = "sha256:" + hashlib.sha256(data).hexdigest()
        if source != asset.get("digest"):
            raise ValueError("GitHub release artifact digest missing/mismatch")
        with tarfile.open(fileobj=io.BytesIO(data), mode="r:gz") as archive:
            members = [member for member in archive.getmembers() if member.isfile()
                       and Path(member.name).name.startswith("codex")]
            if len(members) != 1 or members[0].size > 200_000_000:
                raise ValueError("invalid runtime archive")
            binary = root / "bin/codex"
            with archive.extractfile(members[0]) as stream:
                binary.write_bytes(stream.read(200_000_001))
        binary.chmod(0o700)
    actual = command([str(binary), "--version"])
    expected = version + " (Claude Code)" if runtime == "claude" else "codex-cli " + version
    if actual != expected:
        raise ValueError("installed runtime version mismatch")
    result = {"version": version, "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
              "source_integrity": source}
    if runtime == "claude":
        result.update(tree_identity)
    return result


def main():
    C["validate_context"](os.environ)
    resolve = sys.argv[1:2] == ["identity"]
    args = sys.argv[2:] if resolve else sys.argv[1:]
    if len(args) != (3 if resolve else 2):
        raise ValueError("invalid installer arguments")
    runtime, version = args[:2]
    C["version_tuple"](runtime, version)
    versions = {name: ".".join(map(str, minimum)) for name, minimum in C["MINIMUM"].items()}
    versions[runtime] = version
    api = C["GitHub"]()
    if resolve:
        expected = args[2] if len(args) == 3 else ""
        C["validate_identity"](runtime, expected)
        artifacts = {name: source_identity(api, name, value) for name, value in versions.items()}
        if expected and artifacts[runtime] != expected:
            raise ValueError("detected candidate changed before qualification")
        with tempfile.TemporaryDirectory(prefix="qualification-npm-") as directory:
            npm_lock = N["resolve"](versions["claude"], artifacts["claude"], Path(directory), command)
        Path("qualification-npm-lock.json").write_text(json.dumps(
            N["evidence"](npm_lock, versions["claude"], artifacts["claude"]), indent=2) + "\n")
        C["output"]("artifacts", json.dumps(artifacts, separators=(",", ":")))
        C["output"]("npm_lock", json.dumps(npm_lock, separators=(",", ":")))
        return
    artifacts = json.loads(os.environ["QUAL_ARTIFACTS"])
    if set(artifacts) != set(versions):
        raise ValueError("incomplete pinned runtime pair")
    for name, identity in artifacts.items():
        C["validate_identity"](name, identity)
        if not identity:
            raise ValueError("missing paired artifact identity")
    root = Path(os.environ["RUNNER_TEMP"]) / "qualification-runtimes"
    root.mkdir(mode=0o700)
    (root / "bin").mkdir(mode=0o700)
    npm_lock = os.environ["QUAL_NPM_LOCK"]
    N["unbundle"](npm_lock, versions["claude"], artifacts["claude"])
    identities = {name: install(name, value, root, api, artifacts[name], npm_lock) for name, value in versions.items()}
    path = root / "identity.json"
    path.write_text(json.dumps({"runtimes": identities, "artifacts": artifacts}, indent=2) + "\n")
    with open(os.environ["GITHUB_PATH"], "a") as stream:
        stream.write(str(root / "bin") + "\n")
    with open(os.environ["GITHUB_ENV"], "a") as stream:
        stream.write("PEERTURN_QUAL_IDENTITY=" + str(path) + "\n")
    print("Installed and hashed exact runtime artifacts")


if __name__ == "__main__":
    try:
        main()
    except (ValueError, KeyError, OSError, C["APIError"], subprocess.SubprocessError):
        print("Runtime artifact qualification failed", file=sys.stderr)
        sys.exit(1)
