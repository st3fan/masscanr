# REST service

Start `masscanr-server --listen 127.0.0.1:18080 --data-dir ./masscanr-data`.
`masscanr-cli` offers only `watch` over this API. The service has no built-in
authentication or TLS. Keep the listener on loopback or a private network and
scan only authorized scope.

## Storage and development reset

Set `DBOS_SYSTEM_DATABASE_URL` to an existing PostgreSQL database. For local
development, create a uniquely named database as your current OS user and use
a socket DSN such as `host=/tmp dbname=masscanr_dev_<unique-id> sslmode=disable`.
The service creates its own application and DBOS schemas. One server process owns
a database through a session advisory lock. Set `VULTR_API_TOKEN` in the server
environment before starting a scan; management and preflight do not need it.

This development version requires a **fresh database**. Older application schemas
are rejected. There is no backfill or compatibility layer for old `/v1/scans`
resources or persisted rows. Create a new uniquely named database when updating
to this version; retain or deliberately discard old data and artifacts according
to your development needs. Keep the database and artifact directory together
when restarting the same version. Never reuse or drop an unrelated database.

PostgreSQL is the primary query store for normalized open-port results. A `.scan`
journal still records every accepted observation, including duplicates, and is
the evidence source for rebuilding results after an interrupted database write.
Completed scans also produce saved CSV and JSONL exports. Preserve both the
database and artifact directory if you need historical runs. DBOS Go remains
pinned to v1.8.0 and the workflow version is `masscanr-v10-1`. On restart,
the service validates the journal, reconciles only run-owned workers, and
retries unfinished whole shards within their two-retry budget. An invalid
journal or unresolved worker ownership leaves the run incomplete and triggers
cleanup instead of continuation.

## JSON:API and docs

Huma registers the Fiber v3 routes and serves OpenAPI 3.1 at `/openapi.json`.
Interactive Stoplight Elements docs are at `/docs`; JS and CSS are bundled under
`/docs/assets/`, with no CDN dependency.

For `/v1` JSON requests, send `Content-Type: application/vnd.api+json` and a
`data` resource containing `type` and `attributes`. Responses use the same media
type and contain `data` or an `errors` array. Use `Accept: application/vnd.api+json`
(or `*/*`) for JSON responses. Health, OpenAPI/docs, and successful CSV/JSONL
downloads are format exceptions. API errors, including failed downloads, use
JSON:API `errors`. Request JSON is limited to 1 MiB; the transport limit is 2 MiB.

The main resources are:

| Resource | Purpose |
| --- | --- |
| `targets` | Named, revisioned selector collection; entries include individual IPv4 addresses, IPv4 CIDRs, and supported AS selectors. |
| `scan-plans` | Reusable scanner, ports, rate, worker count, cloud placement, and timeout settings for one Target. Several plans may reference one Target. |
| `scan-preflights` | Short-lived, frozen effective scope and AS-resolution provenance for review and approval. |
| `runs` | One execution, linked to its Target and ScanPlan, with immutable preflight snapshot, lifecycle, progress, and cleanup. |
| `scan-results` | Deduplicated open TCP endpoint for a run; observation count and first/last timestamps summarize raw journal records. |

Create a Target and ScanPlan:

```json
{"data":{"type":"targets","attributes":{"name":"External network","entries":["192.0.2.1","192.0.2.0/28"]}}}
```

```json
{"data":{"type":"scan-plans","attributes":{"name":"Routine inventory","target_id":"<target-id>"}}}
```

The example addresses are documentation-only. Omitted ScanPlan `settings` use
server defaults. A supplied settings object must contain valid values for all
settings (`backend`, `ports`, `provider`, `workers`, `rate`, `region`,
`instance_type`, `timeout`, `cleanup_timeout`). Only `vultr` is supported as a
provider today. `PATCH` accepts a partial settings object and a matching
`revision`; it retains omitted fields and increments the revision:

```json
{"data":{"type":"scan-plans","id":"<scan-plan-id>","attributes":{"revision":1,"settings":{"ports":"22,80,443"}}}}
```

Target `PATCH` similarly requires `revision` and may change `name` or `entries`,
or set `archived:true`. Archiving is one-way, preserves run history, and blocks
new preflights. Changing either a Target or its ScanPlan invalidates an older
preflight. An active run keeps its frozen scope and configuration.

| Request | Result |
| --- | --- |
| `GET /healthz` | 200 readiness or 503 |
| `POST /v1/targets` | 201 Target |
| `GET /v1/targets`, `GET /v1/targets/{id}` | 200 Target list or one Target |
| `PATCH /v1/targets/{id}` | 200 updated or archived Target |
| `POST /v1/scan-plans` | 201 ScanPlan |
| `GET /v1/scan-plans`, `GET /v1/scan-plans/{id}` | 200 ScanPlan list or one ScanPlan |
| `PATCH /v1/scan-plans/{id}` | 200 updated ScanPlan |
| `POST /v1/scan-plans/{id}/preflight` | 201 frozen `scan-preflights` resource; no body |
| `POST /v1/scan-plans/{id}/start` | 202 accepted `runs` resource |
| `GET /v1/scan-plans/{id}/runs` | 200 run history for the plan |
| `GET /v1/runs?active=true` | 200 current active run list and snapshot version |
| `GET /v1/runs` | 200 recent run history across all plans, newest first |
| `GET /v1/runs/{id}` | 200 run snapshot |
| `GET /v1/runs/{id}/workers` | 200 paginated physical worker instance history |
| `POST /v1/runs/{id}/stop` | 202 stop request for the exact run |
| `GET /v1/scan-results` | 200 positive open-port results with coverage metadata |
| `GET /v1/runs/{id}/inspection` | 200 validated journal inspection |
| `GET /v1/runs/{id}/exports/{csv\|jsonl}` | 200 saved complete export |
| `GET /v1/catalog/regions` | 200 region catalog |
| `GET /v1/catalog/instance-types?region=atl` | 200 instance catalog |

