"""Fresh-history export acceptance fixtures, with no publication or remote."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time
import unittest


SCRIPTS = Path(__file__).resolve().parent
GIT = "/usr/bin/git"


class PublicExport(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        self.source = self.base / "source"
        self.source.mkdir()
        self.destination = self.base / "export"
        self.markers = self.base / "markers.txt"
        self.markers.write_text(r"\bprivate-fixture[.]invalid\b" + "\n")
        (self.source / "scripts").mkdir()
        for path in SCRIPTS.glob("public*"):
            shutil.copy2(path, self.source / "scripts" / path.name)
        for name in ("publish-check.sh", "export-public.sh", "export_public.py"):
            shutil.copy2(SCRIPTS / name, self.source / "scripts" / name)
        self.git(self.source, "init", "-q")
        self.git(self.source, "config", "user.name", "Example Maintainer")
        self.git(self.source, "config", "user.email", "maintainer" + "@" + "mail.example")
        self.put("committed content", "README.md")
        self.put("ignored.txt\n", ".gitignore")
        self.commit()

    def git(self, root, *args):
        env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
        return subprocess.run([GIT, "-C", str(root), *args], check=True,
                              env=env, capture_output=True, timeout=30).stdout

    def put(self, text, name):
        path = self.source / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(text if isinstance(text, bytes) else text.encode())

    def commit(self):
        self.git(self.source, "add", "--force", "--all")
        self.git(self.source, "commit", "-qm", "test: synthetic fixture")

    def export(self, destination=None, env=None, args=()):
        environment = dict(os.environ, PEERTURN_PRIVATE_MARKERS=str(self.markers))
        if env is not None:
            environment.update(env)
        return subprocess.run(["/bin/sh", str(self.source / "scripts/export-public.sh"),
                               str(destination or self.destination), *args], cwd=self.source,
                              env=environment, capture_output=True, timeout=60)

    def assert_no_commit(self, destination=None):
        self.assertNotEqual(subprocess.run([GIT, "-C", str(destination or self.destination),
                                            "rev-parse", "HEAD"],
                                           capture_output=True, timeout=30).returncode, 0)

    def test_head_snapshot_single_commit_and_unchanged_source(self):
        for name in (".run/private.txt", "bin/output.txt", "dist/output.txt", ".env.local", ".netrc"):
            self.put("uncommitted synthetic content", name)
        before = self.git(self.source, "status", "--porcelain=v1", "-z")
        head = self.git(self.source, "rev-parse", "HEAD")
        index = (self.source / ".git/index").read_bytes()
        result = self.export()
        self.assertEqual(result.returncode, 0, result.stderr.decode())
        self.assertEqual((self.destination / "README.md").read_text(), "committed content")
        for name in (".run", "bin", "dist", ".env.local", ".netrc"):
            self.assertFalse((self.destination / name).exists(), name)
        self.assertEqual(self.git(self.destination, "rev-list", "--count", "HEAD").strip(), b"1")
        metadata = self.git(self.destination, "log", "-1", "--format=%an <%ae>%n%cn <%ce>%n%B").decode()
        identity = metadata.splitlines()[0]
        self.assertTrue(identity.isascii())
        self.assertNotIn("Example Maintainer", identity)
        self.assertRegex(identity, r" <[0-9]+[+][A-Za-z0-9-]+@users[.]noreply[.]github[.]com>$")
        self.assertEqual(metadata.splitlines()[:2], [identity, identity])
        self.assertIn("Signed-off-by: " + identity, metadata)
        self.assertIn(head.strip(), result.stdout)
        self.assertEqual(self.git(self.destination, "remote"), b"")
        self.assertEqual(self.git(self.source, "rev-parse", "HEAD"), head)
        self.assertEqual((self.source / ".git/index").read_bytes(), index)
        self.assertEqual(self.git(self.source, "status", "--porcelain=v1", "-z"), before)

    def test_dirty_tracked_and_staged_changes_are_rejected(self):
        self.put("dirty", "README.md")
        self.assertNotEqual(self.export().returncode, 0)
        self.assertFalse(self.destination.exists())
        self.git(self.source, "add", "README.md")
        self.assertNotEqual(self.export().returncode, 0)
        self.assertFalse(self.destination.exists())

    def test_untracked_and_ignored_outside_boundary_are_rejected(self):
        for name in ("new.txt", "ignored.txt", "config.local.json"):
            with self.subTest(name=name):
                self.put("uncommitted synthetic content", name)
                result = self.export()
                absent = not self.destination.exists()
                if not absent:
                    shutil.rmtree(self.destination)
                (self.source / name).unlink()
                self.assertNotEqual(result.returncode, 0)
                self.assertTrue(absent)

    def test_hidden_tracked_state_excluded_with_public_dotfiles_retained(self):
        for name in (".private-fixture/project.yml", ".private-fixture/.gitignore",
                     "nested/.private-fixture/state.json", ".github/workflows/private-fixture.yml"):
            self.put("private-fixture.invalid", name)
        public = (".gitignore", ".dockerignore", ".editorconfig", ".gitattributes",
                  ".golangci.yml", ".goreleaser.yaml", ".github/CODEOWNERS",
                  ".github/workflows/ci.yml", ".claude-plugin/marketplace.json",
                  "adapters/claude/.claude-plugin/plugin.json",
                  "adapters/codex/plugin/.codex-plugin/plugin.json",
                  "adapters/codex/.agents/plugins/marketplace.json",
                  "packaging/codex/.agents/plugins/marketplace.json")
        for name in public:
            self.put("public fixture\n", name)
        self.commit()
        result = self.export()
        self.assertEqual(result.returncode, 0, result.stderr.decode())
        self.assertFalse((self.destination / ".private-fixture").exists())
        self.assertFalse((self.destination / "nested").exists())
        self.assertFalse((self.destination / ".github/workflows/private-fixture.yml").exists())
        for name in public:
            self.assertEqual((self.destination / name).read_text(), "public fixture\n")

    def test_existing_nested_and_symlink_destinations_are_rejected(self):
        self.destination.mkdir()
        self.assertNotEqual(self.export().returncode, 0)
        self.assertNotEqual(self.export(self.source / "nested").returncode, 0)
        link = self.base / "link"
        link.symlink_to(self.base, target_is_directory=True)
        self.assertNotEqual(self.export(link / "output").returncode, 0)

    def test_private_content_paths_and_symlinks_never_get_a_commit(self):
        for name, content in (("new.txt", "private-fixture.invalid"),
                              ("private-fixture.invalid.txt", "clean"),
                              ("new.txt", "person" + "@" + "mail.example")):
            with self.subTest(name=name, content=content):
                self.put(content, name)
                self.commit()
                result = self.export()
                self.assertNotEqual(result.returncode, 0)
                self.assertNotIn(content.encode(), result.stderr)
                self.assert_no_commit()
                if self.destination.exists():
                    shutil.rmtree(self.destination)
                (self.source / name).unlink()
                self.commit()
        (self.source / "linked.txt").symlink_to("README.md")
        self.commit()
        self.assertNotEqual(self.export().returncode, 0)
        self.assert_no_commit()

    def test_committed_ignored_inputs_are_scanned_and_modes_preserved(self):
        self.put("person" + "@" + "mail.example", "ignored.txt")
        self.commit()
        self.assertNotEqual(self.export().returncode, 0)
        self.assert_no_commit()
        if self.destination.exists():
            shutil.rmtree(self.destination)
        self.put("clean", "ignored.txt")
        (self.source / "ignored.txt").chmod(0o755)
        self.commit()
        self.assertEqual(self.export().returncode, 0)
        mode = self.git(self.destination, "ls-tree", "HEAD", "ignored.txt").split()[0]
        self.assertEqual(mode, b"100755")

    def test_pythonpath_and_git_environment_cannot_change_identity(self):
        (self.base / "shutil.py").write_text("import sys; sys.exit(0)")
        env = dict(PYTHONPATH=str(self.base), GIT_AUTHOR_NAME="Untrusted Identity",
                   GIT_COMMITTER_EMAIL="user" + "@" + "mail.example")
        result = self.export(env=env)
        self.assertEqual(result.returncode, 0, result.stderr.decode())
        metadata = self.git(self.destination, "log", "-1", "--format=%an <%ae>%n%cn <%ce>").decode()
        identity = metadata.splitlines()[0]
        self.assertNotIn("Untrusted Identity", metadata)
        self.assertNotIn("mail.example", metadata)
        self.assertTrue(identity.endswith("@" + "users.noreply.github.com>"))
        self.assertEqual(metadata.splitlines(), [identity, identity])

    def test_default_commit_dates_are_current_utc_despite_local_environment(self):
        before = int(time.time())
        result = self.export(env={"TZ": "XXX7", "GIT_AUTHOR_DATE": "946684800 -0700",
                                  "GIT_COMMITTER_DATE": "946684800 -0700"})
        after = int(time.time())
        self.assertEqual(result.returncode, 0, result.stderr.decode())
        metadata = self.git(self.destination, "cat-file", "commit", "HEAD")
        dates = [line.rsplit(b" ", 2)[1:] for line in metadata.split(b"\n\n", 1)[0].splitlines()
                 if line.startswith((b"author ", b"committer "))]
        self.assertEqual(len(dates), 2)
        for timestamp, offset in dates:
            self.assertEqual(offset, b"+0000")
            self.assertLessEqual(before, int(timestamp))
            self.assertLessEqual(int(timestamp), after)

    def test_explicit_timestamp_sets_both_raw_commit_dates(self):
        for timestamp in ("0", "1700000000", "253402300799"):
            with self.subTest(timestamp=timestamp):
                destination = self.base / ("export-" + timestamp)
                result = self.export(destination, args=("--timestamp", timestamp), env={"TZ": "XXX7"})
                self.assertEqual(result.returncode, 0, result.stderr.decode())
                metadata = self.git(destination, "cat-file", "commit", "HEAD")
                dates = [line.rsplit(b" ", 2)[1:] for line in metadata.split(b"\n\n", 1)[0].splitlines()
                         if line.startswith((b"author ", b"committer "))]
                self.assertEqual(dates, [[timestamp.encode(), b"+0000"]] * 2)

    def test_invalid_timestamps_fail_before_destination_creation(self):
        for timestamp in ("", "-1", "1.5", "now", "01700000000", "253402300800", "9" * 100):
            with self.subTest(timestamp=timestamp):
                self.assertNotEqual(self.export(args=("--timestamp", timestamp)).returncode, 0)
                self.assertFalse(self.destination.exists())

    def test_global_crlf_configuration_cannot_change_committed_blobs(self):
        self.git(self.source, "config", "core.autocrlf", "false")
        self.put(b"first\r\nsecond\r\n", "README.md")
        self.commit()
        home = self.base / "home"
        home.mkdir()
        (home / ".gitconfig").write_text("[core]\n\tautocrlf = true\n\tsafecrlf = true\n")
        expected = self.git(self.source, "rev-parse", "HEAD:README.md")
        result = self.export(env={"HOME": str(home)})
        self.assertEqual(result.returncode, 0, result.stderr.decode())
        self.assertEqual(self.git(self.destination, "rev-parse", "HEAD:README.md"), expected)

    def test_clean_filter_mutation_refuses_export_success(self):
        self.git(self.source, "config", "filter.fixture.clean", "/bin/cat")
        self.put("README.md filter=fixture\n", ".gitattributes")
        self.commit()
        home = self.base / "home"
        home.mkdir()
        (home / ".gitconfig").write_text("[filter \"fixture\"]\n\tclean = /usr/bin/tr a-z A-Z\n")
        result = self.export(env={"HOME": str(home)})
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn(b"snapshot exported", result.stdout)
        self.assertEqual(self.git(self.destination, "show", "HEAD:README.md"), b"COMMITTED CONTENT")

    def test_explicit_author_is_used_without_gitconfig_identity(self):
        self.git(self.source, "config", "user.name", "")
        self.git(self.source, "config", "user.email", "")
        identity = "Example Maintainer <42+example" + "@" + "users.noreply.github.com>"
        result = self.export(args=("--author", identity))
        self.assertEqual(result.returncode, 0, result.stderr.decode())
        metadata = self.git(self.destination, "log", "-1", "--format=%an <%ae>%n%cn <%ce>%n%B").decode()
        self.assertEqual(metadata.splitlines()[:2], [identity, identity])
        self.assertIn("Signed-off-by: " + identity, metadata)

    def test_private_author_and_invalid_identity_never_get_a_commit(self):
        for identity in ("Private-fixture.invalid <42+example" + "@" + "users.noreply.github.com>",
                         "Example Maintainer <user" + "@" + "mail.example>",
                         "Example\nInjected <42+example" + "@" + "users.noreply.github.com>"):
            with self.subTest(identity=identity):
                result = self.export(args=("--author", identity))
                self.assertNotEqual(result.returncode, 0)
                self.assertNotIn(identity.encode(), result.stderr)
                self.assert_no_commit()

    def test_missing_empty_or_invalid_markers_fail_closed(self):
        for content in (None, "", "[", ".*"):
            with self.subTest(content=content):
                if content is None:
                    self.markers.unlink()
                else:
                    self.markers.write_text(content)
                self.assertNotEqual(self.export().returncode, 0)
                self.assertFalse(self.destination.exists())
        self.markers.write_text("synthetic-marker\n")
        self.assertNotEqual(self.export(env={"PEERTURN_PRIVATE_MARKERS": ""}).returncode, 0)

    def test_untracked_excluded_source_markers_remain_private(self):
        relative = ".run/private/markers.txt"
        self.put(self.markers.read_bytes(), relative)
        result = self.export(args=("--private-markers", str(self.source / relative)))
        self.assertEqual(result.returncode, 0, result.stderr.decode())
        self.assertFalse((self.destination / ".run").exists())
        self.assertEqual(self.git(self.source, "ls-files", "--", relative), b"")

    def test_public_or_tracked_source_marker_files_are_rejected(self):
        self.put(self.markers.read_bytes(), "markers.txt")
        result = self.export(args=("--private-markers", str(self.source / "markers.txt")))
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.destination.exists())
        (self.source / "markers.txt").unlink()
        relative = ".run/private/markers.txt"
        self.put(self.markers.read_bytes(), relative)
        self.commit()
        result = self.export(args=("--private-markers", str(self.source / relative)))
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.destination.exists())

    def test_symlink_marker_files_are_rejected(self):
        relative = ".run/private/markers.txt"
        path = self.source / relative
        path.parent.mkdir(parents=True)
        path.symlink_to(self.markers)
        result = self.export(args=("--private-markers", str(path)))
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.destination.exists())

    def test_invalid_utf8_and_binary_refuse_commit(self):
        for content in (b"\xffinvalid text", b"binary\0content"):
            with self.subTest(content=content):
                self.put(content, "new.txt")
                self.commit()
                self.assertNotEqual(self.export().returncode, 0)
                self.assert_no_commit()
                if self.destination.exists():
                    shutil.rmtree(self.destination)


if __name__ == "__main__":
    unittest.main()
