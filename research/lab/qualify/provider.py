"""Loopback gateway; bypass/full-access hosts can read its credential from the parent."""
import http.client
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import re
import threading
from urllib.parse import urlsplit

from core import Failure, require


def validate_base(value):
    require(isinstance(value, str) and 0 < len(value) <= 2048, "invalid_input")
    url = urlsplit(value)
    require(url.scheme == "https" and url.hostname and not url.username and not url.password and
            not url.query and not url.fragment and
            re.fullmatch(r"[A-Za-z0-9./_-]*", url.path), "invalid_input")
    require(url.port is None or 1 <= url.port <= 65535, "invalid_input")
    return value.rstrip("/")


def budget_error(status, data):
    if status in (402, 429):
        return True
    lower = data.lower()
    return any(marker in lower for marker in (b"budget has been exceeded", b"budget_exceeded",
               b"insufficient credits", b"no credits remaining", b"exceeded your current quota",
               b"insufficient_quota", b"maximum budget", b"spend limit"))


class Gateway:
    def __init__(self, base, key, max_requests=100):
        self.base = urlsplit(validate_base(base))
        require(isinstance(key, str) and 8 <= len(key) <= 4096 and
                not any(c in key for c in "\r\n\x00"), "invalid_input")
        require(type(max_requests) is int and 1 <= max_requests <= 200, "invalid_input")
        self.key, self.max_requests, self.requests = key, max_requests, 0
        self.failure, self.lock = None, threading.Lock()
        self.watches, self.connections = {}, set()
        gateway = self

        class Handler(BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.0"

            def setup(self):
                self.request.settimeout(100)
                super().setup()

            def log_message(self, *_args):
                pass

            def do_POST(self):
                upstream = None
                try:
                    require(self.client_address[0] == "127.0.0.1", "provider_unavailable")
                    require(self.path in ("/v1/messages", "/v1/messages?beta=true", "/v1/responses",
                                          "/v1/chat/completions"),
                            "provider_unavailable")
                    require(self.headers.get("Authorization") == "Bearer qualification-local" or
                            self.headers.get("x-api-key") == "qualification-local", "provider_unavailable")
                    length = int(self.headers.get("Content-Length", "0"))
                    require(0 < length <= 8 * 1024 * 1024, "output_limit")
                    with gateway.lock:
                        gateway.check()
                        gateway.requests += 1
                        require(gateway.requests <= gateway.max_requests, "budget_exhausted")
                    data = self.rfile.read(length)
                    require(len(data) == length, "provider_unavailable")
                    body = json.loads(data)
                    require(isinstance(body, dict), "provider_unavailable")
                    with gateway.lock:
                        for marker in gateway.watches:
                            gateway.watches[marker] += marker.encode() in data
                    suffix = self.path.removeprefix("/v1")
                    prefix = gateway.base.path.rstrip("/")
                    route = prefix + suffix if prefix.endswith("/v1") else prefix + "/v1" + suffix
                    headers = {"Content-Type": "application/json",
                               "Authorization": "Bearer " + gateway.key,
                               "x-api-key": gateway.key}
                    for name in ("anthropic-version", "anthropic-beta"):
                        if self.headers.get(name):
                            headers[name] = self.headers[name]
                    upstream = http.client.HTTPSConnection(gateway.base.hostname,
                                                          gateway.base.port or 443, timeout=90)
                    with gateway.lock:
                        gateway.connections.add(upstream)
                    upstream.request("POST", route, body=data, headers=headers)
                    response = upstream.getresponse()
                    # Redirects are errors. Credentials are sent only to the configured origin.
                    require(not 300 <= response.status < 400, "provider_unavailable")
                    if response.status != 200:
                        error = response.read(1024 * 1024)
                        raise Failure("budget_exhausted" if budget_error(response.status, error)
                                      else "provider_unavailable")
                    self.send_response(200)
                    self.send_header("Content-Type", response.getheader("Content-Type", "application/json"))
                    self.end_headers()
                    total, tail = 0, b""
                    while chunk := response.read1(8192):
                        gateway.check()
                        total += len(chunk)
                        require(total <= 16 * 1024 * 1024, "output_limit")
                        require(not budget_error(200, tail + chunk), "budget_exhausted")
                        tail = chunk[-256:]
                        self.wfile.write(chunk)
                        self.wfile.flush()
                except (Failure, OSError, ValueError, http.client.HTTPException) as error:
                    with gateway.lock:
                        gateway.failure = error.reason if isinstance(error, Failure) else "provider_unavailable"
                    try:
                        self.send_error(503, "qualification provider stopped")
                    except OSError:
                        pass
                finally:
                    if upstream:
                        upstream.close()
                        with gateway.lock:
                            gateway.connections.discard(upstream)

        class Server(ThreadingHTTPServer):
            def handle_error(self, _request, _client_address):
                gateway.failure = "provider_unavailable"

        self.server = Server(("127.0.0.1", 0), Handler)
        self.server.daemon_threads = True
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.url = "http://127.0.0.1:" + str(self.server.server_port)

    def check(self):
        if self.failure:
            raise Failure(self.failure)

    def watch(self, marker):
        require(re.fullmatch(r"QUAL_[A-Z0-9_]{1,64}", marker), "invalid_input")
        with self.lock:
            require(marker not in self.watches, "invalid_input")
            self.watches[marker] = 0

    def seen(self, marker):
        with self.lock:
            return self.watches[marker]

    def close(self):
        self.failure = self.failure or "interrupted"
        with self.lock:
            for connection in self.connections:
                connection.close()
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=3)
        self.key = ""
