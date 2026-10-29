"""Track only the daemon reached through a run-owned private native socket."""
import ctypes
import os
from pathlib import Path
import platform
import signal
import socket
import stat
import struct
import time

from core import child_environment, command, require


def process_identity(pid, root):
    if platform.system() == "Linux":
        path = Path("/proc") / str(pid)
        try:
            metadata = (path / "stat").read_text()
            tail = metadata[metadata.rfind(")") + 2:].split()
            return (path.stat().st_uid, tail[19], str((path / "exe").resolve(strict=True)))
        except (OSError, IndexError):
            return None
    output = command(["/bin/ps", "-p", str(pid), "-o", "uid=", "-o", "lstart=", "-o", "comm="],
                     child_environment(root / "process-inspection"), root,
                     timeout=5, allowed=(0, 1)).decode().strip()
    fields = output.split(None, 6)
    return (int(fields[0]), " ".join(fields[1:6]), fields[6]) if len(fields) == 7 else None


class NativeDaemon:
    def __init__(self, alias, root):
        alias, root = Path(alias), Path(root).resolve()
        require(alias.is_relative_to(root), "native_contract_changed")
        target = alias.resolve(strict=True)
        metadata = target.stat()
        require(stat.S_ISSOCK(metadata.st_mode) and metadata.st_uid == os.getuid() and
                stat.S_IMODE(metadata.st_mode) == 0o600, "native_contract_changed")
        with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as connection:
            connection.settimeout(3)
            connection.connect(str(alias))
            if platform.system() == "Linux":
                pid, uid, _gid = struct.unpack("3i", connection.getsockopt(
                    socket.SOL_SOCKET, socket.SO_PEERCRED, struct.calcsize("3i")))
            else:
                pid = connection.getsockopt(0, 2)
                uid_value, gid_value = ctypes.c_uint(), ctypes.c_uint()
                libc = ctypes.CDLL(None, use_errno=True)
                require(libc.getpeereid(connection.fileno(), ctypes.byref(uid_value),
                                       ctypes.byref(gid_value)) == 0, "native_contract_changed")
                uid = uid_value.value
        require(pid > 1 and pid != os.getpid() and uid == os.getuid(), "native_contract_changed")
        self.pid, self.root = pid, root
        self.identity = process_identity(pid, root)
        require(self.identity is not None and self.identity[0] == uid, "native_contract_changed")

    def close(self):
        current = process_identity(self.pid, self.root)
        if current is None:
            return True
        if current != self.identity:
            return False
        try:
            os.kill(self.pid, signal.SIGTERM)
        except ProcessLookupError:
            return True
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline:
            current = process_identity(self.pid, self.root)
            if current is None:
                return True
            if current != self.identity:
                return False
            time.sleep(0.1)
        os.kill(self.pid, signal.SIGKILL)
        return False
