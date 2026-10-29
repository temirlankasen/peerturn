"""Model-free check of schemas emitted by the actual installed Codex binary."""
import json
from pathlib import Path
import sys
import tempfile


HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
from core import Failure, child_environment, command, executable, private_json, require, runtime_pair


FIELDS = {
    "InitializeParams": ("clientInfo", "capabilities"),
    "InitializeResponse": ("userAgent",),
    "ThreadLoadedListResponse": ("data", "nextCursor"),
    "ThreadReadParams": ("threadId", "includeTurns"),
    "ThreadReadResponse": ("thread",),
    "Thread": ("id", "sessionId", "parentThreadId", "source", "status"),
    "ThreadTurnsListParams": ("threadId", "limit", "sortDirection", "itemsView"),
    "ThreadTurnsListResponse": ("data",),
    "Turn": ("id", "rootTurnId", "status", "itemsView", "items"),
    "ThreadQueueAddParams": ("threadId", "input", "clientUserMessageId"),
    "ThreadQueueAddResponse": ("queuedSubmission",),
    "ThreadQueueListParams": ("threadId",),
    "ThreadQueueListResponse": ("data", "nextCursor"),
    "ThreadQueueDeleteParams": ("threadId", "queuedSubmissionId"),
    "ThreadQueueDeleteResponse": ("deleted",),
}


def schemas(root):
    found = {}
    paths = list(Path(root).rglob("*.json"))
    require(0 < len(paths) <= 3000, "native_contract_changed")
    for path in paths:
        require(not path.is_symlink() and path.stat().st_size <= 32 * 1024 * 1024, "output_limit")
        value = json.loads(path.read_text())
        if not isinstance(value, dict):
            continue
        name = value.get("title", path.stem)
        if isinstance(name, str) and name in FIELDS:
            found.setdefault(name, []).append(value)
        for section in ("$defs", "definitions"):
            for name, definition in value.get(section, {}).items():
                if name in FIELDS:
                    found.setdefault(name, []).append(definition)
    return found


def validate(found):
    for name, fields in FIELDS.items():
        alternatives = found.get(name, [])
        require(alternatives, "native_contract_changed")
        for schema in alternatives:
            require(isinstance(schema, dict) and schema.get("type") == "object" and
                    isinstance(schema.get("properties"), dict) and
                    all(field in schema["properties"] for field in fields), "native_contract_changed")
    return True


def main():
    require(len(sys.argv) == 3, "invalid_input")
    runtime, version = sys.argv[1:]
    pair = runtime_pair(runtime, version)
    with tempfile.TemporaryDirectory(prefix="pq-schema-", dir="/tmp") as temporary:
        root = Path(temporary).resolve()
        env = child_environment(root / "profile")
        binary = executable("codex")
        require(command([binary, "--version"], env, root, timeout=15).decode().strip() ==
                "codex-cli " + pair["codex"], "version_mismatch")
        output = root / "schemas"
        command([binary, "app-server", "generate-json-schema", "--experimental", "--out", str(output)],
                env, root, timeout=90)
        validate(schemas(output))
    private_json(Path.cwd() / "qualification-schema.json", {
        "schema": 1, "runtime": runtime, "version": version,
        "codex_schema": "passed", "claude_native_shapes": "not_tested",
        "live_semantics": "not_tested"})
    print("Codex generated schema fields matched; native semantics require live qualification")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (Failure, OSError, ValueError, KeyError, TypeError):
        print("Generated runtime schema check failed", file=sys.stderr)
        raise SystemExit(1) from None
