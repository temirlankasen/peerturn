"""Read-only native evidence. Assistant claims and unpaired tool records prove nothing."""
import json
import re

from core import Failure, require


def result_object(value):
    if isinstance(value, str):
        try:
            return result_object(json.loads(value))
        except (ValueError, RecursionError):
            return None
    if isinstance(value, list):
        found = [result_object(item) for item in value]
        found = [item for item in found if isinstance(item, dict)]
        return found[0] if len(found) == 1 else None
    if isinstance(value, dict):
        if value.get("type") == "text" and isinstance(value.get("text"), str):
            return result_object(value["text"])
        for wrapper in ("Ok", "content", "structuredContent"):
            if wrapper in value:
                return result_object(value[wrapper])
        return value
    return None


def check_join_arguments(arguments):
    require(isinstance(arguments, dict) and set(arguments) <= {"handle"})
    if "handle" in arguments:
        require(isinstance(arguments["handle"], str) and
                re.fullmatch(r"[0-9a-f]{12}", arguments["handle"]))


def tool_name(name):
    if not isinstance(name, str):
        return ""
    for prefix in ("mcp__peerturn__", "mcp__peerturn."):
        if name.startswith(prefix):
            return name[len(prefix):]
    return name


def json_lines(path):
    require(not path.is_symlink() and path.stat().st_size <= 32 * 1024 * 1024, "output_limit")
    raw = path.read_bytes()
    rows = []
    # A native writer may still be appending the last record.
    for line in raw.splitlines(keepends=True):
        if not line.endswith(b"\n"):
            break
        try:
            row = json.loads(line)
        except (ValueError, UnicodeError):
            raise Failure("native_contract_changed") from None
        require(isinstance(row, dict), "native_contract_changed")
        rows.append(row)
    return rows


class NativeEvidence:
    def __init__(self, runtime, rows):
        self.runtime, self.rows = runtime, rows

    def calls(self, name):
        pending, paired = {}, []

        def start(identifier, actual, arguments, index):
            if tool_name(actual) != name:
                return
            require(isinstance(identifier, str) and identifier not in pending)
            if isinstance(arguments, str):
                try:
                    arguments = json.loads(arguments)
                except ValueError:
                    raise Failure("native_contract_changed") from None
            require(isinstance(arguments, dict), "native_contract_changed")
            pending[identifier] = {"arguments": arguments, "index": index}

        def end(identifier, output, index):
            if identifier in pending:
                item = pending.pop(identifier)
                item.update(result=result_object(output), end=index)
                paired.append(item)

        for index, row in enumerate(self.rows):
            if self.runtime == "claude":
                message = row.get("message", {})
                if not isinstance(message, dict):
                    continue
                blocks = message.get("content", [])
                if not isinstance(blocks, list):
                    continue
                for block in blocks:
                    if not isinstance(block, dict):
                        continue
                    if row.get("type") == "assistant" and block.get("type") == "tool_use":
                        start(block.get("id"), block.get("name"), block.get("input"), index)
                    if row.get("type") == "user" and block.get("type") == "tool_result":
                        end(block.get("tool_use_id"), block.get("content"), index)
            else:
                p = row.get("payload", {})
                if not isinstance(p, dict):
                    continue
                if row.get("type") == "response_item":
                    if p.get("type") == "function_call":
                        start(p.get("call_id"), p.get("name"), p.get("arguments"), index)
                    elif p.get("type") == "function_call_output":
                        end(p.get("call_id"), p.get("output"), index)
                elif row.get("type") == "event_msg":
                    if p.get("type") == "mcp_tool_call_begin":
                        invocation = p.get("invocation", {})
                        if invocation.get("server") == "peerturn":
                            start(p.get("call_id"), invocation.get("tool"),
                                  invocation.get("arguments"), index)
                    elif p.get("type") == "mcp_tool_call_end":
                        end(p.get("call_id"), p.get("result"), index)
        return sorted(paired, key=lambda item: item["index"])

    def input_seen(self, marker, after=0, message_id=None):
        for row in self.rows[after:]:
            content = None
            if self.runtime == "claude" and row.get("type") == "user":
                content = row.get("message", {}).get("content")
                if isinstance(content, list):
                    content = "".join(block.get("text", "") for block in content
                                      if isinstance(block, dict) and block.get("type") == "text")
            elif self.runtime == "codex" and row.get("type") == "event_msg":
                p = row.get("payload", {})
                if p.get("type") == "user_message":
                    content = p.get("message")
            if not isinstance(content, str) or "\n" not in content:
                continue
            prefix, payload = content.split("\n", 1)
            expected = "PeerTurn:" if self.runtime == "claude" else "PeerTurn verified peer message."
            if not prefix.startswith(expected):
                continue
            try:
                frame = json.loads(payload)
            except ValueError:
                continue
            if not isinstance(frame, dict):
                continue
            if self.runtime == "codex":
                if frame.get("schema") != "peerturn.native.v1" or frame.get("scope") != "coauthor":
                    continue
                frame = frame.get("message", {})
                if not isinstance(frame, dict):
                    continue
                body = frame.get("body_utf8", "")
            else:
                if frame.get("scope") != "coauthor":
                    continue
                body = frame.get("untrusted_peer_text", "")
            if isinstance(body, str) and marker in body and \
                    (message_id is None or frame.get("message_id") == message_id):
                return True
        return False

    def completed(self, after=0):
        for row in self.rows[after:]:
            if self.runtime == "claude" and row.get("type") == "assistant" and \
                    row.get("message", {}).get("stop_reason") == "end_turn":
                return True
            if self.runtime == "codex" and row.get("type") == "event_msg" and \
                    row.get("payload", {}).get("type") in ("task_complete", "turn_complete"):
                return True
        return False

    def review_observed(self, after=0):
        events = [row.get("payload", {}) for row in self.rows[after:]
                  if row.get("type") == "event_msg"]
        began = any(p.get("type") == "entered_review_mode" for p in events)
        ended = any(p.get("type") == "exited_review_mode" for p in events)
        return began and ended


def single_call(evidence, name, after, expected_code=None, expected_state=None):
    calls = [item for item in evidence.calls(name) if item["index"] >= after]
    require(len(calls) == 1)
    call = calls[0]
    if name == "peer_join":
        check_join_arguments(call["arguments"])
    result = call["result"]
    require(isinstance(result, dict))
    if expected_code:
        require(result.get("error", {}).get("code") == expected_code or
                result.get("code") == expected_code)
    if expected_state:
        require(result.get("state") == expected_state)
    return call
