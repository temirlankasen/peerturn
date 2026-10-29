"""Resolve one integrity-pinned npm tree, then install it without resolving again."""
import base64
import hashlib
import json
from pathlib import Path
import re
import shutil
import urllib.parse


PACKAGE = "@anthropic-ai/claude-code"
LIMIT = 65536
PATH = re.compile(r"node_modules/(?:@[a-z0-9_.-]+/)?[a-z0-9][a-z0-9_.-]*(?:/node_modules/(?:@[a-z0-9_.-]+/)?[a-z0-9][a-z0-9_.-]*)*")


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":")).encode()


def digest(data):
    return hashlib.sha256(data).hexdigest()


def unique_fields(pairs):
    value = {}
    for key, entry in pairs:
        if key in value:
            raise ValueError("duplicate npm field")
        value[key] = entry
    return value


def manifest(version):
    return {"name": "peerturn-runtime-qualification", "version": "1.0.0", "private": True,
            "dependencies": {PACKAGE: version}}


def identity(data, version, source):
    if not isinstance(data, bytes) or not 0 < len(data) <= LIMIT:
        raise ValueError("oversized/missing npm lock")
    lock = json.loads(data, object_pairs_hook=unique_fields)
    if (not isinstance(lock, dict) or type(lock.get("lockfileVersion")) is not int
            or lock["lockfileVersion"] != 3 or not isinstance(lock.get("packages"), dict)):
        raise ValueError("unsupported npm lock")
    packages = lock["packages"]
    root = packages.get("", {})
    if (not isinstance(root, dict) or root.get("dependencies") != {PACKAGE: version}
            or root.get("name") != manifest(version)["name"] or root.get("version") != "1.0.0"):
        raise ValueError("npm root mismatch")
    for path, package in packages.items():
        if path == "":
            continue
        if (not PATH.fullmatch(path) or not isinstance(package, dict) or package.get("link")
                or not isinstance(package.get("version"), str) or not package["version"]):
            raise ValueError("invalid npm package")
        integrity = package.get("integrity", "")
        if not isinstance(integrity, str) or not re.fullmatch(r"sha512-[A-Za-z0-9+/]{86}==", integrity):
            raise ValueError("npm dependency integrity missing")
        resolved = package.get("resolved")
        if not isinstance(resolved, str):
            raise ValueError("npm dependency source missing")
        parsed = urllib.parse.urlsplit(resolved)
        if (parsed.scheme != "https" or parsed.hostname != "registry.npmjs.org"
                or parsed.username or parsed.password or parsed.port not in (None, 443)
                or parsed.query or parsed.fragment or not parsed.path.endswith(".tgz")):
            raise ValueError("untrusted npm dependency source")
    direct = packages.get("node_modules/" + PACKAGE, {})
    if direct.get("version") != version or direct.get("integrity") != source:
        raise ValueError("npm candidate identity mismatch")
    return {"npm_lock_sha256": digest(data), "npm_tree_sha256": digest(canonical(packages))}


def bundle(data, version, source):
    return dict(identity(data, version, source), package_lock=base64.b64encode(data).decode())


def unbundle(value, version, source):
    if not isinstance(value, str) or len(value) > 90000:
        raise ValueError("invalid npm contracts output")
    supplied = json.loads(value, object_pairs_hook=unique_fields)
    if not isinstance(supplied, dict) or set(supplied) != {"package_lock", "npm_lock_sha256", "npm_tree_sha256"}:
        raise ValueError("incomplete npm contracts output")
    data = base64.b64decode(supplied["package_lock"], validate=True)
    actual = identity(data, version, source)
    if any(supplied[key] != entry for key, entry in actual.items()):
        raise ValueError("npm contracts output digest mismatch")
    return data, actual


def evidence(value, version, source):
    data, _ = unbundle(json.dumps(value), version, source)
    return dict(value, schema=1, claude_version=version, source_integrity=source,
                package_json=manifest(version), resolved_tree=json.loads(data)["packages"])


def prepare(root, version):
    (root / "package.json").write_bytes(canonical(manifest(version)))
    for name in ("user-npmrc", "global-npmrc"):
        (root / name).write_text("")
    npm = shutil.which("npm")
    if not npm:
        raise ValueError("npm required")
    return [npm], ["--prefix", str(root), "--ignore-scripts", "--no-audit", "--no-fund",
                   "--registry", "https://registry.npmjs.org", "--cache", str(root / "npm-cache"),
                   "--userconfig", str(root / "user-npmrc"), "--globalconfig", str(root / "global-npmrc")]


def resolve(version, source, root, command):
    npm, flags = prepare(root, version)
    command(npm + ["install", "--package-lock-only"] + flags, cwd=root)
    if (root / "node_modules").exists():
        raise ValueError("npm contracts unexpectedly installed packages")
    return bundle((root / "package-lock.json").read_bytes(), version, source)


def install(version, source, value, root, command):
    data, result = unbundle(value, version, source)
    npm, flags = prepare(root, version)
    lock = root / "package-lock.json"
    lock.write_bytes(data)
    command(npm + ["ci"] + flags, cwd=root)
    if lock.read_bytes() != data:
        raise ValueError("npm ci changed pinned lock")
    tree = command(npm + ["ls", "--all", "--json", "--long"] + flags, cwd=root)
    # Compare actual package locations/versions with the same resolved lock on each OS.
    packages = json.loads(data)["packages"]
    installed = {}
    def visit(node):
        if not isinstance(node, dict) or node.get("problems"):
            raise ValueError("invalid installed npm tree")
        dependencies = node.get("dependencies", {})
        if not isinstance(dependencies, dict):
            raise ValueError("invalid installed npm dependencies")
        for dependency in dependencies.values():
            if dependency == {}:
                continue
            if not isinstance(dependency, dict) or not isinstance(dependency.get("path"), str):
                raise ValueError("installed npm package path missing")
            relative = Path(dependency["path"]).resolve().relative_to(root.resolve()).as_posix()
            expected = packages.get(relative, {})
            if dependency.get("version") != expected.get("version") or not expected:
                raise ValueError("installed npm dependency mismatch")
            installed[relative] = expected
            visit(dependency)
    visit(json.loads(tree, object_pairs_hook=unique_fields))
    if "node_modules/" + PACKAGE not in installed:
        raise ValueError("installed npm candidate missing")
    result["npm_installed_tree_sha256"] = digest(canonical(installed))
    (root / "npm-tree.json").write_bytes(canonical({"resolved": packages, "installed": installed}))
    return result


def verify(root, version, source, value):
    data, result = unbundle(value, version, source)
    if (root / "package-lock.json").read_bytes() != data:
        raise ValueError("installed npm lock differs from contracts")
    path = root / "npm-tree.json"
    if path.stat().st_size > LIMIT * 2:
        raise ValueError("oversized installed npm tree")
    tree = json.loads(path.read_bytes(), object_pairs_hook=unique_fields)
    packages = json.loads(data)["packages"]
    if (not isinstance(tree, dict) or set(tree) != {"resolved", "installed"}
            or tree["resolved"] != packages or not isinstance(tree["installed"], dict)
            or "node_modules/" + PACKAGE not in tree["installed"]):
        raise ValueError("installed npm tree differs from contracts")
    if any(not path or package != packages.get(path) for path, package in tree["installed"].items()):
        raise ValueError("unlocked installed npm dependency")
    result["npm_installed_tree_sha256"] = digest(canonical(tree["installed"]))
    return result
