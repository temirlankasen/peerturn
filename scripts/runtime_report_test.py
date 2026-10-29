"""Only fixed failure facts may leave the private qualification runner."""
import importlib.util
import os
from pathlib import Path
import unittest
from unittest.mock import MagicMock, patch


ROOT = Path(__file__).resolve().parent.parent
SPEC = importlib.util.spec_from_file_location("runtime_report", ROOT / "scripts/runtime_report.py")
REPORT = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(REPORT)


class RuntimeReportTests(unittest.TestCase):
    def test_repeated_failure_updates_one_issue_without_raw_job_or_step_data(self):
        api = MagicMock(repo="example/peerturn")
        claim = "<!-- runtime-watch 2026-10-25.2 " + "a" * 64 + " -->"
        issue = {"number": 7, "title": "Runtime qualification: codex 0.164.0",
                 "user": {"login": "github-actions[bot]"}, "body": claim + "\nprivate untrusted text"}
        jobs = [{"name": "Live qualification / ubuntu-latest", "conclusion": "failure",
                 "steps": [{"name": "Live native contract qualification", "conclusion": "failure"},
                           {"name": "raw-provider-secret", "conclusion": "failure"}]},
                {"name": "raw-native-transcript", "conclusion": "failure"}]
        api.pages.side_effect = lambda suffix, *args, **kwargs: [issue] if suffix.startswith("issues?") else jobs
        environment = {"GITHUB_REF": "refs/heads/main", "GITHUB_EVENT_NAME": "workflow_dispatch", "GITHUB_RUN_ID": "123"}
        with patch.dict(os.environ, environment), patch.dict(REPORT.C, {"GitHub": lambda: api}), \
                patch.object(REPORT.sys, "argv", ["runtime_report.py", "codex", "0.164.0"]):
            REPORT.main()
            REPORT.main()
        self.assertEqual(api.put.call_count, 2)
        for call in api.put.call_args_list:
            self.assertEqual(call.args[0], "issues/7")
            self.assertEqual(call.args[2], "PATCH")
            body = call.args[1]["body"]
            self.assertIn(claim, body)
            self.assertIn("Live native contract qualification: failure", body)
            self.assertIn("actions/runs/123", body)
            for excluded in ("raw-provider-secret", "raw-native-transcript", "private untrusted text"):
                self.assertNotIn(excluded, body)

    def test_invalid_or_pr_context_cannot_open_issue(self):
        api = MagicMock()
        with patch.dict(os.environ, {"GITHUB_REF": "refs/pull/1/merge", "GITHUB_EVENT_NAME": "pull_request"}), \
                patch.dict(REPORT.C, {"GitHub": lambda: api}), \
                patch.object(REPORT.sys, "argv", ["runtime_report.py", "codex", "0.164.0"]):
            with self.assertRaises(ValueError):
                REPORT.main()
        api.put.assert_not_called()


if __name__ == "__main__":
    unittest.main()
