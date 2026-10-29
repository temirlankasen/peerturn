"""Offline hostile-receipt and credential-boundary acceptance for promotion."""
import copy
import io
import json
from pathlib import Path
import runpy
import unittest
from unittest.mock import MagicMock, patch
import urllib.request
import zipfile


ROOT = Path(__file__).resolve().parent.parent
FIXTURE = runpy.run_path(str(ROOT / "scripts/runtime_promote_test.py"))
PROMOTE = FIXTURE["PROMOTE"]
E = PROMOTE.E


def archive(data, name="qualification-summary.json", extra=False):
    output = io.BytesIO()
    with zipfile.ZipFile(output, "w", compression=zipfile.ZIP_DEFLATED) as value:
        value.writestr(name, data)
        if extra:
            value.writestr("unexpected.txt", "ignored")
    return output.getvalue()


class RuntimeEvidenceTests(unittest.TestCase):
    def api(self):
        return FIXTURE["FakeAPI"]()

    def qualify(self, api):
        return E["qualified_platforms"](api, api.run, "claude", "2.1.308")

    def test_actual_receipt_architecture_is_kept(self):
        self.assertEqual(self.qualify(self.api()), FIXTURE["platforms"]())

    def test_both_platforms_must_qualify_the_same_integrity_pinned_npm_tree(self):
        for key in ("npm_lock_sha256", "npm_tree_sha256"):
            for value in (None, "invalid", "f" * 64):
                api = self.api()
                original = api.artifact
                def changed(artifact_id):
                    receipt = E["summary"](original(artifact_id))
                    if artifact_id == 2:
                        if value is None:
                            receipt["identities"]["runtimes"]["claude"].pop(key, None)
                        else:
                            receipt["identities"]["runtimes"]["claude"][key] = value
                    return archive(json.dumps(receipt))
                with self.subTest(key=key, value=value), patch.object(api, "artifact", side_effect=changed):
                    with self.assertRaises(ValueError):
                        self.qualify(api)

    def test_missing_ambiguous_stale_or_foreign_artifacts_are_refused(self):
        for change in (
            {"expired": True}, {"size_in_bytes": 0}, {"size_in_bytes": 1_000_001},
            {"size_in_bytes": True}, {"workflow_run": {"id": 456}}, {"workflow_run": []},
            {"workflow_run": {"id": 123, "head_sha": "c" * 40, "head_branch": "main"}},
        ):
            api = self.api()
            api.artifacts[0].update(change)
            with self.subTest(change=change), self.assertRaises(ValueError):
                self.qualify(api)
        for artifacts in ([], self.api().artifacts[:1], self.api().artifacts + self.api().artifacts[:1]):
            api = self.api()
            api.artifacts = artifacts
            with self.subTest(artifacts=artifacts), self.assertRaises(ValueError):
                self.qualify(api)

    def test_incomplete_or_contradictory_receipt_is_refused(self):
        original = E["summary"](self.api().artifact(1))
        cases = []
        for changes in ({"passed": False}, {"passed": 1}, {"schema": True}, {"runtime": "codex"},
                        {"version": "2.1.309"}, {"checks": {}}, {"identities": []}):
            cases.append(dict(original, **changes))
        for key, value in (("peerturn_commit", "c" * 40), ("os", "darwin"), ("arch", "sparc"),
                           ("arch", []), ("runtimes", {"claude": []}),
                           ("runtimes", {"claude": {"version": "2.1.309"}})):
            item = copy.deepcopy(original)
            item["identities"][key] = value
            cases.append(item)
        item = copy.deepcopy(original)
        item["checks"]["artifact_identity"] = False
        cases.append(item)
        item = copy.deepcopy(original)
        del item["identities"]["arch"]
        cases.append(item)
        for receipt in cases:
            api = self.api()
            with self.subTest(receipt=receipt), patch.object(api, "artifact", return_value=archive(json.dumps(receipt))), self.assertRaises(ValueError):
                self.qualify(api)

    def test_archive_is_bounded_and_has_one_data_only_summary(self):
        for data in (b"not a ZIP", archive("{}", "../qualification-summary.json"),
                     archive("{}", extra=True), archive("x" * 65537), archive(b"\xff"),
                     archive('{"schema":1,"schema":2}'), archive("null")):
            with self.subTest(data=data[:30]), self.assertRaises(ValueError):
                E["summary"](data)
        with self.assertRaises(ValueError):
            E["summary"](b"x" * 1_000_001)

    def test_platform_scopes_are_closed_and_unique(self):
        valid = FIXTURE["platforms"]()
        E["validate_platforms"](valid)
        for value in (None, [], {}, [valid[0]] * 2, valid * 9,
                      [dict(valid[0], os="windows")], [dict(valid[0], arch="x86_64")],
                      [dict(valid[0], adapter_revision="other")], [dict(valid[0], profile="ask")],
                      [dict(valid[0], extra="ignored")]):
            with self.subTest(value=value), self.assertRaises(ValueError):
                E["validate_platforms"](value)

    def test_redirect_drops_auth_and_denies_untrusted_origins(self):
        handler = E["ArtifactRedirect"]()
        request = urllib.request.Request("https://api.github.com/repos/example/peerturn/actions/artifacts/1/zip",
                                         headers={"Authorization": "Bearer test-placeholder"})
        target = "https://productionresultssa1.blob.core.windows.net/path?sig=placeholder"
        redirected = handler.redirect_request(request, None, 302, "", {}, target)
        self.assertIsNone(redirected.get_header("Authorization"))
        self.assertEqual(redirected.full_url, target)
        # Construct synthetic URL userinfo without an email-address literal.
        userinfo_target = target.replace("://", "://placeholder" + chr(64), 1)
        for url in ("http://productionresultssa1.blob.core.windows.net/path", "https://evil.invalid/path",
                    "https://productionresultssa1.blob.core.windows.net.evil.invalid/path",
                    userinfo_target,
                    "https://productionresultssa1.blob.core.windows.net:444/path",
                    "file:///tmp/summary", "https://127.0.0.1/path"):
            with self.subTest(url=url), self.assertRaises(ValueError):
                handler.redirect_request(request, None, 302, "", {}, url)
        with self.assertRaises(ValueError):
            handler.redirect_request(redirected, None, 302, "", {}, target)

    def test_download_is_bounded_and_auth_stays_on_fixed_api(self):
        api = self.api()
        api.token = "test-placeholder"
        opener = MagicMock()
        response = opener.open.return_value.__enter__.return_value
        response.read.return_value = b"archive"
        with patch.object(urllib.request, "build_opener", return_value=opener):
            self.assertEqual(E["download_artifact"](api, 1), b"archive")
            request = opener.open.call_args.args[0]
            self.assertEqual(request.full_url, "https://api.github.com/repos/example/peerturn/actions/artifacts/1/zip")
            self.assertEqual(request.get_header("Authorization"), "Bearer test-placeholder")
            self.assertEqual(opener.open.call_args.kwargs["timeout"], 30)
            response.read.assert_called_once_with(1_000_001)
            response.read.return_value = b"x" * 1_000_001
            with self.assertRaises(ValueError):
                E["download_artifact"](api, 1)
        for value in (True, 0, -1, "1", 2**63):
            with self.subTest(value=value), self.assertRaises(ValueError):
                E["download_artifact"](api, value)


if __name__ == "__main__":
    unittest.main()
