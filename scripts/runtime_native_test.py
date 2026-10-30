"""Native Claude binary selection from a frozen npm tree; synthetic bytes only, nothing is executed."""
import base64
import json
import os
from pathlib import Path
import runpy
import shutil
import tempfile
import unittest
from unittest.mock import patch


HERE = Path(__file__).resolve().parent
NPM = runpy.run_path(str(HERE / "runtime_npm.py"))
NATIVE = runpy.run_path(str(HERE / "runtime_native.py")) if (HERE / "runtime_native.py").exists() else {}
VERSION = "2.1.308"
SRI = "sha512-" + base64.b64encode(b"a" * 64).decode()
LAUNCHER = "@anthropic-ai/claude-code"
PLACEHOLDER = b'echo "Error: claude native binary not installed." >&2\nexit 1\n'
ARM, X64 = 0x0100000C, 0x01000007


def macho(cpu, kind=2, magic=b"\xcf\xfa\xed\xfe"):
    return magic + cpu.to_bytes(4, "little") + bytes(4) + kind.to_bytes(4, "little") + bytes(48)


def elf(machine, bits=2, order=1, kind=3):
    return (b"\x7fELF" + bytes([bits, order, 1, 0]) + bytes(8) + kind.to_bytes(2, "little")
            + machine.to_bytes(2, "little") + bytes(44))


# host as reported by platform/libc probing, package suffix, lock os/cpu/libc, native header
PLATFORMS = [
    (("Darwin", "arm64", None), "darwin-arm64", "darwin", "arm64", None, macho(ARM)),
    (("Darwin", "x86_64", None), "darwin-x64", "darwin", "x64", None, macho(X64)),
    (("Linux", "x86_64", "glibc"), "linux-x64", "linux", "x64", "glibc", elf(0x3E)),
    (("Linux", "amd64", "glibc"), "linux-x64", "linux", "x64", "glibc", elf(0x3E)),
    (("Linux", "aarch64", "glibc"), "linux-arm64", "linux", "arm64", "glibc", elf(0xB7)),
    (("Linux", "x86_64", "musl"), "linux-x64-musl", "linux", "x64", "musl", elf(0x3E)),
    (("Linux", "aarch64", "musl"), "linux-arm64-musl", "linux", "arm64", "musl", elf(0xB7)),
]
DARWIN, LINUX = PLATFORMS[0], PLATFORMS[2]


def native_entry(version, suffix, os_name, cpu, libc):
    entry = {"version": version, "integrity": SRI, "optional": True, "os": [os_name], "cpu": [cpu],
             "resolved": f"https://registry.npmjs.org/@anthropic-ai/claude-code-{suffix}/-/claude-code-{suffix}-{version}.tgz"}
    if libc:
        entry["libc"] = [libc]
    return entry


