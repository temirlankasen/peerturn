"""Export only reviewed HEAD blobs into a private-policy-checked root commit."""
import os
from pathlib import Path, PurePosixPath
import re
import runpy
import shutil
import subprocess
import sys
import time


SCRIPTS = Path(__file__).resolve().parent
POLICY = runpy.run_path(str(SCRIPTS / "public_policy.py"))
GIT = shutil.which("git")
ENV = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
PUBLIC_AUTHOR = "Temirlan Kasen <48218803+temirlankasen@users.noreply.github.com>"


def git(root, *args, identity=None, timestamp=None, input=None):
    if GIT is None:
        raise ValueError("git is required")
    environment = dict(ENV)
    if identity:
        author, email = identity
        environment.update(GIT_AUTHOR_NAME=author, GIT_AUTHOR_EMAIL=email,
                           GIT_COMMITTER_NAME=author, GIT_COMMITTER_EMAIL=email,
                           GIT_AUTHOR_DATE="@" + timestamp + " +0000",
                           GIT_COMMITTER_DATE="@" + timestamp + " +0000")
    return subprocess.run(
        [GIT, "--no-optional-locks", "-C", str(root), "-c", "core.hooksPath=/dev/null",
         "-c", "commit.gpgsign=false", "-c", "core.autocrlf=false",
         "-c", "core.safecrlf=false", *args],
        check=True, capture_output=True, timeout=60, env=environment, input=input,
    ).stdout


def destination_path(value, source):
    raw = Path(value)
    if ".." in raw.parts or not value or "\0" in value:
        raise ValueError("unsafe destination")
    raw = Path(os.path.abspath(raw))
    # macOS exposes these fixed system aliases; arbitrary links stay forbidden.
    aliases = {Path("/tmp"): Path("/private/tmp"), Path("/var"): Path("/private/var")}
    for part in (raw, *raw.parents):
        if part.is_symlink() and (part not in aliases or part.resolve() != aliases[part]):
            raise ValueError("symlink destination")
    destination = raw.resolve(strict=False)
    if destination.exists() or not destination.parent.is_dir():
        raise ValueError("destination must be new, with an existing parent")
    if destination == source or source in destination.parents:
        raise ValueError("destination must be outside the source tree")
    return destination


def snapshot(source, head, excluded):
    files = []
    for entry in git(source, "ls-tree", "-rz", "--full-tree", head).split(b"\0"):
        if not entry:
            continue
        metadata, raw_name = entry.split(b"\t", 1)
        mode, kind, oid = metadata.split()
        name = raw_name.decode("utf-8")
        parts = PurePosixPath(name).parts
        if (name.startswith("/") or "\\" in name or any(ord(c) < 32 for c in name)
                or any(part in {".", "..", ".git"} for part in parts)):
            raise ValueError("unsafe source path")
        if excluded(name):
            continue
        if kind != b"blob" or mode not in {b"100644", b"100755"}:
            raise ValueError("unsupported source object")
        files.append((name, git(source, "cat-file", "blob", oid.decode("ascii")), mode, oid))
    if not files:
        raise ValueError("empty snapshot")
    return files


def filtered_tree(destination, files):
    root = {}
    for name, _, mode, oid in files:
        parts = PurePosixPath(name).parts
        directory = root
        for part in parts[:-1]:
            directory = directory.setdefault(part, {})
        directory[parts[-1]] = (mode, b"blob", oid)

    def tree_hash(entries):
        records = []
        for name, entry in entries.items():
            if isinstance(entry, dict):
                mode, kind, oid = b"040000", b"tree", tree_hash(entry)
            else:
                mode, kind, oid = entry
            records.append(mode + b" " + kind + b" " + oid + b"\t" + name.encode("utf-8") + b"\0")
        # Expected source OIDs may be absent if a clean filter changed a copied blob.
        return git(destination, "mktree", "-z", "--missing", input=b"".join(records)).strip()

    return tree_hash(root)


