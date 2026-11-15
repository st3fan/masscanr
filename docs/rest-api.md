# REST service

Start `masscanr-server --listen 127.0.0.1:8080 --data-dir ./masscanr-data`.
The separate `masscanr-cli` executable offers only `watch` over this API. The
service has no built-in authentication, authorization, users, or application TLS.
Keep access on loopback or an existing private network, and supply only
authorized scan scope.

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
run snapshots refer to the service's filesystem. Run-ID inspection and saved
CSV/JSONL downloads are available through the API.

DBOS Go is pinned to v1.8.0. The workflow application version is `masscanr-v9-1`.
Workflow changes require an explicit version/recovery review; auto-upgrade and
Conductor are not enabled. App migrations reject unknown schema versions.

## JSON API

Requests and responses use JSON except for saved CSV/JSONL downloads. Request
bodies are limited to 1 MiB and
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
| `GET /v1/runs?active=true` | 200 versioned active-run snapshot envelope; includes cleanup |
| `POST /v1/runs/{id}/stop` | 202 exact immutable run; settled run is a no-op, unknown ID is 404 |
| `GET /v1/runs/{id}` | 200 run, including after its definition is deleted |
| `GET /v1/runs/{id}/inspection` | 200 validated journal state, including partial runs |
| `GET /v1/runs/{id}/exports/csv` | 200 saved CSV for a complete scan |
| `GET /v1/runs/{id}/exports/jsonl` | 200 saved findings JSONL for a complete scan |
| `GET /v1/catalog/regions` | 200 public provider region catalog |
| `GET /v1/catalog/instance-types?region=atl` | 200 plans available in the region; `atl` is the default |
| `GET /v1/scans/{id}/runs` | 200 history array |

Definition and per-scan history list routes accept `limit` (1–1000, default 100) and `after` (last ID from the
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
V9-compatible representation; definition fields use the snake_case names above.

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

## V10 watch snapshots and discovery

Every run response adds `snapshot_version:1`, `active`, `settled`,
`stop_requested`, and `worker_snapshots`. The state booleans come from the database
columns in the same SELECT as the run JSON, including for V9 historical rows.
`settled` is the inverse of `active`: a terminal cleanup failure is settled but
unsuccessful. A completed scanner may still be active during final reconciliation;
do not use `phase` or `summary.ScanOutcome` alone to decide when to detach.

`GET /v1/runs?active=true` returns:

```json
{"snapshot_version":1,"runs":[]}
```

Each element is the same run snapshot returned by `GET /v1/runs/{id}`. The array
contains all active rows, including provisioning, recovery, and cleanup; an empty
array means idle, not an error. `active=true` is required and other query parameters
are rejected with 400. No pagination is necessary under the existing one-active-run
constraint. This endpoint does not enumerate definitions or return historical runs.
When an observed run leaves discovery, fetch it by ID for its final outcome.

`worker_snapshots` is keyed by shard number as a string, or `instance:<id>` for an
unassigned worker discovered during reconciliation. Entries retain cumulative
`shard`, `id`, `ip`, execution `state`/`detail`, independent `cleanup`/
`cleanup_detail`, `progress`, `progress_at`, `found`, and optional `prepared`.
`progress` retains the existing Go field names and unknown flags, with ETA in
nanoseconds; `progress_at` is the time of that telemetry sample, not the poll time.
`found` is cumulative accepted findings, never a delta or a count of unique hosts.
Nil progress/count/timestamp values mean unavailable. Cleanup events do not erase
execution results or worker identity, and a finished worker does not hide peers
still scanning. V9's latest-event `workers` field remains for older clients.

Historical V9 rows can lack cumulative worker snapshots. They remain readable;
missing telemetry is shown as unavailable, not reconstructed from lossy events.
The DBOS workflow version and execution semantics remain `masscanr-v9-1`; this is
an additive JSON/API change, not a replay or migration of past scan traffic.

`POST /v1/runs/{id}/stop` records stop intent only for that exact active ID and
cancels only its local execution context. A settled historical run returns its
snapshot without changing a newer run of the same definition. Repeating the call
is harmless. The existing scan-level stop route remains available for V9 clients,
but viewers must use the run-specific route to avoid a stale-view race.

## Artifact inspection and saved exports

The artifact routes identify data by a server run ID only. They validate that
the journal belongs to that run and resides in the configured artifact directory.
Inspection replays the journal and returns `run_id`, `format`, `schema`,
`created_at`, `config`, `accepted_findings`, `records`, `workers`,
`scan_outcome`, `cleanup_outcome`, `complete`, `truncated`, and `undeleted`.
The worker list reports the last validated state, not current cloud state.
For unfinished or truncated journals, completion and unobserved coverage are
unconfirmed. Invalid journal content fails rather than silently skipping records.

CSV and JSONL routes stream existing saved files only when the journal confirms
a complete scan. Cleanup can be incomplete while scan results remain complete.
Incomplete results or a missing saved export return HTTP 409; neither route
creates or repairs files. Unknown runs return 404. Only `csv` and `jsonl` are
supported, with download content types and `no-store` cache headers. No local
artifact path is accepted from an API client.

The server uses Fiber v3 for incoming requests. Handler work has explicit
timeouts and is cancelled during server shutdown. Client disconnects do not
cancel a handler; if a scan start is uncertain, retry with the same
`Idempotency-Key`.
JSON request bodies remain limited to 1 MiB and receive a JSON 400 error when
they exceed that limit. Fiber has a 2 MiB transport cap; larger requests may
be closed before the handler can return a JSON error.

## Provider catalog

The catalog routes query Vultr's public catalog without a token. Regions include
ID, name, country, and continent. Instance types include ID, family, vCPU, memory,
disk, storage, and CPU details. Results are sorted by ID. Unknown regions return
404; provider failures return 502. Catalog lookup does not reserve capacity and
may differ from availability at scan start.

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