class Tree:
    """An `npm ci --ignore-scripts` result: stub launcher plus the single installed platform package."""

    def __init__(self, directory, platform=DARWIN, version=VERSION):
        self.root = Path(directory).resolve()
        self.host, self.suffix, self.os, self.cpu, self.libc, self.header = platform
        self.version, self.name = version, "@anthropic-ai/claude-code-" + self.suffix
        natives = {row[1]: native_entry(version, *row[1:5]) for row in PLATFORMS}
        self.packages = {
            "": {"name": "peerturn-runtime-qualification", "version": "1.0.0", "dependencies": {LAUNCHER: version}},
            "node_modules/" + LAUNCHER: {
                "version": version, "integrity": SRI, "optionalDependencies": {
                    "@anthropic-ai/claude-code-" + suffix: version for suffix in natives},
                "resolved": f"https://registry.npmjs.org/{LAUNCHER}/-/claude-code-{version}.tgz"}}
        for suffix, entry in natives.items():
            self.packages["node_modules/@anthropic-ai/claude-code-" + suffix] = entry
        self.key = "node_modules/" + self.name
        self.installed = {key: self.packages[key] for key in ("node_modules/" + LAUNCHER, self.key)}
        launcher = self.root / "node_modules" / LAUNCHER
        (launcher / "bin").mkdir(parents=True)
        (launcher / "bin/claude.exe").write_bytes(PLACEHOLDER)
        (self.root / "node_modules/.bin").mkdir()
        (self.root / "node_modules/.bin/claude").symlink_to("../" + LAUNCHER + "/bin/claude.exe")
        self.package = self.root / "node_modules" / self.name
        self.package.mkdir()
        self.manifest = self.package / "package.json"
        self.manifest.write_text(json.dumps({"name": self.name, "version": version, "os": [self.os], "cpu": [self.cpu]}))
        self.binary = self.package / "claude"
        self.binary.write_bytes(self.header)
        self.binary.chmod(0o600)

    def select(self, host=None):
        return NATIVE["select"](self.root, self.version, self.packages, self.installed, host or self.host)

    def contracts(self):
        lock = json.dumps({"lockfileVersion": 3, "packages": self.packages}).encode()
        (self.root / "package-lock.json").write_bytes(lock)
        (self.root / "npm-tree.json").write_bytes(NPM["canonical"](
            {"resolved": self.packages, "installed": self.installed}))
        return json.dumps(NPM["bundle"](lock, self.version, SRI))


