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
               malformed=None, saved_mismatch=None, preflight_mismatch=None,
               ports="80", extra=()):
        requests = []
        plan_revision = [1]
        plan_settings = {"backend": "masscan", "ports": "22", "provider": "vultr",
                         "workers": 1, "rate": 1000, "region": "atl",
                         "instance_type": "vc2-1c-1gb", "timeout": "24h",
                         "cleanup_timeout": "5m"}

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def do_GET(self):
                self.handle_request()

            def do_POST(self):
                self.handle_request()

            def do_PATCH(self):
                self.handle_request()

            def handle_request(self):
                body = self.rfile.read(int(self.headers.get("Content-Length", 0)))
                parsed_body = json.loads(body) if body else None
                requests.append((self.command, self.path, parsed_body,
                                 dict(self.headers)))
                if delay == self.path:
                    time.sleep(.1)
                status, response = 200, {"status": "ready"}
                if self.path == "/v1/targets":
                    status, response = 201, {"data": {"type": "targets", "id": "target-1",
                        "attributes": {"revision": 1, "entries": [{"input": "192.0.2.1"}]}}}
                elif self.path == "/v1/scan-plans" or self.path == "/v1/scan-plans/plan-1":
                    if self.command == "PATCH" and parsed_body.get("data", {}).get("id") != "plan-1":
                        status, response = 400, {"errors": [{"status": "400", "detail": "resource ID is required or mismatched"}]}
                    else:
                        status = 200 if self.command == "PATCH" else 201
                        if self.command == "PATCH":
                            plan_revision[0] = 2
                            plan_settings.clear()
                            plan_settings.update(parsed_body["data"]["attributes"]["settings"])
                        returned_settings = dict(plan_settings)
                        if saved_mismatch and self.command == "PATCH":
                            returned_settings.update(saved_mismatch)
                        response = {"data": {"type": "scan-plans", "id": "plan-1",
                            "attributes": {"target_id": "target-1", "revision": plan_revision[0],
                                           "settings": returned_settings}}}
                elif self.path.endswith("/preflight"):
                    addresses = 1000001 if large else 1
                    config = {"Targets": ["192.0.2.1/32"],
                              "RequestedTargets": [{"target": "192.0.2.1/32"}],
                              "Provider": plan_settings["provider"],
                              "Region": plan_settings["region"],
                              "InstanceType": plan_settings["instance_type"]}
                    if preflight_mismatch:
                        config.update(preflight_mismatch)
                    status, response = 201, {"data": {"type": "scan-preflights", "id": "preflight-1",
                        "attributes": {"target_id": "target-1", "target_revision": 1,
                        "scan_plan_id": "plan-1", "scan_plan_revision": plan_revision[0],
                        "expires_at": "2099-01-01T00:00:00Z",
                        "requires_confirmation": large,
                        "config": config,
                        "workload": {"Addresses": addresses, "Ports": 1, "Probes": addresses}}}}
                elif self.path.endswith("/start"):
                    status, response = 202, {"data": {"type": "runs", "id": "run-1",
                        "attributes": {"scan_plan_id": "plan-1", "target_id": "target-1"}}}
                elif self.path == "/v1/runs/run-1":
                    attributes = {"phase": "scanning", "summary": {}}
                    if async_error:
                        attributes.update(phase="failed", error="worker preparation failed",
                                          summary={"ScanOutcome": "failed", "CleanupOutcome": "complete"})
                    response = {"data": {"type": "runs", "id": "run-1", "attributes": attributes}}
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
                       f"http://127.0.0.1:{server.server_port}", "--wait", ".01",
                       "--poll-interval", ".01", "192.0.2.1", *extra]
            if ports is not None:
                command.extend(("--ports", ports))
            result = subprocess.run(command, input="", text=True, capture_output=True, timeout=5)
            return result, requests
        finally:
            server.shutdown()
            server.server_close()
            thread.join()

    def test_success_and_idempotency_diagnostic(self):
        result, calls = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual([c[1] for c in calls[:6]],
                         ["/healthz", "/v1/targets", "/v1/scan-plans", "/v1/scan-plans/plan-1",
                          "/v1/scan-plans/plan-1/preflight", "/v1/scan-plans/plan-1/start"])
        self.assertEqual(calls[3][2]["data"]["id"], "plan-1")
        self.assertEqual(calls[3][2]["data"]["attributes"]["settings"]["provider"], "vultr")
        start = calls[5]
        self.assertEqual(start[2], {"data": {"type": "runs", "attributes": {"preflight_id": "preflight-1", "confirm": False}}})
        key = next(v for k, v in start[3].items() if k.lower() == "idempotency-key")
        self.assertIn(key, result.stdout)
        self.assertIn("Run ID: run-1", result.stdout)
        self.assertIn("does not guarantee run completion", result.stdout)
        self.assertNotIn("Authorization", start[3])

    def test_digitalocean_provider_and_placement(self):
        result, calls = self.invoke(extra=("--provider", "digitalocean", "--region", "tor1",
                                           "--instance-type", "s-1vcpu-1gb"))
        self.assertEqual(result.returncode, 0, result.stderr)
        patch = calls[3][2]["data"]["attributes"]["settings"]
        self.assertEqual((patch["provider"], patch["region"], patch["instance_type"]),
                         ("digitalocean", "tor1", "s-1vcpu-1gb"))
        self.assertEqual(patch["timeout"], "24h")
        self.assertIn("Frozen provider/placement", result.stdout)
        self.assertEqual(calls[5][1], "/v1/scan-plans/plan-1/start")
        self.assertNotIn("Authorization", calls[5][3])

    def test_vultr_default_without_overrides(self):
        result, calls = self.invoke(ports=None)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(any(method == "PATCH" for method, *_ in calls))
        self.assertEqual(calls[3][1], "/v1/scan-plans/plan-1/preflight")
        self.assertEqual(calls[4][1], "/v1/scan-plans/plan-1/start")
        self.assertIn('"provider": "vultr"', result.stdout)

    def test_invalid_provider_or_placement_makes_no_request(self):
        for args in (("--provider", "digitalocean"),
                     ("--provider", "digitalocean", "--region", "tor1"),
                     ("--provider", "digitalocean", "--instance-type", "s-1vcpu-1gb"),
                     ("--provider", "digitalocean", "--region", "TOR1",
                      "--instance-type", "s-1vcpu-1gb"),
                     ("--provider", "other")):
            with self.subTest(args=args):
                result, calls = self.invoke(extra=args)
                self.assertEqual(result.returncode, 2)
                self.assertEqual(calls, [])

    def test_saved_or_frozen_provider_mismatch_never_starts(self):
        chosen = ("--provider", "digitalocean", "--region", "tor1",
                  "--instance-type", "s-1vcpu-1gb")
        for mismatch in ({"provider": "vultr"}, {"region": "atl"},
                         {"instance_type": "vc2-1c-1gb"}):
            with self.subTest(saved=mismatch):
                result, calls = self.invoke(saved_mismatch=mismatch, extra=chosen)
                self.assertEqual(result.returncode, 1)
                self.assertIn("saved ScanPlan provider/placement differs", result.stderr)
                self.assertEqual(len(calls), 4)
        for mismatch in ({"Provider": "vultr"}, {"Region": "atl"},
                         {"InstanceType": "vc2-1c-1gb"}, {"Provider": None}):
            with self.subTest(frozen=mismatch):
                result, calls = self.invoke(preflight_mismatch=mismatch, extra=chosen)
                self.assertEqual(result.returncode, 1)
                self.assertIn("frozen provider/placement differs", result.stderr)
                self.assertEqual(len(calls), 5)

    def test_digitalocean_start_failure_has_provider_hint(self):
        result, calls = self.invoke(fail=("/v1/scan-plans/plan-1/start", 503,
                                          {"error": "provider unavailable"}),
                                    extra=("--provider", "digitalocean", "--region", "tor1",
                                           "--instance-type", "s-1vcpu-1gb"))
        self.assertEqual(result.returncode, 1)
        self.assertEqual(len(calls), 6)
        self.assertIn("DIGITALOCEAN_API_TOKEN and DIGITALOCEAN_SSH_SEED", result.stderr)
        self.assertNotIn("VULTR_API_TOKEN", result.stderr)

    def test_each_http_failure_stops_sequence(self):
        cases = [("/healthz", 503, 1), ("/v1/targets", 400, 2),
                 ("/v1/scan-plans", 400, 3), ("/v1/scan-plans/plan-1", 400, 4),
                 ("/v1/scan-plans/plan-1/preflight", 422, 5),
                 ("/v1/scan-plans/plan-1/start", 409, 6),
                 ("/v1/scan-plans/plan-1/start", 503, 6)]
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
        self.assertTrue(calls[5][2]["data"]["attributes"]["confirm"])

    def test_preflight_only(self):
        r, calls = self.invoke(large=True, extra=("--preflight-only",))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(len(calls), 5)

    def test_async_failure(self):
        r, _ = self.invoke(async_error=True)
        self.assertEqual(r.returncode, 1)
        self.assertIn("worker preparation failed", r.stdout)
        self.assertIn("execution/cleanup problem", r.stderr)

    def test_malformed_response(self):
        r, calls = self.invoke(malformed="/v1/targets")
        self.assertEqual(r.returncode, 1)
        self.assertIn("invalid JSON", r.stderr)
        self.assertEqual(len(calls), 2)

    def test_start_timeout_does_not_retry(self):
        r, calls = self.invoke(delay="/v1/scan-plans/plan-1/start", extra=("--http-timeout", ".03"))
        self.assertEqual(r.returncode, 1)
        self.assertIn("unknown", r.stderr)
        self.assertIn("Retry ONLY this same start", r.stdout)
        self.assertEqual(sum(c[1].endswith("/start") for c in calls), 1)

    def test_missing_or_inconsistent_plan_fields(self):
        r, calls = self.invoke(fail=("/v1/scan-plans/plan-1/preflight", 201,
                                    {"data": {"type": "scan-preflights", "id": "preflight-1",
                                              "attributes": {"expires_at": "2099-01-01T00:00:00Z"}}}))
        self.assertEqual(r.returncode, 1)
        self.assertIn("revision does not match", r.stderr)
        self.assertEqual(len(calls), 5)

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

    def test_digitalocean_environment_secret_redaction(self):
        names = {"DIGITALOCEAN_API_TOKEN": "fixture-do-token",
                 "DIGITALOCEAN_SSH_SEED": "fixture-do-seed"}
        previous = {name: os.environ.get(name) for name in names}
        os.environ.update(names)
        try:
            result, calls = self.invoke(fail=("/healthz", 503,
                                              {"error": "fixture-do-token fixture-do-seed"}),
                                        extra=("--provider", "digitalocean", "--region", "tor1",
                                               "--instance-type", "s-1vcpu-1gb"))
            self.assertEqual(result.returncode, 1)
            self.assertEqual(len(calls), 1)
            for secret in names.values():
                self.assertNotIn(secret, result.stderr + result.stdout)
            self.assertIn("[REDACTED]", result.stderr)
        finally:
            for name, value in previous.items():
                if value is None:
                    del os.environ[name]
                else:
                    os.environ[name] = value


if __name__ == "__main__":
    unittest.main()
