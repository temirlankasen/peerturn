"""Regression fixtures for promotion authority, age, and bounded reconciliation."""
from datetime import datetime, timedelta, timezone
import importlib.util
import os
from pathlib import Path
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parent.parent
SPEC = importlib.util.spec_from_file_location("promotion_fixtures", ROOT / "scripts/runtime_promote_test.py")
T = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(T)
P = T.PROMOTE


class PromotionSecurityTests(unittest.TestCase):
    def setUp(self):
        self.fixture = T.RuntimePromoteTests()
        self.fixture.setUp()
        self.addCleanup(self.fixture.doCleanups)
        self.api = self.fixture.api

    def test_young_runtime_cannot_write_verification_or_release(self):
        with patch.dict(P.AGE, {"mature": lambda *args: False}):
            P.promote(self.api, self.api.run)
            self.fixture.main_entries = T.entries(with_ledger=True)
            P.reconcile_releases(self.api)
        self.assertEqual(self.fixture.writes(), [])

    def test_existing_branch_requires_expected_tree_and_bot_commit_identity(self):
        branch = "automation/verified-claude-2.1.308-123"

        def put(suffix, payload, method="POST"):
            raise P.C["APIError"](422)

        for changes in ({"tree": {"sha": "attacker-tree"}}, {"author": {"name": "attacker"}},
                        {"parents": [{"sha": "wrong-base"}]}):
            commit = {"tree": {"sha": "tree-new"}, "parents": [{"sha": T.MAIN}],
                      "author": P.BOT_AUTHOR, "committer": P.BOT_AUTHOR}
            commit.update(changes)
            self.api.calls.clear()
            with patch.object(self.api, "put", side_effect=put), patch.object(self.api, "get", side_effect=[
                    {"object": {"sha": T.COMMIT}}, commit]), self.assertRaises(P.UntrustedAutomation):
                P.automation_branch(self.api, branch, T.COMMIT, "tree-new", T.MAIN)
            self.assertFalse(any(call[1] == "pulls" or call[0] == "GRAPHQL" for call in self.api.calls))

    def test_existing_pr_requires_bot_app_repo_branch_and_exact_head(self):
        pr = T.valid_pr()
        invalid = [dict(pr, user={"login": "attacker"}), dict(pr, head=dict(pr["head"], sha="bad")),
                   dict(pr, head=dict(pr["head"], repo={"full_name": "other/repo"}))]
        for value in invalid:
            self.api.calls.clear()
            self.api.pulls = [value]
            with self.assertRaises(ValueError):
                P.enable_merge(self.api, "automation/verified-claude-2.1.308-123", T.COMMIT)
            self.assertEqual(self.fixture.writes(), [])
        self.api.pulls = [pr]
        for app in ({"slug": "other"}, None):
            self.api.pr_issue["performed_via_github_app"] = app
            with self.assertRaises(P.UntrustedAutomation):
                P.enable_merge(self.api, "automation/verified-claude-2.1.308-123", T.COMMIT)
        self.assertEqual(self.fixture.writes(), [])

    def test_existing_branch_and_pr_are_reused_only_for_expected_content(self):
        self.api.protected = True
        self.api.pulls = [T.valid_pr()]
        original = self.api.put

        def put(suffix, payload, method="POST"):
            if suffix == "git/refs":
                raise P.C["APIError"](422)
            return original(suffix, payload, method)

        commit = {"tree": {"sha": "tree-new"}, "parents": [{"sha": T.MAIN, "url": "ignored"}],
                  "author": P.BOT_AUTHOR, "committer": P.BOT_AUTHOR}
        with patch.object(self.api, "put", side_effect=put), patch.object(self.api, "get", side_effect=[
                {"object": {"sha": T.MAIN}}, {"object": {"sha": T.COMMIT}}, commit, self.api.pr_issue]):
            P.promote(self.api, self.api.run)
        self.assertTrue(any(call[0] == "GRAPHQL" for call in self.api.calls))
        self.assertFalse(any(call[1] == "pulls" for call in self.api.calls))

    def test_untrusted_branch_gets_a_fresh_verified_branch_and_pr(self):
        self.api.protected = True
        original_put, original_get = self.api.put, self.api.get
        branch = "automation/verified-claude-2.1.308-123"

        def put(suffix, payload, method="POST"):
            if suffix == "git/refs" and payload["ref"] == "refs/heads/" + branch:
                raise P.C["APIError"](422)
            return original_put(suffix, payload, method)

        def get(suffix):
            if suffix == "git/ref/heads/" + branch:
                return {"object": {"sha": "d" * 40}}
            if suffix == "git/commits/" + "d" * 40:
                return {"tree": {"sha": "attacker-tree"}}
            return original_get(suffix)

        with patch.object(self.api, "put", side_effect=put), patch.object(self.api, "get", side_effect=get):
            P.promote(self.api, self.api.run)
        refs = [call[2]["ref"] for call in self.api.calls if call[1] == "git/refs"]
        self.assertEqual(refs, ["refs/heads/" + branch + "-" + T.COMMIT[:12]])
        self.assertTrue(any(call[0] == "GRAPHQL" for call in self.api.calls))

    def test_untrusted_pr_gets_a_fresh_branch_without_adopting_its_author(self):
        self.api.protected = True
        self.api.pulls = [dict(T.valid_pr(), user={"login": "attacker"})]
        original = self.api.pages

        def pages(suffix, *args, **kwargs):
            if suffix.startswith("pulls?") and ":automation/" in suffix and T.COMMIT[:12] in suffix:
                return []
            return original(suffix, *args, **kwargs)

        with patch.object(self.api, "pages", side_effect=pages):
            P.promote(self.api, self.api.run)
        prs = [call[2]["head"] for call in self.api.calls if call[1] == "pulls"]
        self.assertEqual(prs, ["automation/verified-claude-2.1.308-123-" + T.COMMIT[:12]])
        self.assertTrue(any(call[0] == "GRAPHQL" for call in self.api.calls))

    def test_interrupted_same_run_replacement_pr_is_revalidated_and_resumed(self):
        self.api.protected = True
        branch = "automation/verified-claude-2.1.308-123"
        replacement = branch + "-" + T.COMMIT[:12]
        pr = T.valid_pr()
        pr["head"]["ref"] = replacement
        self.api.pending, self.api.pulls = [pr], [pr]
        original_put, original_get = self.api.put, self.api.get

        def put(suffix, payload, method="POST"):
            if suffix == "git/refs":
                raise P.C["APIError"](422)
            return original_put(suffix, payload, method)

        def get(suffix):
            if suffix == "git/ref/heads/" + branch:
                return {"object": {"sha": "d" * 40}}
            if suffix == "git/ref/heads/" + replacement:
                return {"object": {"sha": T.COMMIT}}
            if suffix == "git/commits/" + "d" * 40:
                return {"tree": {"sha": "attacker-tree"}}
            if suffix == "git/commits/" + T.COMMIT:
                return {"tree": {"sha": "tree-new"}, "parents": [{"sha": T.MAIN}],
                        "author": P.BOT_AUTHOR, "committer": P.BOT_AUTHOR}
            return original_get(suffix)

        with patch.object(self.api, "put", side_effect=put), patch.object(self.api, "get", side_effect=get):
            P.promote(self.api, self.api.run)
        self.assertTrue(any(call[0] == "GRAPHQL" for call in self.api.calls))
        self.assertTrue(any(call[1] == "actions/workflows/ci.yml/dispatches" for call in self.api.calls))

    def test_recent_run_inventory_has_fixed_page_bound(self):
        run = self.api.run
        with patch.object(self.api, "get", return_value={"workflow_runs": [run] * 100}) as get:
            P.requested_runs(self.api)
        self.assertEqual(get.call_count, 3)
        self.assertTrue(all("created=%3E%3D" in call.args[0] for call in get.call_args_list))

    def test_recorded_run_is_skipped_before_querying_jobs(self):
        records = {"claude": [{"version": "2.1.308", "reference": "https://github.com/example/peerturn/actions/runs/123"}],
                   "codex": []}
        with patch.object(P, "requested_runs", return_value=[(self.api.run, None)]), \
                patch.object(P, "qualification", side_effect=AssertionError("unnecessary jobs query")), \
                patch.object(P, "reconcile_releases"), patch.object(P.Path, "read_text", return_value=__import__("json").dumps(records)), \
                patch.dict(P.C, {"GitHub": lambda: self.api}), patch.object(P.sys, "argv", ["promote", "reconcile"]), \
                patch.dict(os.environ, {"GITHUB_REF": "refs/heads/main", "GITHUB_EVENT_NAME": "schedule"}):
            P.main()


