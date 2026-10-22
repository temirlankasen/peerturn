"""Fail closed while scanning current public candidates or exact index blobs."""
import os
from pathlib import Path, PurePosixPath
import runpy
import shutil
import subprocess
import sys


POLICY = runpy.run_path(str(Path(__file__).with_name("public_policy.py")))
GIT = shutil.which("git")
ENV = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}


def git(*args):
    if GIT is None:
        raise ValueError("git is required")
    return subprocess.run([GIT, *args], check=True, capture_output=True,
                          timeout=30, env=ENV).stdout


def options(args):
    flags, marker_file = set(), ENV.get("PEERTURN_PRIVATE_MARKERS")
    args = list(args)
    while args:
        option = args.pop(0)
        if option in flags:
            raise ValueError("duplicate publication option")
        flags.add(option)
        if option == "--private-markers" and args:
            marker_file = args.pop(0)
        elif option not in {"--cached", "--strict-index", "--strict", "--commit"}:
            raise ValueError("invalid publication options")
    markers = POLICY["private_markers"](Path(marker_file)) if marker_file is not None else ()
    return "--cached" in flags, bool({"--strict", "--strict-index"} & flags), "--commit" in flags, markers


def main(args):
    cached, strict, commit, markers = options(args)
    os.chdir(git("rev-parse", "--show-toplevel").decode("utf-8").strip())
    # Fresh public exports have one root commit; inspect their identity by default.
    # A shallow CI checkout also shows one commit, but it is a contributor's own
    # public identity, so only an explicit --commit inspects it.
    shallow = git("rev-parse", "--is-shallow-repository").strip() == b"true"
    commit = commit or (not shallow and len(git("rev-list", "--max-count=2", "--all").splitlines()) == 1)
    excluded, _ = POLICY["exclusions"](Path(__file__).with_name("public-exclude.txt"))
    entries = {}
    for entry in git("ls-files", "--stage", "-z").split(b"\0"):
        if not entry:
            continue
        meta, raw_name = entry.split(b"\t", 1)
        mode, oid, stage = meta.split()
        if stage != b"0":
            raise ValueError("resolve index conflicts before publication")
        name = raw_name.decode("utf-8")
        if name in entries:
            raise ValueError("duplicate index path")
        entries[name] = (mode, oid)
    if not cached:
        for name in git("ls-files", "--others", "--exclude-standard", "-z").split(b"\0"):
            if name:
                entries.setdefault(name.decode("utf-8"), None)
    failures, checked, skipped = [], 0, 0
    for number, (name, entry) in enumerate(sorted(entries.items()), 1):
        parts = PurePosixPath(name).parts
        if name.startswith("/") or any(part in {".", "..", ".git"} for part in parts):
            failures.append(f"entry {number}: unsafe publication path")
            continue
        if excluded(name):
            if strict:
                failures.append(f"entry {number}: excluded private path is indexed")
            else:
                skipped += 1
            continue
        labels = POLICY["findings"](name, markers=markers)
        if labels:
            failures.append(f"entry {number}: path contains {', '.join(labels)}")
        if cached:
            mode, oid = entry
            if mode not in (b"100644", b"100755"):
                failures.append(f"entry {number}: unsupported index object")
                continue
            data = git("cat-file", "blob", oid.decode("ascii"))
        else:
            path = Path(name)
            if any(parent.is_symlink() for parent in (path, *path.parents)):
                failures.append(f"entry {number}: symlink is not publishable")
                continue
            data = POLICY["regular_bytes"](path)
        if b"\0" in data:
            failures.append(f"entry {number}: unexpected binary file requires review")
            continue
        text = data.decode("utf-8")
        checked += 1
        for line_number, line in enumerate(text.splitlines(), 1):
            labels = POLICY["findings"](line, name, markers=markers)
            if labels:
                failures.append(f"entry {number}, line {line_number}: {', '.join(labels)}")
    if commit:
        metadata = git("cat-file", "commit", "HEAD").decode("utf-8")
        headers = metadata.split("\n\n", 1)[0].splitlines()
        for role in ("author", "committer"):
            dates = [line.rsplit(" ", 2) for line in headers if line.startswith(role + " ")]
            if (len(dates) != 1 or len(dates[0]) != 3 or not dates[0][1].isascii()
                    or not dates[0][1].isdigit() or dates[0][2] != "+0000"):
                failures.append("commit metadata: " + role + " date must use UTC")
        for line_number, line in enumerate(metadata.splitlines(), 1):
            labels = POLICY["findings"](line, markers=markers)
            if labels:
                failures.append(f"commit metadata, line {line_number}: {', '.join(labels)}")
    if skipped:
        print(f"PREPARATION ONLY: {skipped} declared private paths omitted; require --strict-index before publication.")
    if failures:
        for failure in failures[:80]:
            print(failure, file=sys.stderr)
        raise ValueError(f"{len(failures)} findings in {checked} text files")
    print(f"publish-check: {checked} public text files passed ({'index' if cached else 'working tree'})")
    print("HEAD commit metadata checked." if commit else "Commit metadata was not requested (--commit).")
    print("Earlier Git history is not inspected; use a reviewed clean publication history.")


if __name__ == "__main__":
    try:
        main(sys.argv[1:])
    except (OSError, UnicodeError, ValueError, subprocess.SubprocessError):
        sys.exit("publish-check: scan failed; review input, file access, and findings")