Target, ScanPlan, run-history, per-plan run, physical worker, and result lists accept `limit` (1–1000,
default 100) and an opaque `after` cursor from `links.next`. Run history accepts
`filter[target_id]`, `filter[scan_plan_id]`, and `filter[phase]`. Results also accept
`filter[run_id]`, `filter[target_id]`, `filter[ip]` (IPv4), and `filter[port]`
(TCP port), plus `sort=ip`, `-ip`, `port`, or `-port`. Sort order is stable across
cursor pages with the run ID as a tie-breaker. `GET /v1/runs?active=true` instead returns the one-active-run view;
other query parameters are rejected. Resource IDs and revision numbers are
returned in the JSON:API resource. Stale revisions, stale preflights, active-run
conflicts, and missing confirmation return 409. Invalid input returns 400;
unknown IDs return 404; failed target resolution returns 422; missing runtime
credentials or shutdown returns 503.

## Preflight and start

`POST /v1/scan-plans/{id}/preflight` resolves AS selectors, computes the effective
scope and workload, and saves a ten-minute preflight. Check `config.Targets`,
`config.RequestedTargets`, `workload`, `shard_count`, `effective_workers`,
`max_probes_per_shard`, and `requires_confirmation` in its
attributes before starting. Resolution provenance and all effective settings are
frozen in the preflight and copied into the run. Starting does not do another
RIPE lookup. More than 1,000,000 planned probes require explicit `confirm:true`.

Start with an `Idempotency-Key` header and a body like:

```json
{"data":{"type":"runs","attributes":{"preflight_id":"<preflight-id>","confirm":true}}}
```

A retry of an uncertain response must use the same key and preflight ID. The
server returns the same run for an accepted retry, including after completion.
Only one run can be active globally, including cleanup. A 202 means accepted,
not that cloud provisioning or scanning succeeded.

Every run includes `snapshot_version:3`, `active`, `settled`, `stop_requested`,
`worker_snapshots`, `target_id`, `scan_plan_id`, and immutable `preflight` in its
attributes. `summary.ScanOutcome` and `summary.CleanupOutcome` are separate.
Logical shards contain at most 1,048,576 address/port probes and are assigned
round-robin to stable worker slots. Requested workers are reduced to the shard
count when there is less work than workers. `summary.CompletedShards` and
`summary.ShardCount` report total completed shard progress. Each
`worker_snapshots` entry reports its slot, current shard and attempt, and
completed/assigned shard counts. Current-shard percent is unknown until a
scanner reports it. Physical replacements retain the slot's queue; the
`/workers` endpoint records each instance separately. Finding counts include
duplicate journal observations, while results remain deduplicated. Missing
telemetry is unknown.
`GET /v1/runs?active=true` includes `meta.snapshot_version:3` and an empty
`data` array while idle. Stop targets one run ID; a settled run is a no-op.

## Results, evidence, and coverage

The controller validates each open TCP finding against the frozen scope and
ports. It appends every accepted finding to the `.scan` journal. Bounded,
journal-synced batches then update PostgreSQL during execution. A run/IP/port
has one `scan-results` row with an observation count and first/last timestamps.
The row includes selector and originating prefix attribution. The query API
provides positive observations only; **absence never proves that a port is
closed**. Filter by `run_id` to see `meta.coverage` with the run phase, result
state (`none`, `live`, `partial`, `complete`, or `error`), and journal checkpoint.
A non-complete run has unconfirmed coverage, including when it has findings.

On finalization or recovery, the server validates and can rebuild PostgreSQL
results from the journal. Rebuilds are atomic: a corrupt later record does not
publish partially replayed rows. Missing or invalid evidence marks result state
`error`; the run and its cleanup remain inspectable. Validated unfinished
shards can resume after owned-worker reconciliation. The journal remains the raw evidence for duplicate and partial
observations; normalized PostgreSQL rows are the query representation.

Inspection at `/v1/runs/{id}/inspection` replays the saved journal for that run,
including validated partial state after interruption or failure. CSV and JSONL
downloads stream existing files only when the journal proves a complete scan.
A missing export or incomplete scan returns a clear JSON:API error without
creating or changing a file. An incomplete cleanup can coexist with complete
scan results. No artifact path is accepted from a client.

## Operations and validation

Fiber handlers have bounded operation contexts and shutdown cancellation. A
client disconnect does not cancel a handler, so use the same idempotency key for
uncertain start responses. SIGINT/SIGTERM waits for owned worker cleanup. The
server never deletes unrelated instances; cleanup matches both the `masscanr`
label and the exact run ownership tag.

Tests use fake providers/scanners, HTTP fixtures, and isolated PostgreSQL
databases. Set `MASSCANR_TEST_POSTGRES` to an administrative socket DSN with
CREATE DATABASE permission to run database tests:

```sh
MASSCANR_TEST_POSTGRES='host=/tmp dbname=postgres sslmode=disable' go test -race ./...
python3 -B -m unittest discover -s scripts -p 'test_start_scan.py'
```

Each test creates and drops only its own unique database. No cloud VM or actual
application scan is needed for these tests.
