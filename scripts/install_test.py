"""Installer admission tests; real cosign/native acceptance lives in the lab."""
import hashlib
import io
import os
from pathlib import Path
import platform
import subprocess
import tarfile
import tempfile
import unittest


SCRIPT = Path(__file__).resolve().with_name("install.sh")


class InstallerTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.home = self.root / "home"
        self.home.mkdir()
        self.tools = self.root / "tools"
        self.tools.mkdir()
        self.downloads = self.root / "downloads"
        self.downloads.mkdir()
        os_name = {"Darwin": "darwin", "Linux": "linux"}[platform.system()]
        arch = {"arm64": "arm64", "aarch64": "arm64", "x86_64": "amd64"}[platform.machine()]
        self.artifact = f"peerturn_0.1.1-preview_{os_name}_{arch}.tar.gz"
        self.manifest = self.root / "peerturn_0.1.1-preview_checksums.txt"
        self.log = self.root / "calls"
        self.helper("cosign", '''#!/bin/sh
if [ "$1" = version ]; then printf '{"gitVersion": "v3.1.3"}\n'; exit; fi
printf '%s\n' "$*" >> "$FIXTURE_LOG"
[ "${BAD_SIGNATURE:-0}" != 1 ]
''')
        self.helper("curl", '''#!/bin/sh
printf '%s\n' "curl-first $1" >> "$FIXTURE_LOG"
[ "$1" = -q ] || exit 77
[ -s "$FIXTURE_ROOT/stderr" ] || exit 78
grep -q '^Release source:' "$FIXTURE_ROOT/stderr" || exit 78
grep -q '^Publisher trust:' "$FIXTURE_ROOT/stderr" || exit 78
[ ! -f "$FIXTURE_ROOT/warning-required" ] || grep -q '^WARNING: LABORATORY MODE' "$FIXTURE_ROOT/stderr" || exit 78
printf '%s\n' "$@" > "$FIXTURE_ROOT/curl-args"
max_time=0
speed_limit=0
speed_time=0
while [ "$#" -gt 0 ]; do
    case $1 in
        --output) output=$2; shift 2 ;;
        --max-time) max_time=$2; shift 2 ;;
        --speed-limit) speed_limit=$2; shift 2 ;;
        --speed-time) speed_time=$2; shift 2 ;;
        *) url=$1; shift ;;
    esac
done
cp "$FIXTURE_ROOT/curl-args" "$FIXTURE_ROOT/curl-args.${url##*/}"
printf '%s\n' "download ${url##*/}" >> "$FIXTURE_LOG"
if [ "${PROGRESS_SECONDS:-0}" -gt "$max_time" ]; then
    printf partial > "$output"
    printf '%s\n' 'curl: (28) fixture transfer exceeded overall timeout' >&2
    exit 28
fi
if [ "${STALLED_DOWNLOAD:-0}" = 1 ]; then
    printf partial > "$output"
    [ "$speed_limit" -gt 0 ] && [ "$speed_time" -gt 0 ] && [ "$speed_time" -le 120 ] || exit 79
    printf '%s\n' 'curl: (28) fixture transfer stalled' >&2
    exit 28
fi
cp "$FIXTURE_ROOT/${url##*/}" "$output"
''')
        self.helper("tar", '''#!/bin/sh
printf '%s\n' "tar $*" >> "$FIXTURE_LOG"
exec /usr/bin/tar "$@"
''')
        self.environment = dict(os.environ, HOME=str(self.home),
                                PATH=str(self.tools) + os.pathsep + os.environ["PATH"],
                                FIXTURE_ROOT=str(self.root), FIXTURE_LOG=str(self.log),
                                TMPDIR=str(self.downloads))
        self.environment.pop("PEERTURN_RELEASE_BASE_URL", None)
        self.package()

    def helper(self, name, body):
        path = self.tools / name
        path.write_text(body)
        path.chmod(0o755)

    def package(self, special=None):
        assets = {
            "peerturn": b"verified executable fixture",
            ".claude-plugin/marketplace.json": b'{"name":"peerturn"}',
            ".agents/plugins/marketplace.json": b'{"name":"peerturn"}',
            "adapters/codex/plugin/scripts/inform.sh": b"#!/bin/sh\n",
        }
        with tarfile.open(self.root / self.artifact, "w:gz") as archive:
            for name, data in assets.items():
                info = tarfile.TarInfo(name)
                info.size = len(data)
                info.mode = 0o755 if name == "peerturn" else 0o644
                archive.addfile(info, io.BytesIO(data))
            if special:
                archive.addfile(special)
        digest = hashlib.sha256((self.root / self.artifact).read_bytes()).hexdigest()
        self.manifest.write_text(f"{digest}  {self.artifact}\n")
        self.manifest.with_suffix(".txt.sigstore.json").write_text("signature fixture")

    def install(self, success=False, args=()):
        with (self.root / "stderr").open("w+") as stderr:
            result = subprocess.run(["/bin/sh", str(SCRIPT), *args], env=self.environment,
                                    stdout=subprocess.PIPE, stderr=stderr, text=True,
                                    timeout=20, check=False)
            stderr.seek(0)
            result.stderr = stderr.read()
        self.assertEqual(result.returncode == 0, success, result.stdout + result.stderr)
        self.assertEqual(list(self.downloads.iterdir()), [], "download workdir leaked")
        if not success:
            self.assertFalse((self.home / ".local/bin/peerturn").exists())
        return result

    def test_verified_archive_and_pinned_signer(self):
        result = self.install(success=True)
        calls = self.log.read_text()
        self.assertIn("--certificate-oidc-issuer https://token.actions.githubusercontent.com", calls)
        self.assertIn("release.yml@refs/tags/v0.1.1-preview", calls)
        self.assertNotIn("--insecure-ignore-tlog", calls)
        self.assertIn("curl-first -q", calls)
        self.assertIn("Release source: https://github.com/temirlankasen/peerturn/releases/download", result.stderr)
        self.assertIn("Publisher trust: GitHub OIDC", result.stderr)
        package = self.home / ".local/share/peerturn/0.1.1-preview"
        inventory = (package / ".peerturn-files.sha256").read_bytes()
        self.assertEqual((package / ".peerturn-verified").read_text().strip(),
                         hashlib.sha256(inventory).hexdigest())
        self.assertEqual((package / "adapters/codex/plugin/bin/peerturn").read_bytes(),
                         (self.home / ".local/bin/peerturn").read_bytes())

    def test_invalid_signature_precedes_archive_download(self):
        self.environment["BAD_SIGNATURE"] = "1"
        self.install()
        self.assertNotIn("download " + self.artifact, self.log.read_text())

    def test_progressing_download_can_exceed_five_minutes(self):
        self.environment["PROGRESS_SECONDS"] = "301"
        self.install(success=True)
        for asset in (self.manifest.name, self.manifest.name + ".sigstore.json", self.artifact):
            with self.subTest(asset=asset):
                args = (self.root / ("curl-args." + asset)).read_text().splitlines()
                self.assertEqual(args[0], "-q")
                for flag, value in (("--proto", "=https"), ("--proto-redir", "=https"),
                                    ("--connect-timeout", "15"), ("--max-filesize", "134217728")):
                    self.assertEqual(args[args.index(flag) + 1], value)
                self.assertGreater(int(args[args.index("--speed-limit") + 1]), 0)
                self.assertLessEqual(int(args[args.index("--speed-time") + 1]), 120)
                self.assertGreater(int(args[args.index("--speed-time") + 1]), 0)
                self.assertGreaterEqual(int(args[args.index("--max-time") + 1]), 1800)
                self.assertLessEqual(int(args[args.index("--max-time") + 1]), 7200)
                self.assertIn("--location", args)
                self.assertIn("--fail", args)

    def test_stalled_download_fails_and_removes_partial_workdir(self):
        self.environment["STALLED_DOWNLOAD"] = "1"
        self.assertIn("fixture transfer stalled", self.install().stderr)
        self.assertNotIn("tar ", self.log.read_text())

    def test_tampered_archive(self):
        with (self.root / self.artifact).open("ab") as file:
            file.write(b"tampered")
        self.assertIn("checksum mismatch", self.install().stderr)
        self.assertNotIn("tar ", self.log.read_text())

    def test_duplicate_manifest_entry(self):
        self.manifest.write_text(self.manifest.read_text() * 2)
        self.assertIn("uniquely listed", self.install().stderr)

    def test_archive_symlink_and_traversal(self):
        for name in ("adapters/link", "../escape"):
            with self.subTest(name=name):
                info = tarfile.TarInfo(name)
                if "link" in name:
                    info.type = tarfile.SYMTYPE
                    info.linkname = "/outside"
                self.package(info)
                self.install()
                self.assertFalse((self.root / "escape").exists())

    def test_installation_symlink(self):
        (self.home / ".local").symlink_to(self.root, target_is_directory=True)
        self.assertIn("symbolic link", self.install().stderr)

    def test_old_cosign_is_rejected(self):
        self.helper("cosign", '#!/bin/sh\nprintf \'{"gitVersion": "v3.0.6"}\\n\'\n')
        self.assertIn("cosign >=3.1.3", self.install().stderr)

    def test_release_source_requires_lab_before_any_download(self):
        self.environment["PEERTURN_RELEASE_BASE_URL"] = "https://release.example/download"
        self.assertIn("requires --lab", self.install().stderr)
        self.assertFalse(self.log.exists())

    def test_local_key_requires_lab_before_any_download(self):
        self.assertIn("requires --lab", self.install(args=("--verify-key", "/key.pub")).stderr)
        self.assertFalse(self.log.exists())

    def test_lab_warning_and_effective_trust_precede_download(self):
        key = self.root / "trusted.pub"
        key.write_text("synthetic public key")
        self.environment["PEERTURN_RELEASE_BASE_URL"] = "https://release.example/download"
        (self.root / "warning-required").touch()
        result = self.install(success=True, args=("--verify-key", str(key), "--lab"))
        self.assertIn("Release source: https://release.example/download", result.stderr)
        self.assertIn("Publisher trust: explicit local cosign key " + str(key), result.stderr)
        self.assertIn("no public OIDC/transparency claim", result.stderr)
        self.assertIn("--insecure-ignore-tlog=true", self.log.read_text())

    def test_lab_source_keeps_public_oidc_unless_key_is_explicit(self):
        self.environment["PEERTURN_RELEASE_BASE_URL"] = "https://release.example/download"
        self.install(success=True, args=("--lab",))
        self.assertIn("--certificate-identity", self.log.read_text())
        self.assertNotIn("--insecure-ignore-tlog", self.log.read_text())

    def test_missing_lab_key_fails_before_any_download(self):
        result = self.install(args=("--lab", "--verify-key", str(self.root / "missing.pub")))
        self.assertIn("public key is unavailable", result.stderr)
        self.assertFalse(self.log.exists())


if __name__ == "__main__":
    unittest.main()
