"""Select the real Claude native executable from the integrity-pinned npm tree.

`npm ci --ignore-scripts` never runs the launcher postinstall, so bin/claude.exe stays a stub and the
executable lives in exactly one platform optional package. Every check here uses lock data, file
metadata and the first header bytes only; nothing is executed before the caller's version probe."""
import json
import os
from pathlib import Path
import platform
import runpy
import stat


NPM = runpy.run_path(str(Path(__file__).with_name("runtime_npm.py")))
LAUNCHER = NPM["PACKAGE"]
LIMIT = 65536
BINARY_LIMIT = 1_000_000_000
CPUS = {"x86_64": "x64", "amd64": "x64", "arm64": "arm64", "aarch64": "arm64"}
MACHO_CPU = {"arm64": 0x0100000C, "x64": 0x01000007}
ELF_MACHINE = {"arm64": 0xB7, "x64": 0x3E}
HOSTS = {("Darwin", None), ("Linux", "glibc"), ("Linux", "musl")}


def libc_family(machine):
    try:
        if os.confstr("CS_GNU_LIBC_VERSION").startswith("glibc "):
            return "glibc"
    except (ValueError, OSError, AttributeError):
        pass
    if os.path.exists("/lib/ld-musl-" + machine + ".so.1"):
        return "musl"
    raise ValueError("unsupported native runtime host")


def current_host():
    system, machine = platform.system(), platform.machine()
    return system, machine, libc_family(machine) if system == "Linux" else None


def target(host):
    system, machine, libc = host
    cpu = CPUS.get(machine)
    if cpu is None or (system, libc) not in HOSTS:
        raise ValueError("unsupported native runtime host")
    name = f"{LAUNCHER}-{system.lower()}-{cpu}" + ("-musl" if libc == "musl" else "")
    return {"name": name, "os": system.lower(), "cpu": cpu, "libc": libc}


def check_lock(spec, version, packages, installed):
    key, name = "node_modules/" + spec["name"], spec["name"]
    entry, launcher = packages.get(key), packages.get("node_modules/" + LAUNCHER)
    if not isinstance(entry, dict) or not isinstance(launcher, dict):
        raise ValueError("native package not pinned by lock")
    optional = launcher.get("optionalDependencies")
    tarball = f"https://registry.npmjs.org/{name}/-/{name.split('/')[1]}-{version}.tgz"
    expected = {"version": version, "resolved": tarball, "os": [spec["os"]], "cpu": [spec["cpu"]],
                "libc": [spec["libc"]] if spec["libc"] else None}
    if (launcher.get("version") != version or not isinstance(optional, dict) or optional.get(name) != version
            or entry.get("optional") is not True or any(entry.get(field) != value for field, value in expected.items())):
        raise ValueError("native package not pinned by lock")
    natives = {path for path in installed if path.startswith("node_modules/" + LAUNCHER + "-")}
    if natives != {key} or installed[key] != entry:
        raise ValueError("installed native package mismatch")


def regular(path):
    """Open a regular file without following links; anything else is refused before it is read."""
    try:
        if not stat.S_ISREG(os.lstat(path).st_mode):
            raise ValueError("unsafe native package path")
        stream = open(path, "rb", opener=lambda name, flags: os.open(name, flags | os.O_NOFOLLOW))
    except OSError:
        raise ValueError("unsafe native package path") from None
    if not stat.S_ISREG(os.fstat(stream.fileno()).st_mode):
        stream.close()
        raise ValueError("unsafe native package path")
    return stream


def package_directory(root, name):
    current = root.resolve(strict=True)
    for part in ("node_modules", *name.split("/")):
        current = current / part
        try:
            if not stat.S_ISDIR(os.lstat(current).st_mode):
                raise ValueError("unsafe native package path")
        except OSError:
            raise ValueError("unsafe native package path") from None
    return current


def check_manifest(directory, spec, version):
    with regular(directory / "package.json") as stream:
        data = stream.read(LIMIT + 1)
    try:
        manifest = json.loads(data, object_pairs_hook=NPM["unique_fields"]) if len(data) <= LIMIT else None
    except ValueError:
        manifest = None
    expected = {"name": spec["name"], "version": version, "os": [spec["os"]], "cpu": [spec["cpu"]]}
    libc = (None, [spec["libc"]]) if spec["libc"] else (None,)
    if (not isinstance(manifest, dict) or any(manifest.get(field) != value for field, value in expected.items())
            or manifest.get("libc") not in libc):
        raise ValueError("invalid native package manifest")


def check_format(header, spec):
    if spec["os"] == "darwin":
        valid = (len(header) >= 16 and header[:4] == b"\xcf\xfa\xed\xfe"
                 and int.from_bytes(header[4:8], "little") == MACHO_CPU[spec["cpu"]]
                 and int.from_bytes(header[12:16], "little") == 2)
    else:
        valid = (len(header) >= 20 and header[:7] == b"\x7fELF\x02\x01\x01"
                 and int.from_bytes(header[16:18], "little") in (2, 3)
                 and int.from_bytes(header[18:20], "little") == ELF_MACHINE[spec["cpu"]])
    if not valid:
        raise ValueError("invalid native binary format")


def check_binary(path, spec):
    with regular(path) as stream:
        info = os.fstat(stream.fileno())
        if not 0 < info.st_size <= BINARY_LIMIT:
            raise ValueError("invalid native binary format")
        check_format(stream.read(64), spec)
        if info.st_mode & 0o700 != 0o700:
            os.fchmod(stream.fileno(), 0o700)


def select(root, version, packages, installed, host=None):
    """Return the verified native executable path for this host; any doubt raises ValueError."""
    if not isinstance(packages, dict) or not isinstance(installed, dict):
        raise ValueError("native package not pinned by lock")
    spec = target(current_host() if host is None else host)
    check_lock(spec, version, packages, installed)
    directory = package_directory(root, spec["name"])
    check_manifest(directory, spec, version)
    binary = directory / "claude"
    check_binary(binary, spec)
    if binary.resolve(strict=True) != binary:
        raise ValueError("unsafe native package path")
    return binary
