#!/usr/bin/env python3
"""Exercise preview.sh against a local HTTP stub; no deployment is submitted."""

import json
import os
import shutil
import subprocess
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path


SHA = "a" * 40
TOKEN = "stub-secret"
SCRIPT = Path(__file__).resolve().parents[1] / "preview.sh"


class Controller(BaseHTTPRequestHandler):
    def log_message(self, *_args):
        pass

    def reply(self, status, value=None):
        data = json.dumps(value).encode() if value is not None else b""
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        state = self.server.state
        state["gets"].append(self.path)
        if self.path == "/healthz":
            self.reply(state["health"])
        elif self.path == "/api/deployments/deployment-1":
            statuses = state["statuses"]
            status = statuses.pop(0) if len(statuses) > 1 else statuses[0]
            self.reply(200, {"id": "deployment-1", "status": status, "error": state["attempt_error"]})
        elif self.path == "/api/environments/masscanr/229":
            self.reply(200, {"active_deployment_id": state["active_id"]})
        else:
            self.reply(404, {"error": "not found"})

    def do_POST(self):
        state = self.server.state
        length = int(self.headers.get("Content-Length", "0"))
        state["posts"].append(
            (self.path, dict(self.headers), self.rfile.read(length))
        )
        self.reply(state["post_status"], state["post_body"])


class PreviewScriptTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.script = Path(self.temp.name) / "preview.sh"
        shutil.copy2(SCRIPT, self.script)
        self.script.chmod(0o700)
        self.state = {
            "gets": [],
            "posts": [],
            "health": 204,
            "post_status": 202,
            "post_body": {
                "id": "deployment-1",
                "status": "queued",
                "url": "https://masscanr-229.preview.internal.example",
            },
            "statuses": ["healthy"],
            "attempt_error": "",
            "active_id": "deployment-1",
        }
        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Controller)
        self.server.state = self.state
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.addCleanup(self.server.server_close)
        self.addCleanup(self.server.shutdown)
        self.base = f"http://127.0.0.1:{self.server.server_port}"

    def run_script(self, *args, env=None):
        settings = {"PATH": os.environ["PATH"], "LC_ALL": "C"}
        settings.update(env or {})
        return subprocess.run(
            [str(self.script), *args],
            env=settings,
            text=True,
            capture_output=True,
            timeout=8,
            check=False,
        )

    def config(self):
        return {
            "MASSCANR_PREVIEW_CONTROLLER_URL": self.base,
            "MASSCANR_PREVIEW_PROJECT": "masscanr",
            "MASSCANR_PREVIEW_CONTROLLER_TOKEN": TOKEN,
        }

    def test_validation_precedes_network(self):
        for args in (("0", SHA), ("01", SHA), ("229", "short"), ("229", SHA[:-1] + "z")):
            with self.subTest(args=args):
                result = self.run_script(*args, env=self.config())
                self.assertEqual(result.returncode, 2)
        self.assertEqual(self.state["gets"], [])
        self.assertEqual(self.state["posts"], [])
        result = self.run_script("229", SHA, env={"MASSCANR_PREVIEW_CONTROLLER_URL": self.base})
        self.assertEqual(result.returncode, 2)
        self.assertEqual(self.state["gets"], [])

    def test_dotenv_and_process_override(self):
        (Path(self.temp.name) / ".env").write_text(
            f"MASSCANR_PREVIEW_CONTROLLER_URL={self.base}\n"
            "MASSCANR_PREVIEW_PROJECT=wrong\n"
            f"MASSCANR_PREVIEW_CONTROLLER_TOKEN={TOKEN}\n"
        )
        result = self.run_script("229", SHA, env={"MASSCANR_PREVIEW_PROJECT": "masscanr"})
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Deployment: deployment-1", result.stdout)
        self.assertIn("Preview: https://masscanr-229.preview.internal.example", result.stdout)
        self.assertIn("Status: queued", result.stdout)
        self.assertEqual(self.state["gets"], ["/healthz"])
        self.assertEqual(len(self.state["posts"]), 1)
        path, headers, payload = self.state["posts"][0]
        self.assertEqual(path, "/api/deployments")
        self.assertEqual(headers["Authorization"], "Bearer " + TOKEN)
        self.assertEqual(headers["Content-Type"], "application/json")
        self.assertEqual(json.loads(payload), {"project": "masscanr", "intent": 229, "commit": SHA})
        self.assertNotIn(TOKEN, result.stdout + result.stderr)

    def test_unreachable_health_prevents_submission(self):
        self.state["health"] = 503
        result = self.run_script("229", SHA, env=self.config())
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Tailscale/home network", result.stderr)
        self.assertEqual(self.state["posts"], [])
        result = self.run_script(
            "229", SHA,
            env={**self.config(), "MASSCANR_PREVIEW_CONTROLLER_URL": "http://127.0.0.1:1"},
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("connect to Tailscale or the home network", result.stderr)
        self.assertEqual(self.state["posts"], [])

    def test_api_error_and_token_redaction(self):
        self.state["post_status"] = 409
        self.state["post_body"] = {"error": "busy " + TOKEN}
        result = self.run_script("229", SHA, env=self.config())
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("HTTP 409", result.stderr)
        self.assertIn("[redacted]", result.stderr)
        self.assertNotIn(TOKEN, result.stderr)

    def test_wait_checks_active_environment(self):
        self.state["statuses"] = ["building", "healthy"]
        self.state["active_id"] = "deployment-1"
        result = self.run_script("--wait", "229", SHA, env=self.config())
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Ready: https://masscanr-229.preview.internal.example", result.stdout)
        self.assertIn("/api/environments/masscanr/229", self.state["gets"])

    def test_wait_failed_and_retired(self):
        for status in ("failed", "retired"):
            with self.subTest(status=status):
                self.state["statuses"] = [status]
                self.state["attempt_error"] = "build failed"
                result = self.run_script("--wait", "229", SHA, env=self.config())
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(status, result.stderr)
                self.assertNotIn("Ready:", result.stdout)

    def test_wait_timeout_when_other_deployment_is_active(self):
        self.state["active_id"] = "deployment-previous"
        result = self.run_script(
            "--wait", "229", SHA,
            env={**self.config(), "MASSCANR_PREVIEW_WAIT_SECONDS": "1"},
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("timed out", result.stderr)
        self.assertNotIn("Ready:", result.stdout)


if __name__ == "__main__":
    unittest.main()
