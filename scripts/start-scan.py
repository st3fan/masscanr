#!/usr/bin/env python3
"""Create, preflight, and start an explicit scan through the V9 REST API."""

import argparse
import http.client
import json
import math
import os
import shlex
import sys
import time
import uuid
from urllib import error, parse, request

MAX_RESPONSE = 8 * 1024 * 1024


class Failure(Exception):
    pass


def safe(value):
    text = str(value)
    for name in ("VULTR_API_TOKEN", "PGPASSWORD", "MASSCANR_POSTGRES_PASSWORD",
                 "DBOS_SYSTEM_DATABASE_URL"):
        secret = os.environ.get(name)
        if secret:
            text = text.replace(secret, "[REDACTED]")
    # Prevent a remote error body from sending terminal control sequences.
    return "".join(c if c.isprintable() or c in "\n\t" else "?" for c in text)


def log(message):
    print(safe(message), flush=True)


class NoRedirect(request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class Client:
    def __init__(self, server, timeout):
        self.server = server.rstrip("/")
        self.timeout = timeout
        self.opener = request.build_opener(NoRedirect)

    def call(self, stage, method, path, expected, body=None, headers=None):
        url = self.server + path
        log(f"\n[{stage}] {method} {url}")
        data = None if body is None else json.dumps(body).encode()
        if body is not None:
            log("Request: " + json.dumps(body, sort_keys=True))
        req = request.Request(url, data=data, method=method,
                              headers={"Accept": "application/json",
                                       "Content-Type": "application/json", **(headers or {})})
        began = time.monotonic()
        try:
            response = self.opener.open(req, timeout=self.timeout)
        except error.HTTPError as exc:
            response = exc
        except (error.URLError, OSError, TimeoutError, http.client.HTTPException) as exc:
            raise Failure(f"{stage}: connection/timeout failure: {exc}. "
                          "The request outcome may be unknown; no automatic retry was made.") from exc
        with response:
            status = response.code
            try:
                raw = response.read(MAX_RESPONSE + 1)
            except (OSError, TimeoutError, http.client.HTTPException) as exc:
                raise Failure(f"{stage}: HTTP {status}, but reading the response failed: {exc}") from exc
        log(f"Response: HTTP {status} ({time.monotonic() - began:.2f}s)")
        if len(raw) > MAX_RESPONSE:
            raise Failure(f"{stage}: response exceeded {MAX_RESPONSE} bytes")
        text = raw.decode("utf-8", errors="replace")
        if status != expected:
            hints = {
                400: "Check the request fields and target/port syntax shown above.",
                404: "Check the server address, API version, and resource IDs.",
                409: "V9 reports a generic conflict: another run may be active, the plan may "
                     "be expired/stale, or confirmation/idempotency may not match.",
                422: "Target resolution failed; check the AS target and service/RIPE connectivity.",
                503: "Check service readiness and VULTR_API_TOKEN in the SERVICE environment. "
                     "A launch-pending response may already have reserved a run.",
            }
            raise Failure(f"{stage}: expected HTTP {expected}, got {status}.\n"
                          f"Server body: {text[:8192] or '(empty)'}{' [truncated]' if len(text)>8192 else ''}\n"
                          + hints.get(status, "Check service logs for this request."))
        try:
            obj = json.loads(text)
        except (ValueError, UnicodeError) as exc:
            raise Failure(f"{stage}: HTTP {status} returned invalid JSON: {text[:2048]}") from exc
        if not isinstance(obj, dict):
            raise Failure(f"{stage}: expected a JSON object, got {type(obj).__name__}")
        return obj


def identifier(obj, field, stage):
    value = obj.get(field)
    if not isinstance(value, str) or not value:
        raise Failure(f"{stage}: response is missing a nonempty {field!r}; keys: {list(obj)}")
    return value


def segment(value):
    return parse.quote(value, safe="")


def seconds(value):
    try:
        number = float(value)
    except ValueError as exc:
        raise argparse.ArgumentTypeError("expected seconds") from exc
    if not math.isfinite(number) or number <= 0:
        raise argparse.ArgumentTypeError("must be a finite positive number")
    return number


def arguments(argv=None):
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("targets", nargs="+", help="explicit authorized IPv4/CIDR/AS targets")
    p.add_argument("--server", default="http://127.0.0.1:8080")
    p.add_argument("--ports", help="TCP ports/ranges; omitted uses service defaults")
    p.add_argument("--backend", choices=("masscan", "zmap"))
    p.add_argument("--shards", type=int)
    p.add_argument("--rate", type=int, help="packets/sec per worker")
    p.add_argument("--region")
    p.add_argument("--instance-type")
    p.add_argument("--scan-timeout", help="Go duration, e.g. 1h")
    p.add_argument("--cleanup-timeout", help="Go duration, e.g. 5m")
    p.add_argument("--http-timeout", type=seconds, default=60)
    p.add_argument("--wait", type=seconds, default=30,
                   help="seconds to observe the run for asynchronous errors (default 30)")
    p.add_argument("--poll-interval", type=seconds, default=2)
    p.add_argument("--yes", action="store_true", help="explicitly approve the returned large-scan plan")
    p.add_argument("--preflight-only", action="store_true", help="create/inspect plan without starting")
    a = p.parse_args(argv)
    try:
        url = parse.urlsplit(a.server)
        valid = (url.scheme in ("http", "https") and url.hostname and not url.username
                 and not url.password and not url.query and not url.fragment)
        _ = url.port
    except ValueError:
        valid = False
    if not valid:
        p.error("--server must be an HTTP(S) URL without credentials, query, or fragment")
    if any(v is not None and v <= 0 for v in (a.shards, a.rate)):
        p.error("--shards and --rate must be positive")
    return a


def run(a):
    client = Client(a.server, a.http_timeout)
    health = client.call("health", "GET", "/healthz", 200)
    if health.get("status") != "ready":
        raise Failure(f"health: service did not report ready: {health}")
    definition = {"targets": a.targets}
    for field in ("ports", "backend", "shards", "rate", "region", "instance_type", "cleanup_timeout"):
        if getattr(a, field) is not None:
            definition[field] = getattr(a, field)
    if a.scan_timeout:
        definition["timeout"] = a.scan_timeout
    created = client.call("create", "POST", "/v1/scans", 201, definition)
    scan_id = identifier(created, "id", "create")
    if type(created.get("revision")) is not int or created["revision"] < 1:
        raise Failure("create: missing/invalid definition revision")
    path = "/v1/scans/" + segment(scan_id)
    log(f"Scan ID: {scan_id}\nSaved definition: {json.dumps(created.get('definition'), sort_keys=True)}")
    plan = client.call("preflight", "POST", path + "/preflight", 201)
    plan_id = identifier(plan, "id", "preflight")
    identifier(plan, "expires_at", "preflight")
    if plan.get("scan_id") != scan_id or plan.get("revision") != created.get("revision"):
        raise Failure("preflight: scan ID or definition revision does not match the created scan")
    work = plan.get("workload")
    cfg = plan.get("config")
    if (not isinstance(work, dict) or not isinstance(cfg, dict)
            or not isinstance(cfg.get("Targets"), list) or not cfg["Targets"]
            or type(plan.get("requires_confirmation")) is not bool
            or any(type(work.get(k)) is not int or work[k] <= 0
                   for k in ("Addresses", "Ports", "Probes"))):
        raise Failure("preflight: missing/invalid frozen scope, workload, or confirmation fields")
    if work["Probes"] != work["Addresses"] * work["Ports"]:
        raise Failure("preflight: inconsistent probe count")
    if plan["requires_confirmation"] != (work["Probes"] > 1_000_000):
        raise Failure("preflight: confirmation flag disagrees with V9 probe threshold")
    log(f"Plan ID: {plan_id}\nExpires: {plan.get('expires_at', '(missing)')}\n"
        f"Planned probes: {work['Probes']:,} = {work['Addresses']:,} addresses x {work['Ports']:,} ports")
    log("Frozen targets/provenance: " + json.dumps(cfg.get("RequestedTargets"), indent=2))
    log("Effective scope: " + json.dumps(cfg["Targets"]))
    if a.preflight_only:
        log("Preflight only: definition and plan saved; no start requested.")
        return 0
    if plan["requires_confirmation"] and not a.yes:
        if not sys.stdin.isatty():
            raise Failure("Large scan requires approval. Review the frozen scope, then rerun with --yes "
                          "or use an interactive terminal. No start requested.")
        if input("Start this exact plan? Type yes to approve: ").strip().lower() != "yes":
            raise Failure("Start declined; definition retained and no start requested.")
    key = str(uuid.uuid4())
    body = {"plan_id": plan_id, "confirm": plan["requires_confirmation"]}
    log(f"Idempotency-Key: {key}")
    log("If the start response is lost, inspect history at " + client.server + path + "/runs")
    log("Retry ONLY this same start request/key (do not rerun create blindly):\n" + shlex.join([
        "curl", "--fail-with-body", "-sS", "-X", "POST", client.server + path + "/start",
        "-H", "Content-Type: application/json", "-H", "Idempotency-Key: " + key,
        "-d", json.dumps(body)]))
    started = client.call("start", "POST", path + "/start", 202, body, {"Idempotency-Key": key})
    run_id = identifier(started, "id", "start")
    if started.get("scan_id") != scan_id:
        raise Failure("start: returned run belongs to a different scan")
    run_path = "/v1/runs/" + segment(run_id)
    log(f"Run ID: {run_id}\nAccepted asynchronously; HTTP 202 does not prove the scanner started.\n"
        f"Status URL: {client.server}{run_path}")
    deadline = time.monotonic() + a.wait
    while True:
        current = client.call("status", "GET", run_path, 200)
        if current.get("id") != run_id:
            raise Failure("status: returned a different run")
        identifier(current, "phase", "status")
        summary = current.get("summary")
        if not isinstance(summary, dict):
            raise Failure("status: invalid summary")
        log("Run status: " + json.dumps({k: current.get(k) for k in
                                       ("phase", "summary", "error", "workers", "artifact")}, indent=2))
        if (current.get("error") or summary.get("ScanOutcome") in ("failed", "interrupted", "cancelled")
                or summary.get("CleanupOutcome") == "incomplete"
                or current.get("phase") in ("failed", "interrupted", "cancelled", "cleanup_incomplete")):
            raise Failure("Run reports an execution/cleanup problem; see status above. "
                          "The run may still be cleaning up—keep inspecting its status URL.")
        # V9 has no explicit settled flag, so do not infer settlement from phase alone.
        if time.monotonic() >= deadline:
            log("Observation window ended without a reported error. This does not guarantee run "
                "completion; continue checking the status URL. Exiting does not stop the scan.")
            return 0
        time.sleep(min(a.poll_interval, max(0, deadline - time.monotonic())))


def main(argv=None):
    a = arguments(argv)
    try:
        return run(a)
    except Failure as exc:
        print("\nERROR: " + safe(exc), file=sys.stderr, flush=True)
        print("For server-side detail: docker compose logs --tail=100 masscanr postgres\n"
              "No definitions/runs were deleted and no stop request was sent.", file=sys.stderr)
        return 1
    except (KeyboardInterrupt, EOFError):
        print("\nInterrupted. Any accepted scan continues; inspect the printed IDs/status URL.",
              file=sys.stderr)
        return 130


if __name__ == "__main__":
    sys.exit(main())
