"""Verify exact artifact installation with synthetic downloads and no agents."""
import base64
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import shutil
import tarfile
import tempfile
import unittest
from unittest.mock import MagicMock, patch
import urllib.request


ROOT = Path(__file__).resolve().parent.parent
SPEC = importlib.util.spec_from_file_location("runtime_install", ROOT / "scripts/runtime_install.py")
INSTALL = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(INSTALL)
FIXTURE_SPEC = importlib.util.spec_from_file_location("runtime_native_test", ROOT / "scripts/runtime_native_test.py")
FIXTURE = importlib.util.module_from_spec(FIXTURE_SPEC)
FIXTURE_SPEC.loader.exec_module(FIXTURE)
ASSET = "codex-aarch64-apple-darwin.tar.gz"


def archive_bytes(names):
    result = io.BytesIO()
    with tarfile.open(fileobj=result, mode="w:gz") as archive:
        for name, kind in names:
            member = tarfile.TarInfo(name)
            member.mode = 0o755
            if kind == "symlink":
                member.type = tarfile.SYMTYPE
                member.linkname = "../../outside"
                archive.addfile(member)
            else:
                content = b"synthetic executable bytes"
                member.size = len(content)
                archive.addfile(member, io.BytesIO(content))
    return result.getvalue()


class RuntimeInstallTests(unittest.TestCase):
    def test_install_processes_never_inherit_github_or_provider_credentials(self):
        result = MagicMock(returncode=0, stdout=b"synthetic version")
        values = {"GH_TOKEN": "synthetic-github-token", "GITHUB_TOKEN": "synthetic-github-token",
                  "ACTIONS_RUNTIME_TOKEN": "synthetic-runtime-token", "OPENAI_API_KEY": "synthetic-key",
                  "ANTHROPIC_API_KEY": "synthetic-key", "NODE_OPTIONS": "--import=bad",
                  "npm_config_registry": "https://evil.invalid", "NPM_CONFIG_USERCONFIG": "/private/npmrc"}
        with patch.dict(os.environ, values), patch.object(INSTALL.subprocess, "run", return_value=result) as run:
            self.assertEqual(INSTALL.command(["/usr/bin/synthetic", "--version"]), "synthetic version")
        environment = run.call_args.kwargs["env"]
        self.assertTrue(all(key not in environment for key in values))
        self.assertIn("PATH", environment)

    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        (self.root / "bin").mkdir()
        self.api = MagicMock()
        system = patch.object(INSTALL.platform, "system", return_value="Darwin")
        machine = patch.object(INSTALL.platform, "machine", return_value="arm64")
        system.start()
        machine.start()
        self.addCleanup(system.stop)
        self.addCleanup(machine.stop)

    def codex_release(self, data):
        return {"draft": False, "prerelease": False, "tag_name": "rust-v0.164.0",
                "assets": [{"name": ASSET,
                            "browser_download_url": "https://github.com/openai/codex/releases/download/rust-v0.164.0/" + ASSET,
                            "digest": "sha256:" + hashlib.sha256(data).hexdigest()}]}

    def test_download_refuses_untrusted_origins_before_opening(self):
        for url in (
            "http://github.com/openai/artifact", "file:///tmp/artifact", "https://evil.invalid/artifact",
            "https://github.com.evil.invalid/artifact", "https://token" + "@" + "github.com/artifact",
            "https://token:password" + "@" + "github.com/artifact", "https://release-assets.githubusercontent.com.evil.invalid/artifact",
        ):
            with self.subTest(url=url), patch.object(INSTALL.urllib.request, "build_opener") as build:
                with self.assertRaises(ValueError):
                    INSTALL.download(url, {"github.com", "release-assets.githubusercontent.com"})
                build.assert_not_called()

    def test_every_redirect_is_checked_before_following(self):
        captured = []
        response = MagicMock()
        response.__enter__.return_value.read.return_value = b"public artifact"

        def build(handler):
            captured.append(handler())
            opener = MagicMock()
            opener.open.return_value = response
            return opener

        with patch.object(INSTALL.urllib.request, "build_opener", side_effect=build):
            self.assertEqual(INSTALL.download("https://github.com/openai/artifact",
                                             {"github.com", "release-assets.githubusercontent.com"}), b"public artifact")
        request = urllib.request.Request("https://github.com/openai/artifact")
        for url in ("https://evil.invalid/artifact", "http://github.com/artifact", "https://token" + "@" + "github.com/artifact"):
            with self.subTest(url=url), self.assertRaises(ValueError):
                captured[0].redirect_request(request, None, 302, "Found", {}, url)
        redirected = captured[0].redirect_request(request, None, 302, "Found", {},
                                                 "https://release-assets.githubusercontent.com/artifact?signature=public")
        self.assertEqual(redirected.host, "release-assets.githubusercontent.com")

    def test_download_is_bounded(self):
        response = MagicMock()
        response.__enter__.return_value.read.return_value = b"12345"
        opener = MagicMock()
        opener.open.return_value = response
        with patch.object(INSTALL.urllib.request, "build_opener", return_value=opener), self.assertRaises(ValueError):
            INSTALL.download("https://github.com/artifact", {"github.com"}, limit=4)
        response.__enter__.return_value.read.assert_called_once_with(5)

    def test_codex_checks_digest_before_unpacking_or_executing(self):
        data = archive_bytes([("codex", "regular")])
        release = self.codex_release(data)
        release["assets"][0]["digest"] = "sha256:" + "0" * 64
        self.api.request.return_value = release
        with patch.object(INSTALL, "download", return_value=data), patch.object(INSTALL, "command") as command:
            with self.assertRaisesRegex(ValueError, "digest"):
                INSTALL.install("codex", "0.164.0", self.root, self.api)
            command.assert_not_called()
        self.assertFalse((self.root / "bin/codex").exists())

    def test_detection_identity_mismatch_stops_before_artifact_download(self):
        data = archive_bytes([("codex", "regular")])
        release = self.codex_release(data)
        release["id"] = 1
        release["assets"][0]["id"] = 2
        self.api.request.return_value = release
        with patch.object(INSTALL, "download") as download, self.assertRaises(ValueError):
            INSTALL.install("codex", "0.164.0", self.root, self.api, expected="0" * 64)
        download.assert_not_called()

    def test_codex_refuses_symlink_and_ambiguous_archives(self):
        for members in (
            [("codex", "symlink")],
            [("codex", "regular"), ("codex-other", "regular")],
            [("unrelated", "regular")],
        ):
            data = archive_bytes(members)
            self.api.request.return_value = self.codex_release(data)
            with self.subTest(members=members), patch.object(INSTALL, "download", return_value=data), patch.object(INSTALL, "command") as command:
                with self.assertRaises(ValueError):
                    INSTALL.install("codex", "0.164.0", self.root, self.api)
                command.assert_not_called()
            self.assertFalse((self.root / "bin/codex").exists())

    def test_codex_refuses_oversized_binary_before_extracting(self):
        member = tarfile.TarInfo("codex")
        member.size = 200_000_001
        archive = MagicMock()
        archive.__enter__.return_value.getmembers.return_value = [member]
        data = b"synthetic archive metadata"
        self.api.request.return_value = self.codex_release(data)
        with patch.object(INSTALL, "download", return_value=data), patch.object(INSTALL.tarfile, "open", return_value=archive), patch.object(INSTALL, "command") as command:
            with self.assertRaises(ValueError):
                INSTALL.install("codex", "0.164.0", self.root, self.api)
            archive.__enter__.return_value.extractfile.assert_not_called()
            command.assert_not_called()

    def test_codex_validates_release_and_unique_asset_before_download(self):
        data = archive_bytes([("codex", "regular")])
        for change in (
            {"draft": True}, {"prerelease": True}, {"tag_name": "rust-v0.165.0"},
            {"assets": []}, {"assets": self.codex_release(data)["assets"] * 2},
        ):
            self.api.request.return_value = dict(self.codex_release(data), **change)
            with self.subTest(change=change), patch.object(INSTALL, "download") as download, self.assertRaises(ValueError):
                INSTALL.install("codex", "0.164.0", self.root, self.api)
            download.assert_not_called()

    def test_binary_version_must_match_exact_artifact_version(self):
        data = archive_bytes([("codex", "regular")])
        self.api.request.return_value = self.codex_release(data)
        with patch.object(INSTALL, "download", return_value=data), patch.object(INSTALL, "command", return_value="codex-cli 0.165.0") as command:
            with self.assertRaisesRegex(ValueError, "version mismatch"):
                INSTALL.install("codex", "0.164.0", self.root, self.api)
            command.assert_called_once_with([str(self.root / "bin/codex"), "--version"])

    def test_valid_codex_install_records_actual_binary_and_source_hashes(self):
        data = archive_bytes([("codex", "regular")])
        self.api.request.return_value = self.codex_release(data)
        with patch.object(INSTALL, "download", return_value=data), patch.object(INSTALL, "command", return_value="codex-cli 0.164.0"):
            result = INSTALL.install("codex", "0.164.0", self.root, self.api)
        binary = self.root / "bin/codex"
        self.assertEqual(binary.read_bytes(), b"synthetic executable bytes")
        self.assertEqual(binary.stat().st_mode & 0o777, 0o700)
        self.assertEqual(result, {"version": "0.164.0", "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
                                  "source_integrity": "sha256:" + hashlib.sha256(data).hexdigest()})

    def test_claude_requires_the_contracts_lock_before_any_live_download_or_execution(self):
        with patch.object(INSTALL, "download") as download, patch.object(INSTALL, "command") as command:
            with self.assertRaises(ValueError):
                INSTALL.install("claude", "2.1.308", self.root, self.api)
        download.assert_not_called()
        command.assert_not_called()

    def install_claude(self, tree, output=FIXTURE.VERSION + " (Claude Code)"):
        identity = {"npm_lock_sha256": "d" * 64, "npm_tree_sha256": "e" * 64, "npm_installed_tree_sha256": "f" * 64}
        self.command = MagicMock(return_value=output)
        with patch.dict(INSTALL.N, {"install": MagicMock(return_value=identity)}), patch.object(INSTALL, "command", self.command):
            result = INSTALL.install("claude", FIXTURE.VERSION, self.root, self.api, FIXTURE.SRI, tree.contracts())
        return result, identity

    def refuse_claude(self, tree, reason="."):
        with self.assertRaisesRegex(ValueError, reason):
            self.install_claude(tree)
        self.command.assert_not_called()
        self.assertFalse((self.root / "bin/claude").is_symlink())

    def test_claude_links_the_native_binary_and_never_the_ignore_scripts_stub(self):
        tree = FIXTURE.Tree(self.root)
        result, identity = self.install_claude(tree)
        link = self.root / "bin/claude"
        self.assertEqual(link.resolve(), tree.binary)
        self.assertEqual((self.root / "node_modules/.bin/claude").resolve().read_bytes(), FIXTURE.PLACEHOLDER)
        self.command.assert_called_once_with([str(tree.binary), "--version"])
        self.assertEqual(result, dict(identity, version=FIXTURE.VERSION, source_integrity=FIXTURE.SRI,
                                      binary_sha256=hashlib.sha256(tree.header).hexdigest()))

    def test_claude_selects_the_package_of_the_linux_host(self):
        tree = FIXTURE.Tree(self.root, FIXTURE.LINUX)
        with (patch.object(INSTALL.platform, "system", return_value="Linux"),
              patch.object(INSTALL.platform, "machine", return_value="x86_64"),
              patch.object(os, "confstr", return_value="glibc 2.39")):
            self.install_claude(tree)
        self.assertEqual((self.root / "bin/claude").resolve(), tree.binary)
        self.assertTrue(str(tree.binary).endswith("claude-code-linux-x64/claude"))

    def test_claude_stub_without_the_platform_package_is_never_executed(self):
        tree = FIXTURE.Tree(self.root)
        shutil.rmtree(tree.package)
        tree.installed.pop(tree.key)
        self.refuse_claude(tree, "installed")

    def test_claude_native_binary_of_another_architecture_is_never_executed(self):
        tree = FIXTURE.Tree(self.root)
        tree.binary.write_bytes(FIXTURE.macho(FIXTURE.X64))
        self.refuse_claude(tree, "format")

    def test_claude_platform_package_of_another_version_is_never_executed(self):
        tree = FIXTURE.Tree(self.root)
        tree.packages[tree.key]["version"] = "2.1.307"
        self.refuse_claude(tree, "lock")

    def test_claude_binary_version_must_match_the_pinned_version(self):
        tree = FIXTURE.Tree(self.root)
        with self.assertRaisesRegex(ValueError, "version mismatch"):
            self.install_claude(tree, "2.1.309 (Claude Code)")

    def test_contracts_emit_the_same_lock_bundle_and_auditable_tree_artifact(self):
        source = "sha512-" + base64.b64encode(b"a" * 64).decode()
        lock = {"lockfileVersion": 3, "packages": {
            "": {"name": "peerturn-runtime-qualification", "version": "1.0.0",
                 "dependencies": {"@anthropic-ai/claude-code": "2.1.308"}},
            "node_modules/@anthropic-ai/claude-code": {"version": "2.1.308", "integrity": source,
                "resolved": "https://registry.npmjs.org/claude-code.tgz"}}}
        frozen = INSTALL.N["bundle"](json.dumps(lock).encode(), "2.1.308", source)
        outputs = {}
        environment = {"GITHUB_REF": "refs/heads/main", "GITHUB_EVENT_NAME": "workflow_dispatch"}
        cwd = Path.cwd()
        try:
            os.chdir(self.root)
            with (
                patch.dict(os.environ, environment),
                patch.object(INSTALL.sys, "argv", ["runtime_install.py", "identity", "claude", "2.1.308", ""]),
                patch.dict(INSTALL.C, {"GitHub": MagicMock(), "output": outputs.__setitem__}),
                patch.dict(INSTALL.N, {"resolve": MagicMock(return_value=frozen)}),
                patch.object(INSTALL, "source_identity", side_effect=[source, "c" * 64]),
            ):
                INSTALL.main()
        finally:
            os.chdir(cwd)
        self.assertEqual(json.loads(outputs["npm_lock"]), frozen)
        artifact = json.loads((self.root / "qualification-npm-lock.json").read_text())
        self.assertEqual(artifact["resolved_tree"], lock["packages"])
        self.assertEqual(base64.b64decode(artifact["package_lock"]), json.dumps(lock).encode())
        self.assertEqual(artifact["package_json"]["dependencies"], {"@anthropic-ai/claude-code": "2.1.308"})


if __name__ == "__main__":
    unittest.main()
