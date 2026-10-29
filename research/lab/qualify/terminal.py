"""Bounded private PTY automation. Only recognized native trust dialogs are answered."""
import errno
import fcntl
import os
import pty
import re
import selectors
import struct
import termios
import time

from core import Failure, require


ANSI = re.compile(r"\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07]*(?:\x07|\x1b\\))")
CURSOR = re.compile(r"^[ \t│┃]*([❯›])[ \t]*(.*?)[ \t│┃]*$", re.MULTILINE)
PLACEHOLDERS = {"claude": ("", 'try "write a test"'),
                "codex": ("", "implement {feature}", "write tests for @filename",
                          "find and fix a bug in @filename", "summarize recent commits")}


def composer_ready(screen, runtime):
    require(runtime in PLACEHOLDERS, "invalid_input")
    cursors = list(CURSOR.finditer(screen))
    if not cursors:
        return False
    cursor = cursors[-1]
    expected = "❯" if runtime == "claude" else "›"
    if cursor[1] != expected or cursor[2].strip().lower() not in PLACEHOLDERS[runtime]:
        return False
    footer = screen[cursor.end():].lower()
    profile = "bypass permissions on" if runtime == "claude" else "100% context left"
    return len(footer) <= 1000 and "? for shortcuts" in footer and profile in footer


def unknown_startup_dialog(screen, runtime):
    lower = screen.lower()
    if re.search(r"\b(?:sign[ -]in|log[ -]in|login|authenticate|authentication|oauth|"
                 r"trust|approval|choose|select|menu|dialog)\b|\b(?:enter|provide) (?:an? )?api key", lower):
        return True
    cursors = list(CURSOR.finditer(screen))
    return bool(cursors and cursors[-1][2].strip().lower() not in PLACEHOLDERS[runtime])


def approved_startup_dialog(screen, trusted_path):
    lower = screen.lower()
    cursors = list(CURSOR.finditer(screen))
    if not cursors:
        return None
    choice = re.sub(r"^[1-9][0-9]?[.)]\s*", "", cursors[-1][2].strip().lower())
    if "choose the text style" in lower and choice.startswith("dark mode"):
        return "theme"
    if trusted_path.lower() in lower and any(phrase in lower for phrase in (
            "do you trust the files in this folder", "do you trust the contents of this directory")) and \
            choice in ("yes, i trust this folder", "yes, continue", "yes, proceed", "trust directory"):
        return "trust-project"
    if "peerturn" in lower and re.search(r"(?:trust|enable|allow).{0,48}hooks", lower) and \
            choice in ("yes, enable hooks", "yes, trust hooks", "trust and enable", "enable hooks"):
        return "trust-plugin"
    if "bypass permissions" in lower and choice in ("yes, i accept", "accept"):
        return "bypass"
    return None


def selected_mcp_entry(screen, entry):
    require(entry in ("peerturn", "reconnect"), "invalid_input")
    pattern = r"(?:^|\n)\s*[❯›>]\s*(?:[1-9][0-9]?[.)]\s*)?" + entry + r"(?:\s|$)"
    return re.search(pattern, screen, flags=re.IGNORECASE) is not None


def connected_mcp_status(screen):
    return re.search(r"(?:^|\n)\s*(?:status:\s*[✓✔]?\s*connected\b|"
                     r"[✓✔]\s*(?:peerturn\s*[:·-]?\s*)?connected\b|"
                     r"peerturn\s*[:·-]\s*connected\b)", screen, re.IGNORECASE) is not None


class Terminal:
    def __init__(self, run, argv, environment, cwd):
        self.run, self.text, self.trust_observed = run, "", False
        self.master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 60, 180, 0, 0))
        self.proc = run.spawn(argv, environment, cwd, stdin=slave, stdout=slave, stderr=slave)
        os.close(slave)
        os.set_blocking(self.master, False)
        self.selector = selectors.DefaultSelector()
        self.selector.register(self.master, selectors.EVENT_READ)
        self.answered = set()

    def drain(self):
        self.run.check()
        for _key, _event in self.selector.select(timeout=0.05):
            try:
                chunk = os.read(self.master, 65536)
            except OSError as error:
                if error.errno == errno.EIO:
                    chunk = b""
                else:
                    raise
            if chunk:
                self.text += ANSI.sub("", chunk.decode("utf-8", "replace")).replace("\r", "")
                require(len(self.text) <= 16 * 1024 * 1024, "output_limit")
        require(self.proc.poll() is None, "process_failed")

    def write(self, data):
        self.drain()
        encoded = data.encode()
        require(len(encoded) <= 65536, "invalid_input")
        for offset in range(0, len(encoded), 512):
            os.write(self.master, encoded[offset:offset + 512])
            time.sleep(0.005)

    def paste(self, text):
        require("\x1b" not in text and "\x00" not in text, "invalid_input")
        self.write("\x1b[200~" + text + "\x1b[201~")
        time.sleep(0.2)
        self.write("\r")

    def contains(self, text, tail=12000):
        self.drain()
        return text in self.text[-tail:].lower()

    def screen_since(self, offset):
        self.drain()
        return self.text[offset:]

    def reconnect_claude_mcp(self):
        # docs/onboarding.md prescribes /mcp -> peerturn -> Reconnect after bind.
        # Match only fresh native menu output; previous prompt text proves nothing.
        start = len(self.text)
        self.paste("/mcp")
        self.run.wait(lambda: "mcp" in self.screen_since(start).lower() and
                      selected_mcp_entry(self.screen_since(start), "peerturn"), 20)
        details = len(self.text)
        self.write("\r")
        self.run.wait(lambda: "peerturn" in self.screen_since(details).lower() and
                      selected_mcp_entry(self.screen_since(details), "reconnect"), 20)
        reconnect = len(self.text)
        self.write("\r")
        self.run.wait(lambda: connected_mcp_status(self.screen_since(reconnect)), 30)
        # Dismiss server details and the server list without submitting model input.
        self.write("\x1b")
        time.sleep(0.1)
        self.write("\x1b")

    def startup(self, runtime, trusted_path):
        deadline = time.monotonic() + 90
        fresh = 0
        while time.monotonic() < deadline:
            self.drain()
            screen = self.text[fresh:][-14000:]
            # Only a recognized dialog with its selected affirmative choice may
            # receive Enter. A cursor inside an unknown menu is never a composer.
            dialog = approved_startup_dialog(screen, trusted_path)
            if dialog and dialog not in self.answered:
                self.write("\r")
                self.answered.add(dialog)
                self.trust_observed |= dialog == "trust-plugin"
                fresh = len(self.text)
                time.sleep(0.4)
                continue
            require(not unknown_startup_dialog(screen, runtime), "native_contract_changed")
            if composer_ready(screen, runtime):
                time.sleep(0.2)
                self.drain()
                confirmed = self.text[fresh:][-14000:]
                require(not unknown_startup_dialog(confirmed, runtime), "native_contract_changed")
                if composer_ready(confirmed, runtime):
                    return
            time.sleep(0.1)
        raise Failure("native_contract_changed")

    def close(self):
        self.selector.close()
        os.close(self.master)
