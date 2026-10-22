"""Publication scanner regressions with synthetic privacy fixtures only."""
import os
from pathlib import Path
import runpy
import shutil
import subprocess
import tempfile
import unittest


SCRIPTS = Path(__file__).resolve().parent
GIT = "/usr/bin/git"
SCAN = SCRIPTS / "publish-check.sh"


class PublicationCheck(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.git("init", "-q")

    def git(self, *args, input=None):
        environment = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
        environment.update(GIT_AUTHOR_DATE="1700000000 +0000", GIT_COMMITTER_DATE="1700000000 +0000")
        return subprocess.run([GIT, "-C", str(self.root), *args], check=True,
                              env=environment, input=input, capture_output=True, timeout=30).stdout

    def put(self, value, name="sample.txt"):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(value if isinstance(value, bytes) else value.encode())
        self.git("add", "--", name)

    def scan(self, *args, env=None):
        return subprocess.run(["/bin/sh", str(SCAN), *args], cwd=self.root,
                              env=env, capture_output=True, timeout=30)

    def rejected(self, value, name="sample.txt", args=("--cached", "--strict-index")):
        self.put(value, name)
        result = self.scan(*args)
        self.assertNotEqual(result.returncode, 0, result.stdout.decode())
        self.assertNotIn(value.encode() if isinstance(value, str) else value,
                         result.stderr)

    def test_isolates_current_directory_and_pythonpath(self):
        for module in ("ipaddress", "shutil", "subprocess"):
            with self.subTest(module=module):
                self.put("import sys; sys.exit(0)\n", module + ".py")
                self.put("/" + "Users" + "/someone/private")
                env = dict(os.environ, PYTHONPATH=str(self.root))
                self.assertNotEqual(self.scan("--cached", "--strict-index",
                                             env=env).returncode, 0)

    def test_sensitive_markers(self):
        markers = [
            "person" + "@" + "mail.example", "user+tag" + "@" + "example.com",
            "machine." + "tailnet." + "ts.net", "100." + "64.0.1",
            "100." + "127.255.254", "fd7a:" + "115c:a1e0::1234",
            "FD7A:" + "115C:A1E0:0:1:2:3:4",
            "-----BEGIN " + "OPENSSH PRIVATE KEY-----",
            "-----BEGIN " + "RSA PRIVATE KEY-----",
            "-----BEGIN " + "PRIVATE KEY-----",
            "gh" + "p_" + "a" * 36, "github" + "_pat_" + "a" * 60,
            "s" + "k-" + "a" * 48, "xo" + "xb-" + "1" * 30,
            "xo" + "xp-" + "1" * 30, "xo" + "xa-" + "1" * 30,
            "C:" + "\\" + "Users" + "\\someone\\secret", "\\/" + "Users" + "\\/someone",
            "%2F" + "Users%2Fsomeone", "%252f" + "Users%252fsomeone",
            "\\u002f" + "Users\\u002fsomeone", "/" + "home" + "/someone/key",
        ]
        for marker in markers:
            with self.subTest(marker=markers.index(marker)):
                self.rejected(marker)

    def test_public_identity_uses_generic_noreply_exception(self):
        self.put("Example Maintainer <42+example" + "@" + "users.noreply.github.com>")
        self.assertEqual(self.scan("--cached", "--strict-index").returncode, 0)
        self.rejected("42+example" + "@" + "users.noreply.github.com.private.invalid")

    def test_tailscale_network_boundaries_and_safe_examples(self):
        self.put("192.0.2.1 100.63.255.255 100.128.0.0 127.0.0.1 ::1\n"
                 "https://example.com /tmp/project /project-root\n")
        self.assertEqual(self.scan("--cached", "--strict-index").returncode, 0)

    def test_rfc1918_networks_rejected_with_public_boundaries_allowed(self):
        for marker in ("10." + "1.2.3", "172." + "16.1.2", "172." + "31.255.254",
                       "192." + "168.1.2"):
            with self.subTest(marker=marker):
                self.rejected(marker)
        self.put("9.255.255.255 11.0.0.0 172.15.255.255 172.32.0.0\n"
                 "192.167.255.255 192.169.0.0 192.0.2.1 127.0.0.1 ::1\n")
        self.assertEqual(self.scan("--cached", "--strict").returncode, 0)

    def test_hidden_state_excluded_with_exact_public_exceptions(self):
        policy = runpy.run_path(str(SCRIPTS / "public_policy.py"))
        excluded, descend = policy["exclusions"](SCRIPTS / "public-exclude.txt")
        for name in (".private-fixture/project.yml", "nested/.private-fixture/state.json",
                     ".github/workflows/private-fixture.yml", ".claude-plugin/private-fixture.json"):
            with self.subTest(name=name):
                self.assertTrue(excluded(name))
        for name in (".gitignore", ".dockerignore", ".editorconfig", ".gitattributes",
                     ".golangci.yml", ".goreleaser.yaml", ".github/workflows/ci.yml",
                     ".github/CODEOWNERS", ".claude-plugin/marketplace.json",
                     "adapters/claude/.claude-plugin/plugin.json",
                     "adapters/codex/plugin/.codex-plugin/plugin.json",
                     "adapters/codex/.agents/plugins/marketplace.json",
                     "packaging/codex/.agents/plugins/marketplace.json"):
            with self.subTest(name=name):
                self.assertFalse(excluded(name))
        self.assertTrue(descend(".github"))
        self.assertFalse(descend(".private-fixture"))

    def test_private_paths_do_not_depend_on_gitignore(self):
        for name in ("research/lab/results/new/raw/data.txt", ".run/output.txt",
                     "research/sources/source.txt", "research/lab/home/config.txt",
                     "research/lab/results/install-by-link/fixture.py"):
            with self.subTest(name=name):
                self.put("synthetic", name)
                self.assertNotEqual(self.scan("--cached", "--strict-index").returncode, 0)
        result = self.scan("--cached")
        self.assertEqual(result.returncode, 0, result.stderr.decode())
        self.assertIn(b"PREPARATION ONLY", result.stdout)

    def test_read_failures_and_unsupported_entries_fail_closed(self):
        self.rejected(b"\xffinvalid UTF-8")
        self.put("clean")
        (self.root / "sample.txt").unlink()
        self.assertNotEqual(self.scan().returncode, 0)
        (self.root / "sample.txt").symlink_to("missing-target")
        self.git("add", "sample.txt")
        self.assertNotEqual(self.scan("--cached", "--strict-index").returncode, 0)

    def test_scans_untracked_and_staged_content_separately(self):
        self.put("clean")
        (self.root / "sample.txt").write_text("/" + "Users" + "/secret")
        self.assertEqual(self.scan("--cached", "--strict-index").returncode, 0)
        self.assertNotEqual(self.scan().returncode, 0)
        (self.root / "sample.txt").write_text("clean")
        (self.root / "untracked.txt").write_text("person" + "@" + "mail.example")
        self.assertNotEqual(self.scan().returncode, 0)

    def test_external_private_markers_are_redacted_and_normalized(self):
        policy = runpy.run_path(str(SCRIPTS / "public_policy.py"))
        with tempfile.TemporaryDirectory() as directory:
            marker_file = Path(directory) / "markers.txt"
            marker_file.write_text(r"\bsynthetic-node[.]example[.]invalid\b" + "\n")
            markers = policy["private_markers"](marker_file)
            self.assertEqual(policy["findings"]("SYNTHETIC-NODE.EXAMPLE.INVALID", markers=markers),
                             ["private marker"])
            self.put("https://synthetic-node%2eexample%2einvalid")
            result = self.scan("--cached", "--strict", "--private-markers", str(marker_file))
            self.assertNotEqual(result.returncode, 0)
            self.assertNotIn(b"synthetic-node", result.stderr)
            self.assertIn(b"private marker", result.stderr)
            self.assertEqual(self.scan("--cached", "--strict").returncode, 0)

    def test_invalid_external_marker_files_fail_closed(self):
        self.put("clean")
        with tempfile.TemporaryDirectory() as directory:
            marker_file = Path(directory) / "markers.txt"
            for content in (None, "", "[", ".*"):
                with self.subTest(content=content):
                    if content is not None:
                        marker_file.write_text(content)
                    result = self.scan("--cached", "--private-markers", str(marker_file))
                    self.assertNotEqual(result.returncode, 0)

    def test_commit_metadata_includes_author_committer_and_message(self):
        self.put("clean")
        public_email = "42+example" + "@" + "users.noreply.github.com"
        private_email = "user" + "@" + "mail.example"
        self.git("config", "user.name", "Example Maintainer")
        self.git("config", "user.email", public_email)
        self.git("commit", "-qm", "test: safe metadata")
        self.assertEqual(self.scan("--cached", "--strict", "--commit").returncode, 0)
        for field in ("author", "committer", "message"):
            with self.subTest(field=field):
                self.git("config", "user.email", private_email if field == "committer" else public_email)
                author = "Example Maintainer <" + (private_email if field == "author" else public_email) + ">"
                message = private_email if field == "message" else "test: metadata"
                self.git("commit", "-q", "--allow-empty", "--author", author, "-m", message)
                result = self.scan("--cached", "--strict", "--commit")
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(b"commit metadata", result.stderr)
                self.assertNotIn(private_email.encode(), result.stderr)

    def test_root_commit_metadata_is_scanned_by_default(self):
        self.put("clean")
        private_email = "user" + "@" + "mail.example"
        self.git("config", "user.name", "Example Maintainer")
        self.git("config", "user.email", private_email)
        self.git("commit", "-qm", "test: synthetic root commit")
        result = self.scan("--cached", "--strict")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(b"commit metadata", result.stderr)
        self.assertNotIn(private_email.encode(), result.stderr)

    def test_shallow_clone_commit_metadata_requires_explicit_request(self):
        # CI checks out one commit; a contributor's own public identity there
        # is not an exported root and must not fail every pull request.
        self.put("clean")
        contributor = "user" + "@" + "mail.example"
        self.git("config", "user.name", "Example Contributor")
        self.git("config", "user.email", contributor)
        self.git("commit", "-qm", "test: base")
        self.git("commit", "-q", "--allow-empty", "-m", "test: contribution")
        clone = self.root.parent / (self.root.name + "-shallow")
        self.addCleanup(shutil.rmtree, clone, True)
        self.git("clone", "-q", "--depth", "1", "file://" + str(self.root), str(clone))
        shallow = subprocess.run(["/bin/sh", str(SCAN), "--cached", "--strict"], cwd=clone,
                                 capture_output=True, timeout=30)
        self.assertEqual(shallow.returncode, 0, shallow.stderr.decode())
        explicit = subprocess.run(["/bin/sh", str(SCAN), "--cached", "--strict", "--commit"], cwd=clone,
                                  capture_output=True, timeout=30)
        self.assertNotEqual(explicit.returncode, 0)
        self.assertIn(b"commit metadata", explicit.stderr)

    def test_commit_metadata_requires_utc_in_both_raw_date_headers(self):
        self.put("clean")
        self.git("config", "user.name", "Example Maintainer")
        self.git("config", "user.email", "42+example" + "@" + "users.noreply.github.com")
        self.git("commit", "-qm", "test: date metadata")
        metadata = self.git("cat-file", "commit", "HEAD")
        for field in ("author", "committer"):
            for offset in ("+0700", "-0700", "-0000"):
                with self.subTest(field=field, offset=offset):
                    header = next(line for line in metadata.splitlines()
                                  if line.startswith(field.encode() + b" "))
                    replacement = header.rsplit(b" ", 1)[0] + b" " + offset.encode()
                    # git commit normalizes -0000; build the exact raw header under test.
                    changed = metadata.replace(header, replacement, 1)
                    oid = self.git("hash-object", "-w", "-t", "commit", "--stdin", input=changed).strip()
                    self.git("update-ref", "HEAD", oid.decode("ascii"))
                    result = self.scan("--cached", "--strict", "--commit")
                    self.assertNotEqual(result.returncode, 0)
                    self.assertIn(b"commit metadata", result.stderr)
                    self.assertIn(b"UTC", result.stderr)
                    self.assertNotIn(offset.encode(), result.stderr)

    def test_policy_parse_failure_and_git_environment_fail_closed(self):
        copied = self.root / "checker"
        shutil.copytree(SCRIPTS, copied)
        (copied / "public-exclude.txt").write_text("tree ../outside\n")
        result = subprocess.run(["/bin/sh", str(copied / "publish-check.sh")],
                                cwd=self.root, capture_output=True, timeout=30)
        self.assertNotEqual(result.returncode, 0)
        self.put("person" + "@" + "mail.example")
        env = dict(os.environ, GIT_INDEX_FILE=str(self.root / "empty-index"))
        self.assertNotEqual(self.scan("--cached", "--strict-index", env=env).returncode, 0)

    def test_reviewed_lab_summaries_are_public(self):
        for name in ("research/lab/README.md", "research/lab/results/m3-claude/fixes/README.md",
                     "research/lab/results/install-by-link/evidence-summary.json"):
            self.put("synthetic public summary", name)
        self.assertEqual(self.scan("--cached", "--strict-index").returncode, 0)

    def test_strict_alias_rejects_private_paths(self):
        self.put("synthetic", "research/lab/results/unknown/raw/example.txt")
        self.assertNotEqual(self.scan("--cached", "--strict").returncode, 0)
        self.assertNotEqual(self.scan("--strict").returncode, 0)

    def test_public_module_versions_and_home_placeholders_are_safe(self):
        self.put("github.com/example/package" + "@" + "v1.2.3\n" +
                 "//<user-home>/.ssh/**\n" + "git format %an <%ae> %cn <%ce>\n")
        self.assertEqual(self.scan("--cached", "--strict").returncode, 0)


if __name__ == "__main__":
    unittest.main()
