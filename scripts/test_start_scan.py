"""Offline contract tests: no masscanr service or cloud calls."""
import json
import os
from pathlib import Path
import subprocess
import sys
import threading
import time
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

SCRIPT = Path(__file__).with_name("start-scan.py")


class StartScanTests(unittest.TestCase):
    def invoke(self, *, fail=None, large=False, async_error=False, delay=None,
               malformed=None, extra=()):
        requests = []

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def do_GET(self):
                self.handle_request()

            def do_POST(self):
                self.handle_request()

            def handle_request(self):
                body = self.rfile.read(int(self.headers.get("Content-Length", 0)))
                requests.append((self.command, self.path, json.loads(body) if body else None,
                                 dict(self.headers)))
                if delay == self.path:
                    time.sleep(.1)
                status, response = 200, {"status": "ready"}
                if self.path == "/v1/scans":
                    status, response = 201, {"id": "scan-1", "revision": 1,
                                              "definition": {"targets": ["192.0.2.1"]}}
                elif self.path.endswith("/preflight"):
                    addresses = 1000001 if large else 1
                    status, response = 201, {
                        "id": "plan-1", "scan_id": "scan-1", "revision": 1,
                        "expires_at": "2099-01-01T00:00:00Z",
                        "requires_confirmation": large,
                        "config": {"Targets": ["192.0.2.1/32"],
                                   "RequestedTargets": [{"target": "192.0.2.1/32"}]},
                        "workload": {"Addresses": addresses, "Ports": 1, "Probes": addresses}}
                elif self.path.endswith("/start"):
                    status, response = 202, {"id": "run-1", "scan_id": "scan-1"}
                elif self.path == "/v1/runs/run-1":
                    response = {"id": "run-1", "phase": "scanning", "summary": {}}
                    if async_error:
                        response.update(phase="failed", error="worker preparation failed",
                                        summary={"ScanOutcome": "failed", "CleanupOutcome": "complete"})
                if fail and fail[0] == self.path:
                    status, response = fail[1:]
                raw = json.dumps(response).encode()
                if malformed == self.path:
                    raw = b"<html>not JSON</html>"
                self.send_response(status)
                self.send_header("Content-Length", str(len(raw)))
                self.end_headers()
                try:
                    self.wfile.write(raw)
                except (BrokenPipeError, ConnectionResetError):
                    pass

        server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            command = [sys.executable, str(SCRIPT), "--server",
                       f"http://127.0.0.1:{server.server_port}", "--ports", "80",
                       "--wait", ".01", "--poll-interval", ".01", "192.0.2.1", *extra]
            result = subprocess.run(command, input="", text=True, capture_output=True, timeout=5)
            return result, requests
        finally:
            server.shutdown()
            server.server_close()
            thread.join()

    def test_success_and_idempotency_diagnostic(self):
        result, calls = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual([c[1] for c in calls[:4]],
                         ["/healthz", "/v1/scans", "/v1/scans/scan-1/preflight", "/v1/scans/scan-1/start"])
        start = calls[3]
        self.assertEqual(start[2], {"plan_id": "plan-1", "confirm": False})
        key = next(v for k, v in start[3].items() if k.lower() == "idempotency-key")
        self.assertIn(key, result.stdout)
        self.assertIn("Run ID: run-1", result.stdout)
        self.assertIn("does not guarantee run completion", result.stdout)
        self.assertNotIn("Authorization", start[3])

    def test_each_http_failure_stops_sequence(self):
        cases = [("/healthz", 503, 1), ("/v1/scans", 400, 2),
                 ("/v1/scans/scan-1/preflight", 422, 3),
                 ("/v1/scans/scan-1/start", 409, 4),
                 ("/v1/scans/scan-1/start", 503, 4)]
        for path, code, count in cases:
            with self.subTest(path=path, code=code):
                r, calls = self.invoke(fail=(path, code, {"error": "fixture failure"}))
                self.assertEqual(r.returncode, 1)
                self.assertIn(f"got {code}", r.stderr)
                self.assertIn("fixture failure", r.stderr)
                self.assertEqual(len(calls), count)

    def test_large_scan_needs_explicit_confirmation(self):
        r, calls = self.invoke(large=True)
        self.assertEqual(r.returncode, 1)
        self.assertIn("requires approval", r.stderr)
        self.assertFalse(any(c[1].endswith("/start") for c in calls))
        r, calls = self.invoke(large=True, extra=("--yes",))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertTrue(calls[3][2]["confirm"])

    def test_preflight_only(self):
        r, calls = self.invoke(large=True, extra=("--preflight-only",))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(len(calls), 3)

    def test_async_failure(self):
        r, _ = self.invoke(async_error=True)
        self.assertEqual(r.returncode, 1)
        self.assertIn("worker preparation failed", r.stdout)
        self.assertIn("execution/cleanup problem", r.stderr)

    def test_malformed_response(self):
        r, calls = self.invoke(malformed="/v1/scans")
        self.assertEqual(r.returncode, 1)
        self.assertIn("invalid JSON", r.stderr)
        self.assertEqual(len(calls), 2)

    def test_start_timeout_does_not_retry(self):
        r, calls = self.invoke(delay="/v1/scans/scan-1/start", extra=("--http-timeout", ".03"))
        self.assertEqual(r.returncode, 1)
        self.assertIn("unknown", r.stderr)
        self.assertIn("Retry ONLY this same start", r.stdout)
        self.assertEqual(sum(c[1].endswith("/start") for c in calls), 1)

    def test_missing_or_inconsistent_plan_fields(self):
        r, calls = self.invoke(fail=("/v1/scans/scan-1/preflight", 201,
                                    {"id": "plan-1", "scan_id": "scan-1", "revision": 1,
                                     "expires_at": "2099-01-01T00:00:00Z"}))
        self.assertEqual(r.returncode, 1)
        self.assertIn("missing/invalid", r.stderr)
        self.assertEqual(len(calls), 3)

    def test_known_environment_secret_redaction(self):
        old = os.environ.get("VULTR_API_TOKEN")
        os.environ["VULTR_API_TOKEN"] = "fixture-sensitive-token"
        try:
            r, _ = self.invoke(fail=("/healthz", 503, {"error": "fixture-sensitive-token"}))
            self.assertNotIn("fixture-sensitive-token", r.stderr + r.stdout)
            self.assertIn("[REDACTED]", r.stderr)
        finally:
            if old is None:
                del os.environ["VULTR_API_TOKEN"]
            else:
                os.environ["VULTR_API_TOKEN"] = old


if __name__ == "__main__":
    unittest.main()
