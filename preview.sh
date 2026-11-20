#!/bin/sh
# Submit one fetchable commit to Preview Controller.
set -eu
cd "$(CDPATH= cd "$(dirname "$0")" && pwd)"
exec python3 - "$@" <<'PY'
import json
import os
import re
import sys
import time
from pathlib import Path
from urllib.error import HTTPError, URLError
from urllib.parse import quote, urlsplit
from urllib.request import HTTPRedirectHandler, ProxyHandler, Request, build_opener


KEYS = (
    "MASSCANR_PREVIEW_CONTROLLER_URL",
    "MASSCANR_PREVIEW_PROJECT",
    "MASSCANR_PREVIEW_CONTROLLER_TOKEN",
)


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, *_args):
        return None


OPENER = build_opener(ProxyHandler({}), NoRedirect())


def fail(message, code=1):
    print(f"preview.sh: {message}", file=sys.stderr)
    raise SystemExit(code)


def usage():
    print("Usage: ./preview.sh [--wait] INTENT COMMIT_SHA", file=sys.stderr)
    raise SystemExit(2)


def settings():
    values = {}
    path = Path(".env")
    if path.is_file():
        for line in path.read_text(encoding="utf-8").splitlines():
            line = line.strip()
            if line.startswith("export "):
                line = line[7:].lstrip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            key, value = line.split("=", 1)
            key = key.strip()
            if key not in KEYS:
                continue
            value = value.strip()
            if len(value) >= 2 and value[0] == value[-1] and value[0] in "'\"":
                value = value[1:-1]
            values[key] = value
    return {key: os.environ.get(key, values.get(key, "")) for key in KEYS}


def read_json(response):
    content = response.read(65537)
    if len(content) > 65536:
        fail("controller response is too large")
    try:
        result = json.loads(content)
    except (ValueError, UnicodeDecodeError):
        fail("controller returned invalid JSON")
    if not isinstance(result, dict):
        fail("controller returned an invalid response")
    return result


def request(url, token="", payload=None):
    headers = {}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    data = None
    if payload is not None:
        headers["Content-Type"] = "application/json"
        data = json.dumps(payload, separators=(",", ":")).encode("utf-8")
    req = Request(url, data=data, headers=headers, method="POST" if data else "GET")
    try:
        with OPENER.open(req, timeout=5 if payload is None else 15) as response:
            return response.status, read_json(response)
    except HTTPError as error:
        try:
            body = read_json(error)
            message = str(body.get("error", "request rejected"))
        except SystemExit:
            message = "request rejected"
        return error.code, {"error": message}
    except (URLError, TimeoutError, OSError):
        fail("cannot reach Preview Controller; connect to Tailscale or the home network and check the controller")


def main():
    args = sys.argv[1:]
    wait = False
    if args and args[0] == "--wait":
        wait = True
        args = args[1:]
    if len(args) != 2:
        usage()
    intent_text, commit = args
    if not re.fullmatch(r"[1-9][0-9]*", intent_text) or int(intent_text) > 9223372036854775807:
        fail("Intent must be a positive decimal integer", 2)
    if not re.fullmatch(r"[0-9a-fA-F]{40}", commit):
        fail("commit must be a full 40-character hexadecimal SHA", 2)

    config = settings()
    base = config["MASSCANR_PREVIEW_CONTROLLER_URL"].rstrip("/")
    project = config["MASSCANR_PREVIEW_PROJECT"]
    token = config["MASSCANR_PREVIEW_CONTROLLER_TOKEN"]
    try:
        url = urlsplit(base)
        port = url.port
    except ValueError:
        fail("set MASSCANR_PREVIEW_CONTROLLER_URL to an http(s) management origin", 2)
    if (
        url.scheme not in ("http", "https")
        or not url.hostname
        or url.username
        or url.password
        or url.path
        or url.query
        or url.fragment
        or port == 0
    ):
        fail("set MASSCANR_PREVIEW_CONTROLLER_URL to an http(s) management origin", 2)
    if not re.fullmatch(r"[a-z0-9](?:[a-z0-9-]*[a-z0-9])?", project):
        fail("set MASSCANR_PREVIEW_PROJECT to the configured project key", 2)
    if not token or any(char in token for char in "\r\n"):
        fail("set MASSCANR_PREVIEW_CONTROLLER_TOKEN in the environment or local .env", 2)
    if wait:
        timeout_text = os.environ.get("MASSCANR_PREVIEW_WAIT_SECONDS", "600")
        if not timeout_text.isdecimal() or not 1 <= int(timeout_text) <= 3600:
            fail("MASSCANR_PREVIEW_WAIT_SECONDS must be 1–3600", 2)

    try:
        with OPENER.open(Request(base + "/healthz", method="GET"), timeout=5) as response:
            health_status = response.status
    except HTTPError as error:
        health_status = error.code
    except (URLError, TimeoutError, OSError):
        fail("cannot reach Preview Controller; connect to Tailscale or the home network before submitting")
    if health_status not in (200, 204):
        fail(f"controller health check returned HTTP {health_status}; check Tailscale/home network and controller health")

    status, result = request(
        base + "/api/deployments",
        token,
        {"project": project, "intent": int(intent_text), "commit": commit},
    )
    if status != 202:
        message = str(result.get("error", "request rejected")).replace(token, "[redacted]")
        fail(f"deployment request returned HTTP {status}: {message}")
    deployment_id = result.get("id")
    preview_url = result.get("url")
    if result.get("status") != "queued" or not isinstance(deployment_id, str) or not deployment_id or not isinstance(preview_url, str) or not preview_url:
        fail("controller accepted the request but returned an invalid status, id, or preview url")
    try:
        preview = urlsplit(preview_url)
        preview_port = preview.port
    except ValueError:
        fail("controller accepted the request but returned an invalid preview url")
    if (
        preview.scheme != "https"
        or not preview.hostname
        or preview.username
        or preview.password
        or preview.path
        or preview.query
        or preview.fragment
        or preview_port == 0
        or token in deployment_id
        or token in preview_url
    ):
        fail("controller accepted the request but returned an invalid deployment id or preview url")
    print(f"Deployment: {deployment_id}")
    print(f"Preview: {preview_url}")
    print("Status: queued", flush=True)
    if not wait:
        return

    deadline = time.monotonic() + int(timeout_text)
    deployment_path = quote(deployment_id, safe="")
    environment_path = f"{quote(project, safe='')}/{intent_text}"
    while time.monotonic() < deadline:
        status, attempt = request(base + "/api/deployments/" + deployment_path, token)
        if status != 200:
            message = str(attempt.get("error", "request rejected")).replace(token, "[redacted]")
            fail(f"deployment status returned HTTP {status}: {message}")
        state = attempt.get("status")
        if state == "failed":
            message = str(attempt.get("error") or "no reason supplied").replace(token, "[redacted]")
            fail(f"deployment {deployment_id} failed: {message}")
        if state == "retired":
            fail(f"deployment {deployment_id} was retired before becoming active")
        if state == "healthy":
            status, environment = request(base + "/api/environments/" + environment_path, token)
            if status != 200:
                message = str(environment.get("error", "request rejected")).replace(token, "[redacted]")
                fail(f"environment status returned HTTP {status}: {message}")
            if environment.get("active_deployment_id") == deployment_id:
                print(f"Ready: {preview_url}")
                return
        time.sleep(min(2, max(0, deadline - time.monotonic())))
    fail(f"timed out waiting for deployment {deployment_id} to become active")


if __name__ == "__main__":
    main()
PY
