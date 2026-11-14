# V9 REST service

V9 runs as `masscanr serve --listen 127.0.0.1:8080 --data-dir ./masscanr-data`.
The interactive `scan` command is retired. `inspect`, `export`, and provider
catalog commands remain available for maintenance. V9 has no authentication,
authorization, users, or application TLS. Supply only authorized scan scope.

## PostgreSQL and deployment

Set `DBOS_SYSTEM_DATABASE_URL` to the PostgreSQL connection string. For local
development, create a uniquely named database as your current user, then use a
socket DSN such as `host=/tmp dbname=masscanr_dev_<unique-id> sslmode=disable`.
The database must already exist. The service creates its versioned `masscanr`
application schema and DBOS system schema. No database credentials are needed
for the local socket configuration. Set `VULTR_API_TOKEN` in the environment
before starting a scan; it is not required for CRUD or offline maintenance.

One service process owns a database, enforced by a session advisory lock. Keep
the artifact directory on persistent storage and reuse it with the same database
on restart. The database contains definitions, frozen plans, run state, and DBOS
checkpoints. `.scan` files contain detailed findings; successful runs also produce
CSV and JSONL. Back up both the database and artifact directory. Artifact paths in
API responses refer to the service's filesystem; downloads/search are not included.

DBOS Go is pinned to v1.8.0. The workflow application version is `masscanr-v9-1`.
Workflow changes require an explicit version/recovery review; auto-upgrade and
Conductor are not enabled. App migrations reject unknown schema versions.

## JSON API

All requests and responses use JSON. Request bodies are limited to 1 MiB and
unknown fields are rejected. Errors have the form `{"error":"message"}`. Invalid
input returns 400; absent resources return 404; stale revisions, stale plans,
missing required approval, and active-run conflicts return 409. Target lookup
failure returns 422; missing runtime credentials or shutdown returns 503.
Unexpected database errors return 500 without exposing connection details.

Create a definition with `POST /v1/scans` (201):

```json
{
  "targets": ["192.0.2.1"],
  "backend": "masscan",
  "ports": "22,80,443",
  "shards": 2,
  "rate": 500000,
  "region": "atl",
  "instance_type": "vc2-1c-1gb",
  "timeout": "24h",
  "cleanup_timeout": "5m"
}
```

The example address is documentation-only. Targets may also be IPv4 CIDRs or AS
identities, with the V8 syntax/provenance rules. Only `targets` is required;
omitted options use the existing defaults (including the Nmap top 100 TCP ports).
Zero/empty values are validated, not silently defaulted. Timeouts use Go duration
strings. The response contains `id`, `revision`, `created_at`, `updated_at`, and
`definition` with effective defaults.

| Request | Result |
| --- | --- |
| `GET /healthz` | 200 ready, or 503 when PostgreSQL is unavailable |
| `GET /v1/scans` | 200 array of definitions |
| `GET /v1/scans/{id}` | 200 definition |
| `PUT /v1/scans/{id}` | 200 updated definition; body has `revision` and full `definition` |
| `DELETE /v1/scans/{id}` | 204; retains historical runs and artifacts |
| `POST /v1/scans/{id}/preflight` | 201 frozen plan; no body needed |
| `POST /v1/scans/{id}/start` | 202 accepted run |
| `POST /v1/scans/{id}/stop` | 202 latest run; repeated requests are harmless |
| `GET /v1/runs/{id}` | 200 run, including after its definition is deleted |
| `GET /v1/scans/{id}/runs` | 200 history array |

List routes accept `limit` (1–1000, default 100) and `after` (last ID from the
previous page). IDs sort lexically. Replacement and deletion conflict while the
definition has an active run. A replacement body is:

```json
{"revision":1,"definition":{"targets":["192.0.2.1"],"ports":"443"}}
```

Replacement uses defaults for omitted options and increments the revision.
Preflight returns `id`, `scan_id`, `revision`, `expires_at`, `config`, `timeouts`,
`workload`, and `requires_confirmation`. The nested effective Go scan structures
currently use their exported field names: `config.RequestedTargets`,
`config.Targets`, `config.Ports`, `workload.Addresses`, `workload.Ports`, and
`workload.Probes`. Timeouts in this snapshot are integer nanoseconds. This is the
initial V9 API for review; definition fields use the snake_case names above.

Plans last 10 minutes and freeze the exact resolved AS mapping and provenance.
Changing a definition invalidates older plans. Starting does not perform another
RIPE lookup. Above 1,000,000 planned probes, explicit `confirm:true` is required.
No worker or `.scan` artifact is created before plan acceptance.

Start with `Idempotency-Key: <unique-request-key>` (1–200 bytes) and this body:

```json
{"plan_id":"<preflight-id>","confirm":true}
```

Use the same key and plan when retrying a lost response. A different plan with the
same key conflicts. A successful start returns the same run for later retries,
even after completion. Only one run may be active, through cleanup; there is no
scan queue. A 503 during dispatch may mean the reservation was persisted: retry
the same key. Startup recovers reserved runs that were not dispatched.

## Run lifecycle

Run responses contain `id` (also the DBOS workflow/controller ID), `scan_id`,
immutable `plan`, `phase`, `created_at`, `artifact`, `summary`, `error`, and latest
`workers` events. `summary.ScanOutcome` and `summary.CleanupOutcome` are separate;
check both. `summary.Undeleted` and `summary.ReconciliationUnresolved` identify
incomplete cleanup. The workflow may complete normally while the domain scan is
cancelled: a stop is a persisted request to cancel work and finish cleanup.

Stop persists its intent and cancels the active Go context. The execution step
also polls stop intent every 200 ms. It cancels on a database polling error.
Cleanup has its own deadline and is not cancelled by the stopped scan context.
SIGINT/SIGTERM stops admitting work and waits for cleanup (shutdown ceiling:
30 minutes). The process runs in the foreground for a service manager to supervise.

A durable attempt marker is written before entering controller execution. If the
process crashes after this point, recovery cancels the previous DBOS workflow and
launches a separate cleanup workflow. It never repeats provisioning or scanning
for that attempt, even if the crash preceded the first cloud request. This
conservative boundary avoids ambiguous create retries and duplicate scan traffic.
The journal is replayed when available to retain a completed scan outcome;
otherwise the outcome is interrupted. Partial artifacts are retained.

Cleanup rechecks the exact run tag and `masscanr` label. Provider outages can leave
cleanup incomplete; the API reports this terminal outcome and IDs for operator
reconciliation. A terminal incomplete-cleanup run is not automatically retried.
Direct cancellation/rewind of DBOS workflows outside this API is unsupported;
restart reconciliation provides recovery for interrupted run records.

## Validation

Tests use fake providers/scanners, HTTP fixtures, and isolated PostgreSQL
databases. For local socket integration tests:

```sh
MASSCANR_TEST_POSTGRES='host=/tmp dbname=postgres sslmode=disable' go test -race ./...
```

The test DSN is a keyword DSN for an administrative database with CREATE DATABASE
permission. Each test creates and drops only its own unique database. Without the
environment variable, database tests skip; CI supplies PostgreSQL and runs them.
No actual application scan or cloud VM is needed for these tests.
