#!/usr/bin/env python3
"""Create a Target and ScanPlan, then preflight and start through JSON:API."""

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

    def call(self, stage, method, path, expected, body=None, headers=None, resource_id=None):
        url = self.server + path
        log(f"\n[{stage}] {method} {url}")
        if body is not None and path.startswith("/v1/"):
            kind = "runs" if path.endswith("/start") else ("targets" if path == "/v1/targets" else "scan-plans")
            resource = {"type": kind, "attributes": body}
            if method == "PATCH":
                if not resource_id:
                    raise Failure(f"{stage}: PATCH resource ID is required")
                resource["id"] = resource_id
            body = {"data": resource}
        data = None if body is None else json.dumps(body).encode()
        if body is not None:
            log("Request: " + json.dumps(body, sort_keys=True))
        req = request.Request(url, data=data, method=method,
                              headers={"Accept": "application/vnd.api+json",
                                       "Content-Type": "application/vnd.api+json", **(headers or {})})
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
                409: "Another run may be active, the preflight may be expired/stale, "
                     "or confirmation/idempotency may not match.",
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
        if path.startswith("/v1/"):
            data = obj.get("data")
            if not isinstance(data, dict) or not isinstance(data.get("attributes"), dict):
                raise Failure(f"{stage}: missing JSON:API resource")
            return {"id": data.get("id"), **data["attributes"]}
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
    p.add_argument("--server", default="http://127.0.0.1:18080")
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
    target = client.call("create target", "POST", "/v1/targets", 201,
                         {"name": "CLI target", "entries": a.targets})
    target_id = identifier(target, "id", "create target")
    created = client.call("create scan plan", "POST", "/v1/scan-plans", 201,
                          {"name": "CLI scan plan", "target_id": target_id})
    scan_plan_id = identifier(created, "id", "create scan plan")
    settings = created.get("settings")
    if not isinstance(settings, dict):
        raise Failure("create scan plan: missing settings")
    changed = False
    for field in ("ports", "backend", "shards", "rate", "region", "instance_type", "cleanup_timeout"):
        if getattr(a, field) is not None:
            settings[field] = getattr(a, field)
            changed = True
    if a.scan_timeout:
        settings["timeout"] = a.scan_timeout
        changed = True
    path = "/v1/scan-plans/" + segment(scan_plan_id)
    if changed:
        created = client.call("update scan plan", "PATCH", path, 200,
                              {"revision": created.get("revision"), "settings": settings},
                              resource_id=scan_plan_id)
    log(f"Target ID: {target_id}\nScanPlan ID: {scan_plan_id}\nSaved settings: {json.dumps(created.get('settings'), sort_keys=True)}")
    plan = client.call("preflight", "POST", path + "/preflight", 201)
    plan_id = identifier(plan, "id", "preflight")
    identifier(plan, "expires_at", "preflight")
    if (plan.get("scan_plan_id") != scan_plan_id or plan.get("scan_plan_revision") != created.get("revision")
            or plan.get("target_id") != target_id or plan.get("target_revision") != target.get("revision")):
        raise Failure("preflight: Target or ScanPlan revision does not match")
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
        raise Failure("preflight: confirmation flag disagrees with probe threshold")
    log(f"Preflight ID: {plan_id}\nExpires: {plan.get('expires_at', '(missing)')}\n"
        f"Planned probes: {work['Probes']:,} = {work['Addresses']:,} addresses x {work['Ports']:,} ports")
    log("Frozen targets/provenance: " + json.dumps(cfg.get("RequestedTargets"), indent=2))
    log("Effective scope: " + json.dumps(cfg["Targets"]))
    if a.preflight_only:
        log("Preflight only: Target and ScanPlan saved; no start requested.")
        return 0
    if plan["requires_confirmation"] and not a.yes:
        if not sys.stdin.isatty():
            raise Failure("Large scan requires approval. Review the frozen scope, then rerun with --yes "
                          "or use an interactive terminal. No start requested.")
        if input("Start this exact plan? Type yes to approve: ").strip().lower() != "yes":
            raise Failure("Start declined; Target and ScanPlan retained and no start requested.")
    key = str(uuid.uuid4())
    body = {"preflight_id": plan_id, "confirm": plan["requires_confirmation"]}
    log(f"Idempotency-Key: {key}")
    log("If the start response is lost, inspect history at " + client.server + path + "/runs")
    log("Retry ONLY this same start request/key (do not rerun create blindly):\n" + shlex.join([
        "curl", "--fail-with-body", "-sS", "-X", "POST", client.server + path + "/start",
        "-H", "Content-Type: application/vnd.api+json", "-H", "Idempotency-Key: " + key,
        "-d", json.dumps({"data": {"type": "runs", "attributes": body}})]))
    started = client.call("start", "POST", path + "/start", 202, body, {"Idempotency-Key": key})
    run_id = identifier(started, "id", "start")
    if started.get("scan_plan_id") != scan_plan_id or started.get("target_id") != target_id:
        raise Failure("start: returned run belongs to a different Target or ScanPlan")
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
        # An accepted run can still be active during cleanup.
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
              "No Targets, ScanPlans, or runs were deleted and no stop request was sent.", file=sys.stderr)
        return 1
    except (KeyboardInterrupt, EOFError):
        print("\nInterrupted. Any accepted scan continues; inspect the printed IDs/status URL.",
              file=sys.stderr)
        return 130


if __name__ == "__main__":
    sys.exit(main())
