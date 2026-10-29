"""Private local package and HTTPS relay. Nothing contacts a deployed relay."""
import hashlib
import json
from pathlib import Path
import shutil
import socket
import subprocess
import urllib.request

from core import child_environment, command, digest, executable, require


ROOT = Path(__file__).resolve().parents[3]
FILES = (".claude-plugin/marketplace.json", "adapters/claude/.claude-plugin/plugin.json",
         "adapters/claude/hooks/hooks.json", "adapters/claude/hooks/register.js",
         "adapters/claude/skills/peerturn/SKILL.md", "adapters/codex/plugin/.codex-plugin/plugin.json",
         "adapters/codex/plugin/hooks/hooks.json", "adapters/codex/plugin/scripts/inform.sh",
         "adapters/codex/plugin/skills/peerturn/SKILL.md")


def package(run, binaries):
    root = run.root / "package"
    root.mkdir(mode=0o700)
    require((ROOT / "bin/peerturn").is_file(), "missing_binary")
    for name in FILES:
        destination = root / name
        destination.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        shutil.copyfile(ROOT / name, destination)
    destination = root / ".agents/plugins/marketplace.json"
    destination.parent.mkdir(mode=0o700, parents=True)
    shutil.copyfile(ROOT / "packaging/codex/.agents/plugins/marketplace.json", destination)
    for name in ("peerturn", "adapters/codex/plugin/bin/peerturn"):
        destination = root / name
        destination.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        shutil.copyfile(ROOT / "bin/peerturn", destination)
        destination.chmod(0o700)
    (root / "adapters/codex/plugin/scripts/inform.sh").chmod(0o700)
    files = sorted(p for p in root.rglob("*") if p.is_file())
    require(len(files) <= 256)
    inventory = "".join(digest(p) + "  " + p.relative_to(root).as_posix() + "\n" for p in files)
    (root / ".peerturn-files.sha256").write_text(inventory)
    tools = run.root / "tools"
    tools.mkdir(mode=0o700)
    for name, binary in binaries.items():
        (tools / name).symlink_to(binary)
    return root, hashlib.sha256(inventory.encode()).hexdigest()


def unused_port():
    with socket.socket() as probe:
        probe.bind(("127.0.0.1", 0))
        return probe.getsockname()[1]


class Relay:
    def __init__(self, run, binary):
        self.run, self.binary = run, str(binary)
        self.root = run.root / "relay"
        self.root.mkdir(mode=0o700)
        self.env = child_environment(self.root)
        self.port, self.private_port = unused_port(), unused_port()
        require(self.port != self.private_port, "process_failed")
        self.url = "https://127.0.0.1:" + str(self.port)
        self.certificate, key = self.root / "cert.pem", self.root / "key.pem"
        conf = self.root / "certificate.conf"
        conf.write_text('[req]\ndistinguished_name=dn\nx509_extensions=ext\nprompt=no\n'
                        '[dn]\nCN=PeerTurn disposable loopback relay\n[ext]\n'
                        'subjectAltName=IP:127.0.0.1\nbasicConstraints=critical,CA:TRUE\n'
                        'keyUsage=critical,digitalSignature,keyEncipherment,keyCertSign\n')
        command([executable("openssl"), "req", "-x509", "-newkey", "rsa:2048", "-nodes",
                 "-days", "1", "-config", str(conf), "-keyout", str(key),
                 "-out", str(self.certificate)], self.env, self.root, timeout=30)
        key.chmod(0o600)
        self.process = None

    def start(self, admin_keys):
        require(admin_keys and all(isinstance(key, str) and len(key) == 43 for key in admin_keys))
        data = self.root / "data"
        data.mkdir(mode=0o700)
        config = self.root / "relay.toml"
        config.write_text("\n".join([
            'listen = "127.0.0.1:' + str(self.port) + '"',
            'private_listen = "127.0.0.1:' + str(self.private_port) + '"',
            'public_authority = "127.0.0.1:' + str(self.port) + '"',
            'data_dir = ' + json.dumps(str(data)),
            'tls_cert = ' + json.dumps(str(self.certificate)),
            'tls_key = ' + json.dumps(str(self.root / "key.pem")),
            'admin_keys = ' + json.dumps(admin_keys), ""]))
        config.chmod(0o600)
        self.process = self.run.spawn([self.binary, "relay", "--config", str(config)],
                                      self.env, self.root, stdin=subprocess.DEVNULL,
                                      stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        self.run.wait(self.ready, 20)

    def ready(self):
        if self.process.poll() is not None:
            return False
        try:
            with urllib.request.urlopen("http://127.0.0.1:" + str(self.private_port) +
                                        "/readyz", timeout=1) as response:
                return response.status == 200
        except OSError:
            return False
