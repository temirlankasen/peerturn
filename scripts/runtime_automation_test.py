"""Offline acceptance for runtime qualification and publication bookkeeping."""
import importlib.util
import json
import os
from pathlib import Path
import re
import sys
import tempfile
import unittest
from unittest.mock import MagicMock, patch


ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(ROOT / "scripts"))
SPEC = importlib.util.spec_from_file_location("runtime_common", ROOT / "scripts/runtime_common.py")
COMMON = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(COMMON)
EVIDENCE = "https://github.com/example/peerturn/actions/runs/123"
PLATFORMS = [{"os": "linux", "arch": "arm64", "adapter_revision": COMMON.E["ADAPTER_REVISION"], "profile": COMMON.E["PROFILE"]}]


class RuntimeAutomationTests(unittest.TestCase):
    def test_issue_lookup_filters_creator_before_bounded_pagination(self):
        api = COMMON.GitHub.__new__(COMMON.GitHub)
        human = {"title": COMMON.title("claude", "2.1.308"), "user": {"login": "spam"}}
        bot = dict(human, number=7, user={"login": "github-actions[bot]"})
        api.get = MagicMock(side_effect=[[human] * 100, [bot]])
        self.assertEqual(COMMON.issue_for(api, "claude", "2.1.308"), bot)
        self.assertEqual(api.get.call_count, 2)
        for call in api.get.call_args_list:
            self.assertIn("creator=github-actions%5Bbot%5D", call.args[0])
        api.get = MagicMock(return_value=[human] * 100)
        with self.assertRaises(ValueError):
            COMMON.issue_for(api, "claude", "2.1.308")
        self.assertLessEqual(api.get.call_count, 10)

    def test_release_and_qualification_never_restore_or_save_caches(self):
        for name in ("release.yml", "runtime-qualify.yml", "ci.yml", "codeql.yml"):
            text = (ROOT / ".github/workflows" / name).read_text()
            self.assertNotIn("actions/cache", text, name)
            steps = re.split(r"(?m)^      - ", text)
            go_steps = [step for step in steps if "uses: actions/setup-go@" in step]
            self.assertTrue(go_steps, name)
            for step in go_steps:
                self.assertRegex(step, r"(?m)^          cache: false$", name)
            for value in re.findall(r"(?m)^\s+cache: (.+)$", text):
                self.assertEqual(value, "false", name)
        for name in ("release.yml", "runtime-watch.yml"):
            text = (ROOT / ".github/workflows" / name).read_text()
            self.assertNotIn("actions/download-artifact", text, name)

    def test_release_downloads_and_verifies_clean_modules_in_both_jobs(self):
        text = (ROOT / ".github/workflows/release.yml").read_text()
        for job in (text.split("  validate:\n")[1].split("  release:\n")[0],
                    text.split("  release:\n")[1]):
            self.assertIn("go clean -modcache -cache", job)
            self.assertIn("go mod download", job)
            self.assertIn("go mod verify", job)
            self.assertLess(job.index("go mod download"), job.index("go mod verify"))

    def test_live_key_and_base_require_main_environment_and_no_reusable_caller(self):
        text = (ROOT / ".github/workflows/runtime-qualify.yml").read_text()
        self.assertNotIn("workflow_call:", text)
        live = text.split("  live:\n")[1].split("  promote:\n")[0]
        self.assertIn("environment: runtime-qualification", live)
        self.assertIn("github.ref == 'refs/heads/main'", live)
        self.assertIn("secrets.PEERTURN_QUAL_LITELLM_KEY", live)
        self.assertIn("secrets.PEERTURN_QUAL_LITELLM_BASE", live)
        self.assertNotIn("vars.PEERTURN_QUAL_LITELLM_BASE", text)

    def test_release_pins_cover_user_facing_guidance(self):
        required = {"internal/relay/invite_page.go", "SECURITY.md", "SUPPORT.md", "GOVERNANCE.md",
                    "docs/README.md", "docs/m3.md", "docs/install-lab.md"}
        self.assertTrue(required <= COMMON.PIN_FILES)
        installer = (ROOT / "scripts/install.sh").read_text()
        pin = re.search(r"(?m)^version=(0\.1\.[0-9]+-preview)$", installer)[1]
        for name in COMMON.PIN_FILES | {"README.md"}:
            self.assertIn(pin, (ROOT / name).read_text(), name)
        page = (ROOT / "internal/relay/invite_page.go").read_text()
        self.assertIn("blob/v" + pin + "/README.md", page)

    def test_version_only_update_is_refused(self):
        root, data = self.fixture()
        before = data.read_bytes()
        with patch.dict(os.environ, {"GITHUB_REPOSITORY": "example/peerturn"}), self.assertRaises(ValueError):
            COMMON.update_records(root, "claude", "2.1.308", EVIDENCE)
        self.assertEqual(data.read_bytes(), before)

    def test_stable_versions_respect_minimum_and_canonical_identity(self):
        for runtime, version, expected in (
            ("claude", "2.1.306", (2, 1, 306)),
            ("claude", "2.1.308", (2, 1, 308)),
            ("codex", "0.163.0", (0, 163, 0)),
            ("codex", "10.20.30", (10, 20, 30)),
        ):
            self.assertEqual(COMMON.version_tuple(runtime, version), expected)
        for runtime, version in (
            ("other", "1.2.3"), ("claude", "2.1.305"), ("codex", "0.162.999"),
            ("claude", "v2.1.308"), ("claude", "2.1.308-rc.1"),
            ("claude", "2.1.308+build"), ("codex", "0.0163.0"),
            ("codex", " 0.164.0"), ("codex", "0.164.0\n"),
            ("codex", "0.164"), ("codex", "4294967296.0.0"),
            ("codex", "1.2.3;echo unsafe"), ("codex", ""),
        ):
            with self.subTest(runtime=runtime, version=version), self.assertRaises(ValueError):
                COMMON.version_tuple(runtime, version)

    def test_candidate_deduplication_is_per_runtime_and_exact_version(self):
        records = {"claude": [{"version": "2.1.306", "reference": "historical"}],
                   "codex": [{"version": "0.163.0", "reference": "historical"}]}
        self.assertFalse(COMMON.eligible("claude", "2.1.306", records))
        self.assertFalse(COMMON.eligible("codex", "0.163.0", records))
        self.assertTrue(COMMON.eligible("claude", "2.1.308", records))
        self.assertTrue(COMMON.eligible("codex", "0.164.0", records))

    def test_only_main_schedule_or_manual_context_is_allowed(self):
        base = {"GITHUB_REF": "refs/heads/main", "GITHUB_EVENT_NAME": "schedule"}
        COMMON.validate_context(base)
        COMMON.validate_context(dict(base, GITHUB_EVENT_NAME="workflow_dispatch"))
        for changes in (
            {"GITHUB_EVENT_NAME": "pull_request"},
            {"GITHUB_EVENT_NAME": "pull_request_target"},
            {"GITHUB_EVENT_NAME": "push"},
            {"GITHUB_REF": "refs/heads/feature"},
            {"GITHUB_REF": "refs/pull/1/merge"},
            {"GITHUB_REF": "refs/tags/v0.1.2-preview"},
            {"GITHUB_EVENT_NAME": ""}, {"GITHUB_REF": ""},
        ):
            with self.subTest(changes=changes), self.assertRaises(ValueError):
                COMMON.validate_context(dict(base, **changes))
        with self.assertRaises(ValueError):
            COMMON.validate_context({})

    def test_next_preview_uses_highest_existing_patch_and_floor(self):
        self.assertEqual(COMMON.next_tag([]), "v0.1.2-preview")
        self.assertEqual(COMMON.next_tag(["v0.1.0-preview"]), "v0.1.2-preview")
        tags = ["v0.1.4-preview", "v0.1.12-preview", "v0.1.3-preview", "v0.2.99-preview",
                "v9.9.999", "v0.1.100-preview.1", "v0.1.013-preview", "malformed"]
        self.assertEqual(COMMON.next_tag(tags), "v0.1.13-preview")

    def fixture(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        root = Path(temp.name)
        records = {"claude": [dict(version="2.1.306", reference="historical Claude evidence", **PLATFORMS[0])],
                   "codex": [dict(version="0.163.0", reference="historical Codex evidence", **PLATFORMS[0])]}
        data = root / "internal/runtimepolicy/verified.json"
        data.parent.mkdir(parents=True)
        data.write_text(json.dumps(records, indent=2) + "\n")
        (root / "README.md").write_text("# PeerTurn\n\n<!-- runtime-verification -->\n\nExisting text.\n")
        (root / "CHANGELOG.md").write_text("# Changelog\n\n## Unreleased\n\nExisting change.\n")
        return root, data

    def test_update_is_evidence_backed_idempotent_and_preserves_baselines(self):
        root, data = self.fixture()
        with patch.dict(os.environ, {"GITHUB_REPOSITORY": "example/peerturn"}):
            self.assertTrue(COMMON.update_records(root, "claude", "2.1.308", EVIDENCE, PLATFORMS))
            snapshot = {path: path.read_bytes() for path in (data, root / "README.md", root / "CHANGELOG.md")}
            self.assertFalse(COMMON.update_records(root, "claude", "2.1.308", EVIDENCE, PLATFORMS))
        self.assertEqual(snapshot, {path: path.read_bytes() for path in snapshot})
        records = json.loads(data.read_text())
        self.assertEqual(records["claude"][0], dict(version="2.1.306", reference="historical Claude evidence", **PLATFORMS[0]))
        self.assertEqual(records["claude"][1], dict(version="2.1.308", reference=EVIDENCE, **PLATFORMS[0]))
        self.assertEqual(records["codex"], [dict(version="0.163.0", reference="historical Codex evidence", **PLATFORMS[0])])
        for name in ("README.md", "CHANGELOG.md"):
            text = (root / name).read_text()
            self.assertIn("Verified with", text)
            self.assertIn("claude 2.1.308", text)
            self.assertIn(EVIDENCE, text)
            self.assertEqual(text.count("2.1.308"), 1)
        self.assertIn("<!-- runtime-verification -->", (root / "README.md").read_text())
        self.assertIn("Existing text.", (root / "README.md").read_text())
        self.assertIn("Existing change.", (root / "CHANGELOG.md").read_text())

    def test_update_refuses_bad_evidence_before_changing_any_file(self):
        root, data = self.fixture()
        paths = (data, root / "README.md", root / "CHANGELOG.md")
        original = {path: path.read_bytes() for path in paths}
        for evidence in (
            "", "http://github.com/example/peerturn/actions/runs/123",
            "https://github.com.evil.invalid/example/peerturn/actions/runs/123",
            "https://github.com/example/peerturn/actions/runs/0",
            "https://github.com/example/peerturn/actions/runs/123?token=secret",
            "https://github.com/other/repository/actions/runs/123",
            "https://github.com/example/peerturn/pull/123",
            "https://github.com/example/peerturn/actions/runs/123\nforged",
        ):
            with self.subTest(evidence=evidence), patch.dict(os.environ, {"GITHUB_REPOSITORY": "example/peerturn"}), self.assertRaises(ValueError):
                COMMON.update_records(root, "claude", "2.1.308", evidence, PLATFORMS)
            self.assertEqual(original, {path: path.read_bytes() for path in paths})

    def test_new_manual_qualification_refreshes_existing_version_evidence(self):
        root, data = self.fixture()
        renewed = EVIDENCE[:-3] + "124"
        with patch.dict(os.environ, {"GITHUB_REPOSITORY": "example/peerturn"}):
            COMMON.update_records(root, "claude", "2.1.308", EVIDENCE, PLATFORMS)
            self.assertTrue(COMMON.update_records(root, "claude", "2.1.308", renewed, PLATFORMS))
            self.assertFalse(COMMON.update_records(root, "claude", "2.1.308", renewed, PLATFORMS))
        entries = json.loads(data.read_text())["claude"]
        self.assertEqual([item["version"] for item in entries], ["2.1.306", "2.1.308"])
        self.assertEqual(entries[1]["reference"], renewed)
        self.assertEqual((root / "README.md").read_text().count("claude 2.1.308"), 1)

    def test_baseline_requalification_preserves_historical_reference(self):
        root, data = self.fixture()
        with patch.dict(os.environ, {"GITHUB_REPOSITORY": "example/peerturn"}):
            self.assertTrue(COMMON.update_records(root, "claude", "2.1.306", EVIDENCE, PLATFORMS))
            self.assertFalse(COMMON.update_records(root, "claude", "2.1.306", EVIDENCE, PLATFORMS))
        self.assertEqual(json.loads(data.read_text())["claude"][0]["reference"],
                         "historical Claude evidence; " + EVIDENCE)

    def test_new_platform_scope_preserves_existing_evidence(self):
        root, data = self.fixture()
        new_platform = [dict(PLATFORMS[0], os="darwin")]
        before = json.loads(data.read_text())["claude"][0]
        with patch.dict(os.environ, {"GITHUB_REPOSITORY": "example/peerturn"}):
            self.assertTrue(COMMON.update_records(root, "claude", "2.1.306", EVIDENCE, new_platform))
            self.assertFalse(COMMON.update_records(root, "claude", "2.1.306", EVIDENCE, new_platform))
        entries = json.loads(data.read_text())["claude"]
        self.assertEqual(entries[0], before)
        self.assertEqual(entries[1], dict(version="2.1.306", reference=EVIDENCE, **new_platform[0]))

    def test_update_validates_documents_before_replacing_registry(self):
        root, data = self.fixture()
        for name, replacement in (("README.md", "# Missing marker\n"),
                                  ("CHANGELOG.md", "# Missing Unreleased\n")):
            with self.subTest(document=name):
                path = root / name
                previous = path.read_bytes()
                path.write_text(replacement)
                before = {item: item.read_bytes() for item in (data, root / "README.md", root / "CHANGELOG.md")}
                with patch.dict(os.environ, {"GITHUB_REPOSITORY": "example/peerturn"}), self.assertRaises(ValueError):
                    COMMON.update_records(root, "claude", "2.1.308", EVIDENCE, PLATFORMS)
                self.assertEqual(before, {item: item.read_bytes() for item in before})
                path.write_bytes(previous)


if __name__ == "__main__":
    unittest.main()
