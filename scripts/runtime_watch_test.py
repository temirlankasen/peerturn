"""Exercise upstream discovery and durable dispatch fencing without network."""
import importlib.util
from pathlib import Path
import unittest
from unittest.mock import patch


ROOT = Path(__file__).resolve().parent.parent
SPEC = importlib.util.spec_from_file_location("runtime_watch", ROOT / "scripts/runtime_watch.py")
WATCH = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(WATCH)
INTEGRITY = "sha512-" + "A" * 86 + "=="
RECORDS = {"claude": [{"version": "2.1.306", "reference": "historical"}],
           "codex": [{"version": "0.163.0", "reference": "historical"}]}


class FakeAPI:
    def __init__(self, pages=None, issues=None, fail=None):
        self.release_pages = pages or [[]]
        self.issues = list(issues or [])
        self.calls = []
        self.fail = fail

    def request(self, path):
        self.calls.append(("GET", path))
        number = int(path.rsplit("page=", 1)[1])
        return self.release_pages[number - 1]

    def pages(self, suffix, **_kwargs):
        self.calls.append(("PAGES", suffix))
        return list(self.issues)

    def put(self, suffix, payload, method="POST"):
        self.calls.append((method, suffix, payload))
        if suffix == self.fail:
            raise WATCH.C["APIError"]("unavailable")
        if suffix == "issues":
            self.issues.append(dict(payload, number=7, user={"login": "github-actions[bot]"}))
        elif suffix.startswith("issues/"):
            number = int(suffix.rsplit("/", 1)[1])
            issue = next(item for item in self.issues if item["number"] == number)
            issue.update(payload)


def release(version="0.164.0", **changes):
    value = {"id": 10, "tag_name": "rust-v" + version, "draft": False, "prerelease": False,
             "assets": [{"id": 20, "name": "codex-aarch64-unknown-linux-gnu.tar.gz", "digest": "sha256:" + "a" * 64}]}
    value.update(changes)
    return value


class RuntimeWatchTests(unittest.TestCase):
    def metadata(self, suffix, field):
        if field == "version":
            return "2.1.308"
        if field == "versions":
            return ["2.1.308", "2.1.305", "2.1.306", "2.1.307", "2.1.309-rc.1", "invalid"]
        self.assertEqual(field, "dist.integrity")
        self.assertIn(suffix, ("@2.1.307", "@2.1.308"))
        return INTEGRITY

    def test_discovery_pages_all_releases_and_filters_known_draft_and_prerelease(self):
        first_page = [release(draft=True) for _ in range(100)]
        second_page = [release(), release("0.163.0"), release("0.165.0-rc.1"),
                       release("0.166.0", prerelease=True), release("0.167.0", draft=True),
                       release(tag_name="v0.168.0"), release("0.162.99")]
        api = FakeAPI([first_page, second_page])
        with patch.object(WATCH, "npm", side_effect=self.metadata) as metadata:
            found = WATCH.candidates(api, RECORDS)
        self.assertEqual([(item["runtime"], item["version"]) for item in found],
                         [("claude", "2.1.307"), ("claude", "2.1.308"), ("codex", "0.164.0")])
        self.assertEqual(len(api.calls), 2)
        self.assertIn("page=2", api.calls[-1][1])
        self.assertEqual(metadata.call_count, 4)
        self.assertTrue(all(item["identity"] for item in found))

    def test_inventory_and_integrity_failures_stop_discovery(self):
        for latest, versions, integrity in (
            ("2.1.308", ["2.1.306"], INTEGRITY),
            ("2.1.308", "2.1.308", INTEGRITY),
            ("2.1.308", ["2.1.308"], "sha512-invalid"),
            ("2.1.308", ["2.1.308"], {"digest": INTEGRITY}),
        ):
            with self.subTest(versions=versions, integrity=integrity):
                api = FakeAPI()
                with patch.object(WATCH, "npm", side_effect=[latest, versions, integrity]), self.assertRaises(ValueError):
                    WATCH.candidates(api, RECORDS)
                self.assertEqual(api.calls, [])

    def test_changed_upstream_artifact_gets_distinct_candidate_identity(self):
        with patch.object(WATCH, "npm", side_effect=["2.1.306", ["2.1.306"]]):
            old = WATCH.candidates(FakeAPI([[release()]]), RECORDS)[0]
        changed = release(assets=[{"id": 21, "name": "codex-aarch64-unknown-linux-gnu.tar.gz",
                                    "digest": "sha256:" + "b" * 64}])
        with patch.object(WATCH, "npm", side_effect=["2.1.306", ["2.1.306"]]):
            new = WATCH.candidates(FakeAPI([[changed]]), RECORDS)[0]
        self.assertNotEqual(old["identity"], new["identity"])

    def test_durable_claim_precedes_dispatch_and_deduplicates_repeat_sweep(self):
        api = FakeAPI()
        candidate = {"runtime": "claude", "version": "2.1.308", "identity": INTEGRITY}
        self.assertTrue(WATCH.dispatch(api, candidate))
        writes = [call for call in api.calls if call[0] != "PAGES"]
        self.assertEqual([call[1] for call in writes],
                         ["issues", "actions/workflows/runtime-qualify.yml/dispatches"])
        self.assertIn(WATCH.C["POLICY"], writes[0][2]["body"])
        self.assertEqual(writes[1][2], {"ref": "main", "inputs": {"runtime": "claude", "version": "2.1.308", "identity": INTEGRITY}})
        count = len(api.calls)
        self.assertFalse(WATCH.dispatch(api, candidate))
        self.assertEqual(len(api.calls), count + 1)

    def test_uncertain_dispatch_is_not_automatically_retried(self):
        suffix = "actions/workflows/runtime-qualify.yml/dispatches"
        api = FakeAPI(fail=suffix)
        candidate = {"runtime": "codex", "version": "0.164.0", "identity": "c" * 64}
        with self.assertRaises(WATCH.C["APIError"]):
            WATCH.dispatch(api, candidate)
        self.assertEqual(len(api.issues), 1)
        api.fail = None
        self.assertFalse(WATCH.dispatch(api, candidate))
        dispatches = [call for call in api.calls if call[1] == suffix]
        self.assertEqual(len(dispatches), 1)

    def test_failed_claim_never_dispatches_paid_run(self):
        api = FakeAPI(fail="issues")
        with self.assertRaises(WATCH.C["APIError"]):
            WATCH.dispatch(api, {"runtime": "claude", "version": "2.1.308", "identity": INTEGRITY})
        self.assertFalse(any("dispatches" in call[1] for call in api.calls))

    def test_changed_artifact_claim_reopens_only_matching_bot_issue(self):
        title = WATCH.C["title"]("claude", "2.1.308")
        human = {"number": 1, "title": title, "body": "human report", "user": {"login": "owner"}}
        bot = {"number": 2, "title": title, "body": "old claim", "user": {"login": "github-actions[bot]"}}
        api = FakeAPI(issues=[human, bot])
        self.assertTrue(WATCH.dispatch(api, {"runtime": "claude", "version": "2.1.308", "identity": INTEGRITY}))
        self.assertEqual(human["body"], "human report")
        self.assertEqual(bot["state"], "open")
        self.assertIn(INTEGRITY, bot["body"])
        self.assertTrue(any(call[0] == "PATCH" and call[1] == "issues/2" for call in api.calls))


if __name__ == "__main__":
    unittest.main()
