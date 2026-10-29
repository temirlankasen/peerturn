"""Exercise the pinned release config against synthetic, unpublished artifacts."""
import hashlib
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import unittest


ROOT = Path(__file__).resolve().parent.parent
EPOCH = 1_700_000_000


class ReleaseTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temp = tempfile.TemporaryDirectory()
        cls.addClassCleanup(cls.temp.cleanup)
        cls.work = Path(cls.temp.name)
        cls.fixture = cls.work / "fixture"
        cls.fixture.mkdir()
        cls.go = shutil.which("go")
        cls.git = shutil.which("git")
        if not cls.go or not cls.git:
            raise RuntimeError("go and git are required for release acceptance")
        pinned = re.search(r"^GORELEASER \?= \$\(GO\) run (\S+)$",
                           (ROOT / "Makefile").read_text(), flags=re.MULTILINE)
        if not pinned:
            raise RuntimeError("Makefile must declare the pinned GoReleaser command")
        cls.goreleaser = [cls.go, "run", pinned.group(1)]
        config = (ROOT / ".goreleaser.yaml").read_text()
        (cls.fixture / ".goreleaser.yaml").write_text(config)
        cls.inputs = re.findall(r"^      - src: (.+)$", config, flags=re.MULTILINE)
        cls.inputs += ["scripts/install.sh", "install.ps1"]
        for name in cls.inputs:
            target = cls.fixture / name
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(ROOT / name, target)
        (cls.fixture / "go.mod").write_text("module github.com/temirlankasen/peerturn\n\ngo 1.27.3\n")
        main = cls.fixture / "cmd/peerturn/main.go"
        main.parent.mkdir(parents=True)
        main.write_text('package main\nimport ("fmt"; "github.com/temirlankasen/peerturn/internal/version")\n'
                        'func main() { fmt.Println(version.Version, version.Commit, version.Date) }\n')
        version = cls.fixture / "internal/version/version.go"
        version.parent.mkdir(parents=True)
        version.write_text('package version\nvar Version, Commit, Date string\n')
        (cls.fixture / ".gitignore").write_text("dist/\n")
        cls.run_command([cls.git, "init", "-q"], cls.fixture)
        cls.run_command([cls.git, "add", "."], cls.fixture)
        environment = dict(os.environ, GIT_AUTHOR_DATE=f"{EPOCH} +0000",
                           GIT_COMMITTER_DATE=f"{EPOCH} +0000")
        cls.run_command([cls.git, "-c", "user.name=Release Test", "-c",
                         "user.email=release" + "@" + "example.invalid", "commit", "-qm", "fixture"],
                        cls.fixture, environment)
        cls.run_command([cls.git, "remote", "add", "origin",
                         "https://github.com/temirlankasen/peerturn.git"], cls.fixture)
        cls.run_command([cls.git, "tag", "v0.1.2-preview"], cls.fixture)
        # A deterministic synthetic SBOM keeps this fixture independent of Syft downloads.
        syft = cls.work / "syft"
        syft.write_text(f"#!{sys.executable}\nimport pathlib, sys\n"
                        "pathlib.Path(sys.argv[-1].split('=', 1)[1]).write_text('{}')\n")
        syft.chmod(0o755)
        cls.environment = dict(os.environ, PATH=str(cls.work) + os.pathsep + os.environ["PATH"])
        cls.snapshots = []
        cls.metadata = []
        cls.manifests = []
        for stamp, mode in ((EPOCH + 100, 0o644), (EPOCH + 500, 0o600)):
            for name in cls.inputs:
                target = cls.fixture / name
                os.utime(target, (stamp, stamp))
                target.chmod(mode)
            cls.run_command([*cls.goreleaser, "release", "--snapshot", "--clean"],
                            cls.fixture, cls.environment)
            dist = cls.fixture / "dist"
            archives = sorted(dist.glob("*.tar.gz"))
            if len(archives) != 4:
                raise AssertionError(f"expected four OS/architecture archives, got {archives}")
            cls.snapshots.append({file.name: hashlib.sha256(file.read_bytes()).hexdigest()
                                  for file in archives})
            metadata = {}
            for file in archives:
                with tarfile.open(file) as archive:
                    metadata[file.name] = archive.getmembers()
            cls.metadata.append(metadata)
            manifests = list(dist.glob("*_checksums.txt"))
            if len(manifests) != 1:
                raise AssertionError(f"expected one checksum manifest, got {manifests}")
            cls.manifests.append(manifests[0].read_text())

    @staticmethod
    def run_command(argv, cwd, environment=None):
        result = subprocess.run(argv, cwd=cwd, env=environment, capture_output=True,
                                text=True, check=False, timeout=300)
        if result.returncode:
            raise AssertionError(f"{argv[0]} failed ({result.returncode}):\n{result.stdout}\n{result.stderr}")

    def test_installers_in_signed_checksum_manifest(self):
        for manifest in self.manifests:
            entries = [line.split() for line in manifest.splitlines()]
            for name in ("install.sh", "install.ps1"):
                matches = [digest for digest, path in entries if path == name]
                path = ROOT / ("scripts" if name == "install.sh" else "") / name
                self.assertEqual(matches, [hashlib.sha256(path.read_bytes()).hexdigest()])

    def test_installers_have_workflow_provenance(self):
        workflow = (ROOT / ".github/workflows/release.yml").read_text()
        subject = re.search(r"subject-path: \|\n((?: {12}[^\n]+\n)+)", workflow)
        self.assertIsNotNone(subject, "release must declare attestation subjects")
        subjects = {line.strip() for line in subject.group(1).splitlines()}
        self.assertTrue({"scripts/install.sh", "install.ps1"} <= subjects, subjects)

    def test_all_tar_entries_have_fixed_metadata(self):
        for metadata in self.metadata:
            for name, members in metadata.items():
                for member in members:
                    with self.subTest(archive=name, member=member.name):
                        self.assertEqual((member.uid, member.gid, member.uname, member.gname, member.mtime),
                                         (0, 0, "root", "root", EPOCH))
                        executable = member.name in ("peerturn", "adapters/codex/plugin/scripts/inform.sh")
                        self.assertEqual(member.mode, 0o755 if executable else 0o644)

    def test_archives_reproduce_after_checkout_metadata_changes(self):
        self.assertEqual(self.snapshots[0], self.snapshots[1])


if __name__ == "__main__":
    unittest.main()
