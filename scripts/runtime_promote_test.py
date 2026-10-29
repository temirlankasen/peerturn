"""Offline evidence and publication fencing for trusted qualification results."""
import importlib.util
import io
import json
import os
from pathlib import Path
import unittest
import zipfile
from unittest.mock import patch


ROOT = Path(__file__).resolve().parent.parent
SPEC = importlib.util.spec_from_file_location("runtime_promote", ROOT / "scripts/runtime_promote.py")
PROMOTE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(PROMOTE)
MAIN = "a" * 40
QUALIFIED = "b" * 40
COMMIT = "c" * 40
TAG = "v0.1.2-preview"
DISPATCH = "actions/workflows/release.yml/dispatches"
REQUIRED = ("Model-free contracts", "Live qualification / ubuntu-latest", "Live qualification / macos-latest")


def platforms():
    return [{"os": os_name, "arch": arch, "adapter_revision": PROMOTE.E["ADAPTER_REVISION"], "profile": PROMOTE.E["PROFILE"]}
            for os_name, arch in (("linux", "amd64"), ("darwin", "arm64"))]


def valid_run():
    return {"id": 123, "display_title": "Qualify claude 2.1.308", "head_branch": "main",
            "head_sha": QUALIFIED, "event": "workflow_dispatch", "status": "in_progress",
            "conclusion": None, "path": ".github/workflows/runtime-qualify.yml",
            "head_repository": {"full_name": "example/peerturn"}}


def valid_pr():
    return {"number": 8, "node_id": "PR_fixture", "user": {"login": "github-actions[bot]", "type": "Bot"},
            "head": {"sha": COMMIT, "ref": "automation/verified-claude-2.1.308-123", "repo": {"full_name": "example/peerturn"}},
            "base": {"ref": "main", "repo": {"full_name": "example/peerturn"}}}


class FakeAPI:
    repo = "example/peerturn"

    def __init__(self):
        self.calls = []
        self.run = valid_run()
        self.jobs = [{"name": name, "conclusion": "success"} for name in REQUIRED]
        self.artifacts = [{"id": index, "name": "qualification-claude-2.1.308-" + runner,
                           "expired": False, "size_in_bytes": 2048,
                           "workflow_run": {"id": 123, "head_sha": QUALIFIED, "head_branch": "main"}}
                          for index, runner in enumerate(("ubuntu-latest", "macos-latest"), 1)]
        self.protected = False
        self.pending = []
        self.pulls = []
        self.pr_issue = {"user": {"login": "github-actions[bot]"}, "performed_via_github_app": {"slug": "github-actions"}}
        self.ci_runs = []
        self.releases = []
        self.release_runs = []
        self.tag_ref = None
        self.uncertain_dispatch = False
        self.issues = [{"number": 7, "title": "Runtime qualification: claude 2.1.308", "body": "qualification claim",
                        "user": {"login": "github-actions[bot]"}}]

    def get(self, suffix):
        self.calls.append(("GET", suffix))
        if suffix == "git/ref/heads/main":
            return {"object": {"sha": MAIN}}
        if suffix == "actions/runs/123":
            return self.run
        if suffix == "issues/8":
            return self.pr_issue
        if suffix == "git/ref/tags/" + TAG:
            if self.tag_ref:
                return self.tag_ref
            raise PROMOTE.C["APIError"](404)
        raise AssertionError("unexpected GET: " + suffix)

    def pages(self, suffix, key=None, **_kwargs):
        self.calls.append(("PAGES", suffix))
        if suffix.endswith("/artifacts"):
            return self.artifacts
        if suffix.startswith("actions/runs/"):
            return list(self.jobs)
        if suffix == "tags":
            return [{"name": "v0.1.1-preview"}]
        if suffix == "pulls?state=open&base=main":
            return list(self.pending)
        if suffix.startswith("pulls?"):
            return list(self.pulls)
        if suffix.startswith("actions/workflows/ci.yml/runs?"):
            return list(self.ci_runs)
        if suffix == "releases":
            return list(self.releases)
        if suffix.startswith("actions/workflows/release.yml/runs?"):
            return list(self.release_runs)
        if suffix.startswith("issues?"):
            return list(self.issues)
        raise AssertionError("unexpected pages: " + suffix)

    def artifact(self, artifact_id):
        platform = platforms()[artifact_id - 1]
        receipt = {"schema": 1, "runtime": "claude", "version": "2.1.308", "passed": True,
                   "checks": dict.fromkeys(PROMOTE.E["CHECKS"], True),
                   "identities": {"peerturn_commit": QUALIFIED, "os": platform["os"],
                                  "arch": "x86_64" if platform["arch"] == "amd64" else "arm64",
                                  "runtimes": {"claude": {"version": "2.1.308", "npm_lock_sha256": "d" * 64,
                                                           "npm_tree_sha256": "e" * 64,
                                                           "npm_installed_tree_sha256": "f" * 64}}}}
        output = io.BytesIO()
        with zipfile.ZipFile(output, "w") as archive:
            archive.writestr("qualification-summary.json", json.dumps(receipt))
        return output.getvalue()

    def put(self, suffix, payload, method="POST"):
        self.calls.append((method, suffix, payload))
        if suffix == "git/trees":
            return {"sha": "tree-new"}
        if suffix == "git/commits":
            return {"sha": COMMIT}
        if suffix == "git/refs/heads/main" and self.protected:
            raise PROMOTE.C["APIError"](403)
        if suffix == "git/refs" and payload["ref"].startswith("refs/tags/"):
            self.tag_ref = {"object": {"sha": payload["sha"], "type": "commit"}}
            return self.tag_ref
        if suffix == "pulls":
            pr = valid_pr()
            pr["head"]["ref"] = payload["head"]
            return pr
        if suffix.startswith("issues/"):
            issue = next(item for item in self.issues if item["number"] == int(suffix.split("/")[1]))
            issue.update(payload)
        if suffix == "issues":
            issue = dict(payload, number=9, user={"login": "github-actions[bot]"})
            self.issues.append(issue)
            return issue
        if suffix == DISPATCH and self.uncertain_dispatch:
            raise PROMOTE.C["APIError"]("unavailable")
        return {}

    def request(self, path, payload):
        self.calls.append(("GRAPHQL", path, payload))
        return {"data": {"enablePullRequestAutoMerge": {"pullRequest": {"number": 8}}}}


