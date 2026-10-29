"""Frozen npm tree acceptance; no real upstream package or agent is installed."""
import base64
import copy
import hashlib
import io
import json
import os
from pathlib import Path
import runpy
import shutil
import subprocess
import tarfile
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch


HERE = Path(__file__).resolve().parent
N = runpy.run_path(str(HERE / "runtime_npm.py")) if (HERE / "runtime_npm.py").exists() else {}
VERSION = "2.1.308"
SRI = "sha512-" + base64.b64encode(b"a" * 64).decode()


def fixture():
    return {"name": "peerturn-runtime-qualification", "version": "1.0.0", "lockfileVersion": 3,
            "requires": True, "packages": {
                "": {"name": "peerturn-runtime-qualification", "version": "1.0.0",
                     "dependencies": {"@anthropic-ai/claude-code": VERSION}},
                "node_modules/@anthropic-ai/claude-code": {
                    "version": VERSION, "resolved": "https://registry.npmjs.org/claude-code.tgz",
                    "integrity": SRI, "dependencies": {"fixture-transitive": "^1.0.0"}},
                "node_modules/fixture-transitive": {
                    "version": "1.0.1", "resolved": "https://registry.npmjs.org/transitive.tgz",
                    "integrity": SRI}}}


class RuntimeNpmTests(unittest.TestCase):
    def test_every_transitive_package_is_integrity_pinned_before_execution(self):
        lock = fixture()
        identity = N["identity"](json.dumps(lock).encode(), VERSION, SRI)
        self.assertRegex(identity["npm_lock_sha256"], r"^[0-9a-f]{64}$")
        self.assertRegex(identity["npm_tree_sha256"], r"^[0-9a-f]{64}$")
        for changes in ({"integrity": ""}, {"integrity": "sha512-invalid"},
                        {"resolved": "https://evil.invalid/package.tgz"},
                        {"resolved": "file:/private/package.tgz"}, {"link": True}):
            broken = copy.deepcopy(lock)
            broken["packages"]["node_modules/fixture-transitive"].update(changes)
            with self.subTest(changes=changes), self.assertRaises(ValueError):
                N["identity"](json.dumps(broken).encode(), VERSION, SRI)

    def test_dependency_mutation_changes_tree_even_with_same_entrypoint(self):
        lock = fixture()
        before = N["identity"](json.dumps(lock).encode(), VERSION, SRI)
        lock["packages"]["node_modules/fixture-transitive"]["integrity"] = "sha512-" + base64.b64encode(b"b" * 64).decode()
        after = N["identity"](json.dumps(lock).encode(), VERSION, SRI)
        self.assertNotEqual(before["npm_tree_sha256"], after["npm_tree_sha256"])
        self.assertNotEqual(before["npm_lock_sha256"], after["npm_lock_sha256"])

    def test_contract_output_preserves_exact_lock_and_rejects_tampering(self):
        raw = json.dumps(fixture(), indent=2).encode() + b"\n"
        bundle = N["bundle"](raw, VERSION, SRI)
        self.assertEqual(N["unbundle"](json.dumps(bundle), VERSION, SRI)[0], raw)
        for key in ("npm_lock_sha256", "npm_tree_sha256"):
            broken = dict(bundle, **{key: "0" * 64})
            with self.subTest(key=key), self.assertRaises(ValueError):
                N["unbundle"](json.dumps(broken), VERSION, SRI)
        with self.assertRaises(ValueError):
            N["unbundle"](json.dumps(bundle), "2.1.309", SRI)
        for invalid in (None, "{}", "x" * 90001, '{"package_lock":"x","package_lock":"y"}'):
            with self.subTest(invalid=str(invalid)[:30]), self.assertRaises(ValueError):
                N["unbundle"](invalid, VERSION, SRI)
        with self.assertRaises(ValueError):
            N["identity"](b" " * 65537, VERSION, SRI)

    def test_platform_optional_nodes_are_part_of_the_common_resolved_identity(self):
        lock = fixture()
        lock["packages"]["node_modules/fixture-transitive"].update(optional=True, os=["darwin"], cpu=["arm64"])
        before = N["identity"](json.dumps(lock).encode(), VERSION, SRI)
        lock["packages"]["node_modules/fixture-transitive"]["os"] = ["linux"]
        after = N["identity"](json.dumps(lock).encode(), VERSION, SRI)
        self.assertNotEqual(before["npm_tree_sha256"], after["npm_tree_sha256"])

    def test_only_contracts_resolve_and_live_installs_the_exact_frozen_tree(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            raw = json.dumps(fixture()).encode()
            calls = []
            def command(argv, cwd):
                self.assertEqual(cwd, root)
                self.assertIn("--ignore-scripts", argv)
                calls.append(argv[1])
                if argv[1] == "install":
                    self.assertIn("--package-lock-only", argv)
                    (root / "package-lock.json").write_bytes(raw)
                    return ""
                if argv[1] == "ci":
                    self.assertEqual((root / "package-lock.json").read_bytes(), raw)
                    return ""
                self.assertEqual(argv[1:5], ["ls", "--all", "--json", "--long"])
                return json.dumps({"dependencies": {
                    "@anthropic-ai/claude-code": {"version": VERSION,
                        "path": str(root / "node_modules/@anthropic-ai/claude-code"), "dependencies": {
                            "fixture-transitive": {"version": "1.0.1", "path": str(root / "node_modules/fixture-transitive")}}}}})
            with patch.object(N["shutil"], "which", return_value="/usr/bin/npm"):
                frozen = N["resolve"](VERSION, SRI, root, command)
                identity = N["install"](VERSION, SRI, json.dumps(frozen), root, command)
            self.assertEqual(calls, ["install", "ci", "ls"])
            self.assertEqual(identity["npm_tree_sha256"], frozen["npm_tree_sha256"])
            self.assertEqual(identity["npm_installed_tree_sha256"], N["digest"](N["canonical"](
                {key: value for key, value in fixture()["packages"].items() if key})))
            self.assertTrue((root / "npm-tree.json").is_file())
            self.assertEqual(N["verify"](root, VERSION, SRI, json.dumps(frozen)), identity)
            tree = json.loads((root / "npm-tree.json").read_text())
            tree["installed"]["node_modules/fixture-transitive"]["version"] = "1.0.2"
            (root / "npm-tree.json").write_text(json.dumps(tree))
            with self.assertRaisesRegex(ValueError, "unlocked installed"):
                N["verify"](root, VERSION, SRI, json.dumps(frozen))

    def test_npm_ci_cannot_silently_rewrite_the_contracts_lock(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            raw = json.dumps(fixture()).encode()
            def command(argv, cwd):
                self.assertEqual(argv[1], "ci")
                (cwd / "package-lock.json").write_bytes(raw + b" ")
            with self.assertRaisesRegex(ValueError, "changed pinned lock"):
                N["install"](VERSION, SRI, json.dumps(N["bundle"](raw, VERSION, SRI)), root, command)


class NpmIntegrityAcceptance(unittest.TestCase):
    def setUp(self):
        self.npm = shutil.which("npm")
        self.assertIsNotNone(self.npm, "npm is required for offline integrity acceptance")
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.project = self.root / "project"
        self.project.mkdir()

    def package(self, name, dependencies=None, os_names=None, optional=None):
        manifest = {"name": name, "version": "1.0.0"}
        if dependencies:
            manifest["dependencies"] = dependencies
        if os_names:
            manifest["os"] = os_names
        if optional:
            manifest["optionalDependencies"] = optional
        target = self.root / (name + ".tgz")
        with tarfile.open(target, "w:gz") as archive:
            for filename, data in (("package.json", json.dumps(manifest).encode()),
                                   ("index.js", b"module.exports = 'synthetic fixture';\n")):
                member = tarfile.TarInfo("package/" + filename)
                member.size = len(data)
                archive.addfile(member, io.BytesIO(data))
        return {"version": "1.0.0", "resolved": target.as_uri(),
                "integrity": "sha512-" + base64.b64encode(hashlib.sha512(target.read_bytes()).digest()).decode()}

    def project_files(self, corrupt=False):
        dep = self.package("fixture-transitive")
        optional = self.package("fixture-optional", os_names=["peerturn-unsupported-os"])
        optional.update(optional=True, os=["peerturn-unsupported-os"])
        direct = self.package("fixture-root", {"fixture-transitive": "1.0.0"}, optional={"fixture-optional": "1.0.0"})
        direct.update(dependencies={"fixture-transitive": "1.0.0"},
                      optionalDependencies={"fixture-optional": "1.0.0"})
        manifest = {"name": "fixture", "version": "1.0.0", "dependencies": {"fixture-root": "1.0.0"}}
        lock = {"name": "fixture", "version": "1.0.0", "lockfileVersion": 3,
                "packages": {"": manifest, "node_modules/fixture-root": direct,
                             "node_modules/fixture-transitive": dep, "node_modules/fixture-optional": optional}}
        if corrupt:
            (self.root / "fixture-transitive.tgz").write_bytes(b"tampered transitive tarball")
        (self.project / "package.json").write_text(json.dumps(manifest))
        path = self.project / "package-lock.json"
        path.write_text(json.dumps(lock))
        return path.read_bytes()

    def ci(self):
        config = self.root / "empty-npmrc"
        config.write_text("")
        global_config = self.root / "global-npmrc"
        global_config.write_text("")
        return subprocess.run([self.npm, "ci", "--offline", "--ignore-scripts", "--no-audit", "--no-fund",
                               "--cache", str(self.root / "cache"), "--userconfig", str(config),
                               "--globalconfig", str(global_config)], cwd=self.project,
                              env={key: value for key, value in os.environ.items()
                                   if key in {"PATH", "HOME", "TMPDIR", "LANG", "LC_ALL"}},
                              capture_output=True, timeout=30, check=False)

    def test_npm_ci_rejects_changed_transitive_tarball(self):
        self.project_files(corrupt=True)
        result = self.ci()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(b"EINTEGRITY", result.stderr)

    def test_npm_ci_preserves_shared_lock_when_optional_platform_is_absent(self):
        before = self.project_files()
        result = self.ci()
        self.assertEqual(result.returncode, 0, result.stderr.decode())
        self.assertTrue((self.project / "node_modules/fixture-transitive/index.js").is_file())
        self.assertFalse((self.project / "node_modules/fixture-optional").exists())
        self.assertEqual((self.project / "package-lock.json").read_bytes(), before)
        result = subprocess.run([self.npm, "ls", "--all", "--json", "--long"], cwd=self.project,
                                env={key: value for key, value in os.environ.items()
                                     if key in {"PATH", "HOME", "TMPDIR", "LANG", "LC_ALL"}},
                                capture_output=True, timeout=30, check=False)
        self.assertEqual(result.returncode, 0, result.stderr.decode())
        direct = json.loads(result.stdout)["dependencies"]["fixture-root"]
        self.assertEqual(Path(direct["path"]).resolve(), (self.project / "node_modules/fixture-root").resolve())
        self.assertEqual(direct["dependencies"]["fixture-transitive"]["version"], "1.0.0")
        self.assertEqual(direct["dependencies"]["fixture-optional"], {})


class QualificationIdentityTests(unittest.TestCase):
    def test_live_receipt_keeps_all_npm_hashes_and_rejects_manifest_drift(self):
        runner = runpy.run_path(str(HERE.parent / "research/lab/qualify/run.py"))
        globals_ = runner["identities"].__globals__
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            versions = {"claude": VERSION, "codex": "0.163.0"}
            raw = json.dumps(fixture()).encode()
            frozen = N["bundle"](raw, VERSION, SRI)
            (root / "package-lock.json").write_bytes(raw)
            packages = fixture()["packages"]
            installed = {key: value for key, value in packages.items() if key}
            (root / "npm-tree.json").write_bytes(N["canonical"]({"resolved": packages, "installed": installed}))
            npm = N["verify"](root, VERSION, SRI, json.dumps(frozen))
            manifest = {"runtimes": {name: {"version": version, "binary_sha256": "a" * 64}
                                      for name, version in versions.items()}}
            manifest["runtimes"]["claude"].update(npm, source_integrity=SRI)
            path = root / "identity.json"
            path.write_text(json.dumps(manifest))
            (root / "bin").mkdir()
            (root / "bin/peerturn").write_bytes(b"synthetic PeerTurn")
            def command(argv, *_args, **_kwargs):
                name = Path(argv[0]).name
                return (VERSION + " (Claude Code)" if name == "claude" else
                        "codex-cli 0.163.0" if name == "codex" else "b" * 40).encode()
            values = {"ROOT": root, "command": command, "digest": lambda _path: "a" * 64,
                      "executable": lambda name: str(root / name), "child_environment": lambda _root: {}}
            environment = {"PEERTURN_QUAL_IDENTITY": str(path), "QUAL_ARTIFACTS": json.dumps({"claude": SRI}),
                           "QUAL_NPM_LOCK": json.dumps(frozen)}
            with patch.dict(globals_, values), patch.dict(os.environ, environment):
                summary = Mock()
                runner["identities"](SimpleNamespace(root=root), versions, summary)
                self.assertEqual({key: summary.identities["runtimes"]["claude"][key] for key in npm}, npm)
                manifest["runtimes"]["claude"]["npm_tree_sha256"] = "0" * 64
                path.write_text(json.dumps(manifest))
                with self.assertRaises(globals_["Failure"]):
                    runner["identities"](SimpleNamespace(root=root), versions, Mock())


if __name__ == "__main__":
    unittest.main()
