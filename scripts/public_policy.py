"""Public privacy patterns and an optional external, private marker policy."""
import fnmatch
import ipaddress
import os
from pathlib import PurePosixPath
import re
import stat
from urllib.parse import unquote


def regular_bytes(path):
    descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(descriptor, "rb") as stream:
        if not stat.S_ISREG(os.fstat(stream.fileno()).st_mode):
            raise ValueError("expected a regular file")
        return stream.read()


def exclusions(path):
    rules = []
    for line in regular_bytes(path).decode("utf-8").splitlines():
        if not line or line.startswith("#"):
            continue
        kind, value = line.split(" ", 1)
        parts = PurePosixPath(value).parts
        if kind not in {"tree", "name", "public"} or not value or "\\" in value:
            raise ValueError("invalid exclusion rule")
        if value.startswith("/") or any(part in {".", ".."} for part in parts):
            raise ValueError("unsafe exclusion rule")
        if kind != "name" and any(c in value for c in "*?["):
            raise ValueError("unexpected exclusion wildcard")
        if kind == "name" and "/" in value:
            raise ValueError("invalid component rule")
        if (kind, value) in rules:
            raise ValueError("duplicate exclusion rule")
        rules.append((kind, value))
    if not rules or ("tree", ".git") not in rules:
        raise ValueError("missing exclusion boundary")
    public = {value for kind, value in rules if kind == "public"}

    def boundary(name):
        return any(
            (kind == "tree" and (name == value or name.startswith(value + "/")))
            or (kind == "name" and any(fnmatch.fnmatchcase(part, value)
                                      for part in PurePosixPath(name).parts))
            for kind, value in rules
        )

    if any(not boundary(name) for name in public):
        raise ValueError("public exception outside exclusion boundary")

    def excluded(name):
        return name not in public and boundary(name)

    def descend(name):
        return not excluded(name) or any(item.startswith(name + "/") for item in public)

    return excluded, descend


PATTERNS = {
    "host home path": r"(?<![\w/])(?:[a-z]:)?/+" + r"(?:Us" + r"ers|ho" + r"me)/+[^/\s]+",
    "private local project path": r"~/" + "projects" + "/",
    "private host configuration path": r"~/[.]" + "claude" + "/",
    "email address": r"[A-Z0-9.!#$%&'*+/=?^_`{|}~-]+" + "@" +
                     r"[A-Z0-9](?:[A-Z0-9-]*[A-Z0-9])?(?:[.][A-Z0-9-]+)*[.][A-Z]{2,63}\b",
    "tailnet hostname": r"(?:[a-z0-9-]+[.])+" + r"ts[.]net\b",
    "private key block": r"-----BEGIN\s+(?:[A-Z0-9]+\s+)*" + r"PRIVATE\s+KEY-----",
    "access token": r"\b(?:gh" + r"p_[A-Za-z0-9]{20,}|github" +
                    r"_pat_[A-Za-z0-9_]{20,}|s" + r"k-[A-Za-z0-9_-]{20,}|xo" +
                    r"x[a-z]-[A-Za-z0-9-]{10,})\b",
}
PATTERNS = {label: re.compile(value, re.IGNORECASE) for label, value in PATTERNS.items()}
IPV4 = re.compile(r"(?<![\w.])(?:[0-9]{1,3}[.]){3}[0-9]{1,3}(?![\w.])")
IPV6 = re.compile(r"(?<![\w:])(?:[0-9a-f]{0,4}:){2,}[0-9a-f:.]+(?:%[\w.-]+)?", re.I)
TAIL4 = ipaddress.ip_network("100" + ".64.0.0/10")
TAIL6 = ipaddress.ip_network("fd7a:" + "115c:a1e0::/48")
PRIVATE4 = (TAIL4, ipaddress.ip_network("10" + ".0.0.0/8"),
            ipaddress.ip_network("172" + ".16.0.0/12"),
            ipaddress.ip_network("192" + ".168.0.0/16"))
NOREPLY = re.compile(r"[0-9]+[+][A-Z0-9-]+@users[.]noreply[.]github[.]com", re.I)


def private_markers(path):
    """Never ship private patterns, even as fragments or hashes, in this module."""
    content = regular_bytes(path).decode("utf-8")
    if len(content) > 1_000_000:
        raise ValueError("private marker policy is too large")
    patterns = []
    for line in content.splitlines():
        if not line or line.startswith("#"):
            continue
        if len(line) > 4096 or len(patterns) >= 4096:
            raise ValueError("private marker policy limit exceeded")
        try:
            pattern = re.compile(line, re.IGNORECASE)
        except re.error:
            raise ValueError("invalid private marker policy") from None
        if pattern.search(""):
            raise ValueError("private marker must not match empty input")
        patterns.append(pattern)
    if not patterns:
        raise ValueError("private marker policy must not be empty")
    return tuple(patterns)


def normalize(value):
    for _ in range(8):
        # Valid source can contain Git placeholders such as percent followed by hex.
        decoded = unquote(value, errors="surrogateescape")
        if decoded == value:
            break
        value = decoded
    value = re.sub(r"\\(?:u00|x)(2f|5c)",
                   lambda match: "/" if match[1].lower() == "2f" else "\\", value, flags=re.I)
    return value.replace("\\", "/")


def findings(value, name=None, markers=()):
    value = normalize(value)
    labels = {"private marker"} if any(pattern.search(value) for pattern in markers) else set()
    for label, pattern in PATTERNS.items():
        if any(label != "email address" or not NOREPLY.fullmatch(match[0])
               for match in pattern.finditer(value)):
            labels.add(label)
    for match in IPV4.finditer(value):
        try:
            address = ipaddress.ip_address(match[0])
        except ValueError:
            continue
        if any(address in network for network in PRIVATE4):
            labels.add("private infrastructure address")
    for match in IPV6.finditer(value):
        try:
            address = ipaddress.ip_address(match[0].split("%", 1)[0])
        except ValueError:
            continue
        if address in TAIL6:
            labels.add("private infrastructure address")
    return sorted(labels)