def ledger():
    return [{"runtime": "claude", "version": "2.1.308", "run_id": 123,
             "qualified_sha": QUALIFIED, "tag": TAG, "policy": PROMOTE.C["POLICY"], "platforms": platforms()}]


def entries(with_ledger=False):
    contents = {
        PROMOTE.C["RECORDS"]: json.dumps({"claude": [{"version": "2.1.306", "reference": "historical"}],
                                        "codex": [{"version": "0.163.0", "reference": "historical"}]}),
        "README.md": "# PeerTurn\n\nCurrent 0.1.1-preview; historical 0.1.0-preview.\n\n<!-- runtime-verification -->\n",
        "CHANGELOG.md": "# Changelog\n\n## Unreleased\n",
        PROMOTE.C["RELEASES"]: json.dumps(ledger() if with_ledger else []),
        "internal/runtimepolicy/policy.go": "qualified code",
    }
    for path in PROMOTE.C["PIN_FILES"]:
        contents[path] = "version=0.1.1-preview\n" if path == "scripts/install.sh" else "Release pin 0.1.1-preview\n"
    return {path: {"sha": "sha-" + path, "content": text,
                   "mode": "100755" if path == "scripts/install.sh" else "100644"}
            for path, text in contents.items()}


class RuntimePromoteTests(unittest.TestCase):
    def setUp(self):
        self.api = FakeAPI()
        self.main_entries = entries()
        self.qualified_entries = entries()
        self.tree_patch = patch.object(PROMOTE, "tree", side_effect=self.repository_tree)
        self.tree_patch.start()
        self.addCleanup(self.tree_patch.stop)
        blob_patch = patch.object(PROMOTE, "blob", side_effect=lambda api, entry: entry["content"])
        blob_patch.start()
        self.addCleanup(blob_patch.stop)
        env_patch = patch.dict(os.environ, {"GITHUB_REPOSITORY": self.api.repo})
        env_patch.start()
        self.addCleanup(env_patch.stop)
        age_patch = patch.dict(PROMOTE.AGE, {"mature": lambda *args: True})
        age_patch.start()
        self.addCleanup(age_patch.stop)

    def repository_tree(self, api, sha):
        return "tree-base", self.qualified_entries if sha == QUALIFIED else self.main_entries

    def writes(self):
        return [call for call in self.api.calls if call[0] not in {"GET", "PAGES"}]

    def test_current_run_accepts_only_complete_successful_required_matrix(self):
        self.assertEqual(PROMOTE.qualification(self.api, self.api.run), ("claude", "2.1.308"))
        for jobs in (
            self.api.jobs[:-1],
            [dict(job, conclusion="failure") if index == 1 else job for index, job in enumerate(self.api.jobs)],
            [dict(job, conclusion=None) if index == 2 else job for index, job in enumerate(self.api.jobs)],
            [dict(job, name="different runner") if index == 2 else job for index, job in enumerate(self.api.jobs)],
            self.api.jobs + [dict(self.api.jobs[0])],
        ):
            with self.subTest(jobs=jobs):
                self.api.jobs = jobs
                with self.assertRaises(ValueError):
                    PROMOTE.qualification(self.api, self.api.run)
        self.assertEqual(self.writes(), [])

    def test_foreign_or_nonmain_run_is_refused_before_mutation(self):
        for changes in (
            {"head_branch": "feature"}, {"event": "pull_request"},
            {"path": ".github/workflows/untrusted.yml"},
            {"head_repository": {"full_name": "other/peerturn"}},
            {"display_title": "Qualify claude 2.1.305"},
            {"display_title": "Qualify codex 0.164.0; shell"},
            {"status": "completed", "conclusion": "cancelled"},
        ):
            with self.subTest(changes=changes), self.assertRaises(ValueError):
                PROMOTE.promote(self.api, dict(self.api.run, **changes))
        self.assertEqual(self.writes(), [])

    def test_source_mismatch_cannot_become_verified(self):
        self.qualified_entries["internal/runtimepolicy/policy.go"]["sha"] = "different-code"
        with self.assertRaises(ValueError):
            PROMOTE.promote(self.api, self.api.run)
        self.assertEqual(self.writes(), [])

    def test_promotion_keeps_only_platforms_proven_by_receipts(self):
        PROMOTE.promote(self.api, self.api.run)
        changes = next(call[2]["tree"] for call in self.writes() if call[1] == "git/trees")
        data = next(item["content"] for item in changes if item["path"] == PROMOTE.C["RECORDS"])
        records = [item for item in json.loads(data)["claude"] if item["version"] == "2.1.308"]
        self.assertEqual({(item.get("os"), item.get("arch")) for item in records},
                         {("linux", "amd64"), ("darwin", "arm64")})

    def test_missing_receipts_prevent_all_repository_writes(self):
        self.api.artifacts = []
        with self.assertRaises(ValueError):
            PROMOTE.promote(self.api, self.api.run)
        self.assertEqual(self.writes(), [])

    def test_release_refuses_platform_receipt_mismatch(self):
        self.main_entries = entries(with_ledger=True)
        value = ledger()
        value[0]["platforms"][0]["arch"] = "arm64"
        self.main_entries[PROMOTE.C["RELEASES"]]["content"] = json.dumps(value)
        with self.assertRaises(ValueError):
            PROMOTE.reconcile_releases(self.api)
        self.assertEqual(self.writes(), [])

    def test_protected_main_falls_back_to_checked_pr_without_force(self):
        self.api.protected = True
        PROMOTE.promote(self.api, self.api.run)
        writes = self.writes()
        main = next(call for call in writes if call[1] == "git/refs/heads/main")
        self.assertEqual(main[0], "PATCH")
        self.assertIs(main[2]["force"], False)
        branch = next(call for call in writes if call[1] == "git/refs")
        self.assertTrue(branch[2]["ref"].startswith("refs/heads/automation/verified-claude-2.1.308-"))
        self.assertTrue(any(call[1] == "pulls" for call in writes))
        self.assertTrue(any(call[1] == "actions/workflows/ci.yml/dispatches" for call in writes))
        self.assertTrue(any(call[0] == "GRAPHQL" for call in writes))
        self.assertFalse(any(call[1] == DISPATCH for call in writes))
        commit = next(call[2] for call in writes if call[1] == "git/commits")
        self.assertIn("Signed-off-by:", commit["message"])
        self.assertEqual(commit["parents"], [MAIN])
        changes = next(call[2]["tree"] for call in writes if call[1] == "git/trees")
        updated = {item["path"]: item for item in changes}
        for path in PROMOTE.C["PIN_FILES"]:
            self.assertIn("0.1.2-preview", updated[path]["content"])
            self.assertNotIn("0.1.1-preview", updated[path]["content"])
            self.assertEqual(updated[path]["mode"], self.main_entries[path]["mode"])
        self.assertIn("historical 0.1.0-preview", updated["README.md"]["content"])

    def test_pin_normalization_allows_version_changes_but_preserves_code(self):
        path = "scripts/install.sh"
        self.main_entries[path]["sha"] = "new-pin-only"
        self.main_entries[path]["content"] = "version=0.1.2-preview\n"
        self.assertTrue(PROMOTE.same_code(self.main_entries, self.qualified_entries, self.api))
        self.main_entries[path]["content"] += "unqualified shell command\n"
        self.assertFalse(PROMOTE.same_code(self.main_entries, self.qualified_entries, self.api))

    def test_executable_mode_change_requires_new_qualification(self):
        self.main_entries["scripts/install.sh"]["mode"] = "100644"
        self.assertFalse(PROMOTE.same_code(self.main_entries, self.qualified_entries, self.api))

    def test_pending_verification_pr_serializes_later_promotions(self):
        self.api.pending = [{"head": {"ref": "automation/verified-codex-0.164.0-456", "repo": {"full_name": self.api.repo}},
                             "base": {"repo": {"full_name": self.api.repo}}, "user": {"login": "github-actions[bot]"}}]
        PROMOTE.promote(self.api, self.api.run)
        self.assertEqual(self.writes(), [])

    def test_fork_or_nonbot_pr_cannot_block_qualification_queue(self):
        for repo, actor in (("untrusted/fork", "github-actions[bot]"), (self.api.repo, "untrusted-user")):
            self.api.calls.clear()
            self.api.pending = [{"head": {"ref": "automation/verified-codex-0.164.0-456", "repo": {"full_name": repo}},
                                 "base": {"repo": {"full_name": self.api.repo}}, "user": {"login": actor}}]
            PROMOTE.promote(self.api, self.api.run)
            self.assertTrue(any(call[1] == "git/refs/heads/main" for call in self.writes()))

    def test_workflow_call_cannot_authorize_a_runtime_even_with_selector(self):
        self.api.run.update(path=".github/workflows/trusted-caller.yml", display_title="Scheduled caller")
        self.api.jobs = [dict(job, name="qualify / " + job["name"]) for job in self.api.jobs]
        with self.assertRaises(ValueError):
            PROMOTE.qualification(self.api, self.api.run, ("claude", "2.1.308"))

    def test_watcher_recovers_only_explicit_dedicated_run(self):
        api = FakeAPI()
        with patch.dict(os.environ, {"QUALIFICATION_RUN": "123", "QUAL_RUNTIME": "claude", "QUAL_VERSION": "2.1.308"}):
            candidates = PROMOTE.requested_runs(api)
        self.assertEqual(candidates, [(api.run, ("claude", "2.1.308"))])

    def test_watcher_does_not_infer_requests_from_untrusted_title_fragments(self):
        api = FakeAPI()
        for run_id in ("0", "123; unsafe", "-1"):
            with patch.dict(os.environ, {"QUALIFICATION_RUN": run_id}), self.assertRaises(ValueError):
                PROMOTE.requested_runs(api)
        self.assertFalse(any(call[0] == "GET" for call in api.calls))

    def test_new_evidence_supersedes_only_unpublished_same_version_receipts(self):
        old = ledger()[0]
        published = dict(old, tag="v0.1.1-preview")
        other = dict(old, runtime="codex", version="0.164.0", tag="v0.1.3-preview")
        records = [old, published, other]
        snapshot = dict(published)
        PROMOTE.supersede_pending(records, "claude", "2.1.308", "v0.1.4-preview", {published["tag"]})
        self.assertEqual(old["superseded_by"], "v0.1.4-preview")
        self.assertEqual(published, snapshot)
        self.assertNotIn("superseded_by", other)

    def test_superseded_receipt_never_creates_or_moves_a_release_tag(self):
        self.main_entries = entries(with_ledger=True)
        value = ledger()
        value[0]["superseded_by"] = "v0.1.3-preview"
        self.main_entries[PROMOTE.C["RELEASES"]]["content"] = json.dumps(value)
        PROMOTE.reconcile_releases(self.api)
        self.assertEqual(self.writes(), [])

    def test_existing_ci_and_auto_merge_are_not_repeated_for_same_sha(self):
        self.api.pulls = [dict(valid_pr(), auto_merge={"enabledBy": "bot"})]
        self.api.ci_runs = [{"head_sha": COMMIT}]
        PROMOTE.enable_merge(self.api, "automation/verified-claude-2.1.308-123", COMMIT)
        self.assertEqual(self.writes(), [])

    def test_release_dispatch_uses_exact_tag_and_claim_before_mutation(self):
        self.main_entries = entries(with_ledger=True)
        PROMOTE.reconcile_releases(self.api)
        writes = self.writes()
        dispatch = next(call for call in writes if call[1] == DISPATCH)
        self.assertEqual(dispatch[2], {"ref": TAG})
        claims = [index for index, call in enumerate(writes)
                  if call[1].startswith("issues/") and "release-dispatch " + TAG in call[2].get("body", "")]
        self.assertTrue(claims, "release dispatch has no durable pre-dispatch claim")
        self.assertLess(claims[0], writes.index(dispatch))
        self.assertIn("qualification claim", self.api.issues[0]["body"])

    def test_existing_published_release_is_immutable(self):
        self.main_entries = entries(with_ledger=True)
        self.api.releases = [{"tag_name": TAG, "draft": False}]
        PROMOTE.reconcile_releases(self.api)
        self.assertEqual(self.writes(), [])

    def test_existing_release_attempt_prevents_second_signing_job(self):
        self.main_entries = entries(with_ledger=True)
        self.api.release_runs = [{"head_sha": MAIN, "head_branch": TAG, "conclusion": "failure"}]
        PROMOTE.reconcile_releases(self.api)
        self.assertFalse(any(call[1] == DISPATCH for call in self.writes()))

    def test_uncertain_release_dispatch_never_automatically_retries(self):
        self.main_entries = entries(with_ledger=True)
        self.api.uncertain_dispatch = True
        with self.assertRaises(PROMOTE.C["APIError"]):
            PROMOTE.reconcile_releases(self.api)
        self.api.uncertain_dispatch = False
        PROMOTE.reconcile_releases(self.api)
        self.assertEqual(len([call for call in self.writes() if call[1] == DISPATCH]), 1)

    def test_release_refuses_changed_code(self):
        self.main_entries = entries(with_ledger=True)
        self.qualified_entries["internal/runtimepolicy/policy.go"]["sha"] = "different-code"
        with self.assertRaises(ValueError):
            PROMOTE.reconcile_releases(self.api)
        self.assertEqual(self.writes(), [])

    def test_existing_unrelated_tag_is_never_moved_or_released(self):
        self.main_entries = entries(with_ledger=True)
        self.api.tag_ref = {"object": {"sha": "d" * 40, "type": "commit"}}
        unrelated = entries(with_ledger=True)
        unrelated["internal/runtimepolicy/policy.go"]["sha"] = "unqualified-source"

        def repository_tree(api, sha):
            if sha == "d" * 40:
                return "unrelated-tree", unrelated
            return self.repository_tree(api, sha)

        with patch.object(PROMOTE, "tree", side_effect=repository_tree), self.assertRaises(ValueError):
            PROMOTE.reconcile_releases(self.api)
        self.assertEqual(self.writes(), [])


if __name__ == "__main__":
    unittest.main()
