#!/usr/bin/env python3
"""Installer fetch retry for scripts/validate-golangci-lint.sh."""

import http.server
import os
import subprocess
import tempfile
import threading
import unittest
import uuid
from pathlib import Path

SCRIPT = Path(__file__).resolve().with_name("validate-golangci-lint.sh")


def make_handler(failures, body):
    class Handler(http.server.BaseHTTPRequestHandler):
        failures_left = failures
        hits = 0
        payload = body

        def log_message(self, fmt, *args):
            del fmt, args

        def do_GET(self):
            Handler.hits += 1
            if Handler.failures_left > 0:
                Handler.failures_left -= 1
                self.send_response(503)
                self.send_header("Content-Length", "0")
                self.end_headers()
                return
            self.send_response(200)
            self.send_header("Content-Type", "text/plain")
            self.send_header("Content-Length", str(len(Handler.payload)))
            self.end_headers()
            self.wfile.write(Handler.payload)

    return Handler


class InstallerFetchRetryTest(unittest.TestCase):
    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory(
            prefix=f"golangci-lint-fetch-{os.getpid()}-{uuid.uuid4().hex}-"
        )
        self.addCleanup(self.tempdir.cleanup)

    def start_server(self, failures, body=b"installer-ok\n"):
        handler = make_handler(failures, body)
        server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        return server, handler

    def fetch(self, url, dest):
        env = os.environ.copy()
        env["FETCH_URL"] = url
        env["FETCH_DEST"] = dest
        for key in ("http_proxy", "https_proxy", "HTTP_PROXY", "HTTPS_PROXY"):
            env.pop(key, None)
        env["NO_PROXY"] = "*"
        env["no_proxy"] = "*"
        return subprocess.run(
            [
                "bash",
                "-c",
                'source "$1" && fetch_golangci_lint_installer "$FETCH_URL" "$FETCH_DEST"',
                "bash",
                str(SCRIPT),
            ],
            env=env,
            capture_output=True,
            text=True,
            check=False,
        )

    def test_non_200_then_200_writes_installer(self):
        body = b"#!/bin/sh\necho installed\n"
        server, handler = self.start_server(failures=1, body=body)
        dest = str(Path(self.tempdir.name) / "install.sh")
        url = f"http://127.0.0.1:{server.server_address[1]}/{uuid.uuid4().hex}"
        result = self.fetch(url, dest)
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertEqual(2, handler.hits)
        self.assertEqual(body, Path(dest).read_bytes())

    def test_exhausted_retries_name_installer_http_curl(self):
        server, handler = self.start_server(failures=3)
        dest = str(Path(self.tempdir.name) / "install.sh")
        url = f"http://127.0.0.1:{server.server_address[1]}/{uuid.uuid4().hex}"
        result = self.fetch(url, dest)
        self.assertNotEqual(0, result.returncode)
        self.assertEqual(3, handler.hits)
        self.assertIn("golangci-lint installer", result.stderr)
        self.assertIn("HTTP/curl", result.stderr)


if __name__ == "__main__":
    unittest.main()
