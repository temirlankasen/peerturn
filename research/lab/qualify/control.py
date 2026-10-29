"""Run only from a disposable native tool child; never contains provider credentials."""
import json
import os
from pathlib import Path
import re
import subprocess
import sys


def main():
    root = Path(__file__).resolve().parent
    plan = json.loads((root / "plan.json").read_text())
    binary = plan["binary"]
    if not Path(binary).is_absolute() or len(sys.argv) < 2:
        return 2
    operation = sys.argv[1]
    if operation == "effect" and len(sys.argv) == 3:
        marker = sys.argv[2]
        if not re.fullmatch(r"QUAL_[A-Z0-9_]{1,48}", marker):
            return 2
        path = root / "effects" / marker
        path.parent.mkdir(mode=0o700, exist_ok=True)
        with path.open("x", opener=lambda p, f: os.open(p, f, 0o600)) as out:
            out.write(marker + "\n")
        print("QUAL_EFFECT_RECORDED")
        return 0
    commands = {
        "init": ["init", "--relay", plan["relay"], "--file-permissions"],
        "bind": ["session", "bind", "--current", "--json"],
        "status": ["status", "--current", "--json"],
        "invite": ["invite", "create", "--current", "--scope", "coauthor", "--json"],
    }
    if operation not in commands or len(sys.argv) != 2:
        return 2
    argv = commands[operation]
    if operation == "init":
        if plan["runtime"] == "codex":
            argv += ["--codex-current"]
        else:
            argv += ["--session", plan["alias"]]
    output = root / "results" / (operation + ".json")
    output.parent.mkdir(mode=0o700, exist_ok=True)
    if output.exists():
        return 2
    with subprocess.Popen([binary, *argv], stdin=subprocess.DEVNULL,
                          stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                          start_new_session=False) as proc:
        try:
            data, _ = proc.communicate(timeout=45)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.communicate()
            return 3
    if len(data) > 1024 * 1024:
        return 3
    try:
        result = json.loads(data)
    except (ValueError, UnicodeError):
        return 3
    with output.open("x", opener=lambda p, f: os.open(p, f, 0o600)) as out:
        json.dump({"exit": proc.returncode, "result": result}, out)
    print("QUAL_CONTROL_COMPLETED")
    return 0 if proc.returncode == 0 else 1


if __name__ == "__main__":
    raise SystemExit(main())