def options(args):
    values = {"--author": PUBLIC_AUTHOR, "--private-markers": ENV.get("PEERTURN_PRIVATE_MARKERS"),
              "--timestamp": str(int(time.time()))}
    seen, destinations = set(), []
    args = list(args)
    while args:
        value = args.pop(0)
        if value in values and value not in seen and args:
            seen.add(value)
            values[value] = args.pop(0)
        elif value.startswith("--"):
            raise ValueError("invalid export options")
        else:
            destinations.append(value)
    if len(destinations) != 1 or not values["--private-markers"]:
        raise ValueError("destination and private markers are required")
    identity = re.fullmatch(r"([^<>\r\n\x00-\x1f]+) <([^<>\s]+@[^<>\s]+)>", values["--author"])
    if identity is None or len(values["--author"]) > 320:
        raise ValueError("invalid explicit public identity")
    if identity[1] != identity[1].strip() or not POLICY["PATTERNS"]["email address"].fullmatch(identity[2]):
        raise ValueError("invalid explicit public identity")
    timestamp = values["--timestamp"]
    if not re.fullmatch(r"(?:0|[1-9][0-9]{0,11})", timestamp) or int(timestamp) > 253402300799:
        raise ValueError("timestamp must be Unix seconds through year 9999")
    return destinations[0], values["--private-markers"], identity.groups(), timestamp


def scan(destination, marker_path, commit=False):
    args = [sys.executable, "-I", "-B", str(SCRIPTS / "public_scan.py"),
            "--cached", "--strict", "--private-markers", str(marker_path)]
    if commit:
        args.append("--commit")
    result = subprocess.run(args, cwd=destination, env=ENV, check=False,
                            capture_output=True, timeout=120)
    if result.returncode:
        sys.stderr.buffer.write(result.stderr)
        raise ValueError("strict publication scan failed")
    if commit:
        sys.stdout.buffer.write(result.stdout)


def main(args):
    destination_value, marker_value, identity, timestamp = options(args)
    source = Path(git(SCRIPTS.parent, "rev-parse", "--show-toplevel").decode("utf-8").strip()).resolve()
    if source != SCRIPTS.parent:
        raise ValueError("exporter must belong to the source repository")
    destination = destination_path(destination_value, source)
    excluded, _ = POLICY["exclusions"](SCRIPTS / "public-exclude.txt")
    marker_path = Path(marker_value).absolute()
    if marker_path.is_symlink():
        raise ValueError("private marker policy must be a regular file")
    marker_path = marker_path.resolve(strict=True)
    if marker_path in {source, destination} or destination in marker_path.parents:
        raise ValueError("private markers must remain outside the export")
    if source in marker_path.parents:
        relative = marker_path.relative_to(source).as_posix()
        if not excluded(relative) or git(source, "ls-files", "--cached", "-z", "--", ":(literal)" + relative):
            raise ValueError("source markers must be excluded and untracked")
    markers = POLICY["private_markers"](marker_path)
    author, email = identity
    message = ("feat: initial public preview of PeerTurn\n\nSigned-off-by: " + author +
               " <" + email + ">\n")
    if POLICY["findings"](message, markers=markers):
        raise ValueError("private public-commit metadata")
    head = git(source, "rev-parse", "--verify", "HEAD^{commit}").decode("ascii").strip()
    if git(source, "status", "--porcelain=v1", "--untracked-files=no", "-z"):
        raise ValueError("tracked source files must match HEAD")
    untracked = git(source, "ls-files", "--others", "--exclude-standard", "-z")
    ignored = git(source, "ls-files", "--others", "--ignored", "--exclude-standard", "-z")
    if any(not excluded(name.decode("utf-8")) for name in (untracked + ignored).split(b"\0") if name):
        raise ValueError("untracked or ignored files outside the private boundary")
    files = snapshot(source, head, excluded)
    destination.mkdir(mode=0o700)
    for name, data, mode, _ in files:
        path = destination / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(data)
        path.chmod(0o755 if mode == b"100755" else 0o644)
    object_format = git(source, "rev-parse", "--show-object-format").decode("ascii").strip()
    git(destination, "init", "-q", "--initial-branch=main", "--template=",
        "--object-format=" + object_format)
    expected_tree = filtered_tree(destination, files)
    # Only HEAD blobs were copied; force-add keeps committed ignored blobs visible.
    git(destination, "add", "--force", "--all", "--", ".")
    scan(destination, marker_path)
    git(destination, "commit", "-q", "--no-gpg-sign", "--cleanup=verbatim", "-m", message,
        identity=identity, timestamp=timestamp)
    if git(destination, "rev-parse", "HEAD^{tree}").strip() != expected_tree:
        raise ValueError("public tree does not match filtered source HEAD")
    scan(destination, marker_path, commit=True)
    if git(destination, "rev-list", "--count", "HEAD").strip() != b"1" or git(destination, "remote"):
        raise ValueError("fresh-history invariant failed")
    print("Source commit: " + head)
    print("Public HEAD snapshot exported with one initial commit; no remote configured.")


if __name__ == "__main__":
    try:
        main(sys.argv[1:])
    except (OSError, UnicodeError, ValueError, subprocess.SubprocessError):
        sys.exit("export-public: export failed; no publication was attempted")