class RuntimeAgeTests(unittest.TestCase):
    def test_exact_72_hours_boundary_and_invalid_timestamps(self):
        age = __import__("runpy").run_path(str(ROOT / "scripts/runtime_age.py"))
        now = datetime(2026, 10, 25, tzinfo=timezone.utc)
        for hours, expected in ((71.999, False), (72, True), (73, True), (-1, False)):
            stamp = (now - timedelta(hours=hours)).isoformat()
            self.assertEqual(age["old_enough"](stamp, now), expected)
        for stamp in (None, "", "invalid", "2026-10-01T00:00:00", 123):
            with self.subTest(stamp=stamp), self.assertRaises(ValueError):
                age["old_enough"](stamp, now)

    def test_publication_comes_from_exact_npm_time_or_github_release(self):
        age = __import__("runpy").run_path(str(ROOT / "scripts/runtime_age.py"))
        now = datetime(2026, 10, 25, tzinfo=timezone.utc)
        with patch.dict(age["mature"].__globals__, {"npm_time": lambda: {"2.1.308": "2026-10-22T00:00:00Z"}}):
            self.assertTrue(age["mature"](self.api, "claude", "2.1.308", now))
        release = {"tag_name": "rust-v0.164.0", "draft": False, "prerelease": False,
                   "published_at": "2026-10-22T00:00:01Z"}
        with patch.object(self.api, "request", return_value=release):
            self.assertFalse(age["mature"](self.api, "codex", "0.164.0", now))

    def setUp(self):
        self.api = T.FakeAPI()


if __name__ == "__main__":
    unittest.main()