class NativeSelectionTests(unittest.TestCase):
    def tree(self, platform=DARWIN):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        return Tree(directory.name, platform)

    def refuse(self, tree, reason, host=None):
        with self.assertRaisesRegex(ValueError, reason):
            tree.select(host)

    def test_selects_the_real_platform_binary_instead_of_the_ignore_scripts_stub(self):
        for platform in PLATFORMS:
            tree = self.tree(platform)
            with self.subTest(host=platform[0]):
                binary = tree.select()
                self.assertEqual(binary, tree.binary)
                self.assertEqual(binary.read_bytes(), platform[5])
                self.assertEqual(binary.stat().st_mode & 0o777, 0o700)
                stub = tree.root / "node_modules/.bin/claude"
                self.assertEqual(stub.resolve().read_bytes(), PLACEHOLDER)
                self.assertNotEqual(binary, stub.resolve())

    def test_unknown_or_ambiguous_hosts_never_fall_back_to_another_platform(self):
        tree = self.tree()
        for host in (("Windows", "AMD64", None), ("FreeBSD", "amd64", None), ("Linux", "riscv64", "glibc"),
                     ("Linux", "ppc64le", "glibc"), ("Darwin", "i386", None), ("Linux", "x86_64", None),
                     ("Linux", "x86_64", "uclibc"), ("Darwin", "arm64", "musl"), ("", "", None)):
            with self.subTest(host=host):
                self.refuse(tree, "host", host)

    def test_host_must_select_the_package_that_is_actually_installed(self):
        tree = self.tree()
        self.refuse(tree, "installed", ("Darwin", "x86_64", None))
        self.refuse(tree, "installed", ("Linux", "x86_64", "glibc"))

    def test_lock_must_pin_the_exact_optional_platform_package(self):
        changes = {
            "version": lambda p, e: e.update(version="2.1.307"),
            "os": lambda p, e: e.update(os=["linux"]),
            "cpu": lambda p, e: e.update(cpu=["x64"]),
            "libc": lambda p, e: e.update(libc=["musl"]),
            "optional": lambda p, e: e.update(optional=False),
            "other tarball": lambda p, e: e.update(resolved=e["resolved"].replace("darwin-arm64", "darwin-x64")),
            "other version tarball": lambda p, e: e.update(resolved=e["resolved"].replace(VERSION, "2.1.307")),
            "undeclared": lambda p, e: p["node_modules/" + LAUNCHER]["optionalDependencies"].pop(
                "@anthropic-ai/claude-code-darwin-arm64"),
            "declared at another version": lambda p, e: p["node_modules/" + LAUNCHER]["optionalDependencies"].update(
                {"@anthropic-ai/claude-code-darwin-arm64": "2.1.307"}),
            "missing": lambda p, e: p.pop("node_modules/@anthropic-ai/claude-code-darwin-arm64"),
            "launcher missing": lambda p, e: p.pop("node_modules/" + LAUNCHER),
            "launcher version": lambda p, e: p["node_modules/" + LAUNCHER].update(version="2.1.307"),
        }
        for label, change in changes.items():
            tree = self.tree()
            change(tree.packages, tree.packages.get(tree.key, {}))
            with self.subTest(label=label):
                self.refuse(tree, "lock")

    def test_linux_lock_must_carry_the_matching_libc(self):
        tree = self.tree(LINUX)
        tree.packages[tree.key]["libc"] = ["musl"]
        self.refuse(tree, "lock")
        del tree.packages[tree.key]["libc"]
        self.refuse(tree, "lock")

    def test_installed_tree_must_hold_exactly_the_selected_package_as_locked(self):
        tree = self.tree()
        tree.installed.pop(tree.key)
        self.refuse(tree, "installed")
        tree = self.tree()
        tree.installed[tree.key] = dict(tree.packages[tree.key], integrity="sha512-" + "b" * 86 + "==")
        self.refuse(tree, "installed")
        tree = self.tree()
        tree.installed["node_modules/@anthropic-ai/claude-code-darwin-x64"] = tree.packages[
            "node_modules/@anthropic-ai/claude-code-darwin-x64"]
        self.refuse(tree, "installed")
        tree = self.tree(LINUX)
        tree.installed["node_modules/@anthropic-ai/claude-code-linux-x64-musl"] = tree.packages[
            "node_modules/@anthropic-ai/claude-code-linux-x64-musl"]
        self.refuse(tree, "installed")

    def test_package_manifest_must_match_name_version_os_cpu_and_libc(self):
        base = {"name": "@anthropic-ai/claude-code-darwin-arm64", "version": VERSION, "os": ["darwin"], "cpu": ["arm64"]}
        broken = [dict(base, name="@anthropic-ai/claude-code-darwin-x64"), dict(base, version="2.1.305"),
                  dict(base, os=["linux"]), dict(base, os=["darwin", "linux"]), dict(base, cpu=["x64"]),
                  dict(base, libc=["glibc"]), {key: value for key, value in base.items() if key != "os"}, [base]]
        for manifest in broken:
            tree = self.tree()
            tree.manifest.write_text(json.dumps(manifest))
            with self.subTest(manifest=manifest):
                self.refuse(tree, "manifest")
        for raw in ('{"name":"x","name":"@anthropic-ai/claude-code-darwin-arm64"}', "{", " " * 65537):
            tree = self.tree()
            tree.manifest.write_text(raw)
            with self.subTest(raw=raw[:20]):
                self.refuse(tree, "manifest")
        tree = self.tree(LINUX)
        tree.manifest.write_text(json.dumps(dict(base, name=tree.name, os=["linux"], cpu=["x64"], libc=["musl"])))
        self.refuse(tree, "manifest")
        tree.manifest.write_text(json.dumps(dict(base, name=tree.name, os=["linux"], cpu=["x64"], libc=["glibc"])))
        tree.select()

    def test_binary_must_be_the_host_native_executable_format(self):
        cases = {
            "x86_64 Mach-O in arm64 package": (DARWIN, macho(X64)), "universal Mach-O": (DARWIN, macho(ARM, magic=b"\xca\xfe\xba\xbe")),
            "32-bit Mach-O": (DARWIN, macho(ARM, magic=b"\xce\xfa\xed\xfe")), "dylib": (DARWIN, macho(ARM, kind=6)),
            "ELF in macOS package": (DARWIN, elf(0xB7)), "launcher stub": (DARWIN, PLACEHOLDER), "empty": (DARWIN, b""),
            "truncated": (DARWIN, macho(ARM)[:9]), "aarch64 ELF in x64 package": (LINUX, elf(0xB7)),
            "32-bit ELF": (LINUX, elf(0x3E, bits=1)), "big-endian ELF": (LINUX, elf(0x3E, order=2)),
            "relocatable ELF": (LINUX, elf(0x3E, kind=1)), "Mach-O in Linux package": (LINUX, macho(X64)),
            "shell script": (LINUX, b"#!/bin/sh\nexec node cli.js\n" + bytes(64)),
        }
        for label, (platform, header) in cases.items():
            tree = self.tree(platform)
            tree.binary.write_bytes(header)
            with self.subTest(label=label):
                self.refuse(tree, "format")
                self.assertEqual(tree.binary.stat().st_mode & 0o777, 0o600)

    def test_oversized_binary_is_refused_without_reading_it(self):
        tree = self.tree()
        with open(tree.binary, "r+b") as stream:
            stream.truncate(1_000_000_001)
        self.refuse(tree, "format")

    def test_symlinks_and_missing_files_never_reach_the_version_probe(self):
        outside = Path(tempfile.mkdtemp()).resolve()
        self.addCleanup(shutil.rmtree, outside, True)
        (outside / "claude").write_bytes(DARWIN[5])

        def relink(path, target):
            holder = outside / ("moved-" + path.name)
            shutil.move(path, holder)
            path.symlink_to(target if target else holder)

        cases = {
            "binary to outside file": lambda t: (t.binary.unlink(), t.binary.symlink_to(outside / "claude")),
            "binary to sibling file": lambda t: (shutil.copy(t.binary, t.package / "real"), t.binary.unlink(),
                                                 t.binary.symlink_to("real")),
            "package directory": lambda t: relink(t.package, None),
            "scope directory": lambda t: relink(t.package.parent, None),
            "node_modules": lambda t: relink(t.root / "node_modules", None),
            "manifest": lambda t: relink(t.manifest, None),
            "binary is a directory": lambda t: (t.binary.unlink(), t.binary.mkdir()),
            "binary missing": lambda t: t.binary.unlink(),
            "manifest missing": lambda t: t.manifest.unlink(),
        }
        for label, change in cases.items():
            tree = self.tree()
            change(tree)
            with self.subTest(label=label):
                self.refuse(tree, "path")

    def test_launcher_stub_alone_is_not_a_runtime(self):
        tree = self.tree()
        shutil.rmtree(tree.package)
        tree.installed.pop(tree.key)
        self.refuse(tree, "installed")

    def test_libc_family_is_detected_without_running_the_agent(self):
        with patch.object(os, "confstr", return_value="glibc 2.39"):
            self.assertEqual(NATIVE["libc_family"]("x86_64"), "glibc")
        with patch.object(os, "confstr", side_effect=ValueError), patch.object(
                os.path, "exists", side_effect=lambda path: path == "/lib/ld-musl-aarch64.so.1"):
            self.assertEqual(NATIVE["libc_family"]("aarch64"), "musl")
            with self.assertRaisesRegex(ValueError, "host"):
                NATIVE["libc_family"]("x86_64")
        with patch.object(os, "confstr", return_value="unknown 1.0"), patch.object(os.path, "exists", return_value=False):
            with self.assertRaisesRegex(ValueError, "host"):
                NATIVE["libc_family"]("x86_64")

    def test_current_host_probes_libc_only_on_linux(self):
        with patch.object(NATIVE["platform"], "system", return_value="Darwin"), patch.object(
                NATIVE["platform"], "machine", return_value="arm64"), patch.object(os, "confstr", side_effect=AssertionError):
            self.assertEqual(NATIVE["current_host"](), ("Darwin", "arm64", None))
        with patch.object(NATIVE["platform"], "system", return_value="Linux"), patch.object(
                NATIVE["platform"], "machine", return_value="aarch64"), patch.object(os, "confstr", return_value="glibc 2.39"):
            self.assertEqual(NATIVE["current_host"](), ("Linux", "aarch64", "glibc"))


if __name__ == "__main__":
    unittest.main()
