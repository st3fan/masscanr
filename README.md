# masscanr

## V9 service (under review)

V9 exposes scan CRUD, frozen preflight, start/stop, and run history through a REST
service backed by PostgreSQL and DBOS. Start with `masscanr serve`; the interactive
`scan` command is retired. See the [REST API and operations guide](docs/rest-api.md)
for database setup, request examples, recovery semantics, and local test commands.
[V9 planning issue](https://github.com/st3fan/masscanr/issues/86) tracks this iteration.

### Run locally

The local PostgreSQL server accepts connections as your current operating-system
user through its Unix socket. Create a uniquely named development database:

```sh
masscanr_db="masscanr_dev_$(openssl rand -hex 8)"
createdb --host=/tmp "$masscanr_db"
export DBOS_SYSTEM_DATABASE_URL="host=/tmp dbname=$masscanr_db sslmode=disable"

go build -o masscanr ./cmd/masscanr
./masscanr serve --listen 127.0.0.1:8080 --data-dir ./masscanr-data
```

The service initializes its PostgreSQL and DBOS schemas automatically. Keep the
same database and artifact directory when restarting. Set `VULTR_API_TOKEN` in
the service's environment through your secret manager before starting scans;
CRUD and preflight work without it. For example, with 1Password:

```sh
VULTR_API_TOKEN='op://Company/Vultr/api-token' \
  op run -- ./masscanr serve --listen 127.0.0.1:8080 --data-dir ./masscanr-data
```

Use Ctrl-C or SIGTERM to stop the service and wait for worker cleanup. Only one
service process may use a database. This version has no users or authentication.

### Manage a scan

In another terminal, check readiness and create a scan definition. Replace the
placeholder below with a network you are authorized to scan. These commands use
`curl` and `jq`.

```sh
curl --fail-with-body http://127.0.0.1:8080/healthz

scan_id=$(curl --fail-with-body -sS http://127.0.0.1:8080/v1/scans \
  -H 'Content-Type: application/json' \
  -d '{"targets":["YOUR_AUTHORIZED_IPV4_OR_CIDR"],"ports":"22,80,443"}' \
  | jq -r .id)

# Resolve and inspect the exact scope before starting.
curl --fail-with-body -sS -X POST \
  "http://127.0.0.1:8080/v1/scans/$scan_id/preflight" > preflight.json
jq . preflight.json
plan_id=$(jq -r .id preflight.json)

# Start only after reviewing the returned targets and planned probe count.
# Keep this request key when retrying a lost response.
request_key=$(openssl rand -hex 16)
run_id=$(curl --fail-with-body -sS \
  "http://127.0.0.1:8080/v1/scans/$scan_id/start" \
  -H 'Content-Type: application/json' \
  -H "Idempotency-Key: $request_key" \
  -d "{\"plan_id\":\"$plan_id\",\"confirm\":true}" | jq -r .id)

curl --fail-with-body -sS "http://127.0.0.1:8080/v1/runs/$run_id" | jq .

# Request stop when needed; poll run status until cleanup settles.
curl --fail-with-body -sS -X POST \
  "http://127.0.0.1:8080/v1/scans/$scan_id/stop" | jq .
```

Plans expire after ten minutes. Only one scan can run at a time, including worker
cleanup. Results are saved in the artifact directory as `.scan` journals and,
after successful completion, CSV/JSONL exports. The run response exposes artifact
paths and separate scan/cleanup outcomes. See the [full API guide](docs/rest-api.md)
for update/delete requests, pagination, recovery behavior, and test setup.

### Historical CLI reference

The sections below describe the V1–V8 engine, artifacts, and historical CLI. Their
scan invocation examples are retained as reference; use the V9 API for execution.

Run masscan or ZMap shards on temporary Vultr VPS workers, stream discoveries back to a
local `.scan` document with CSV/JSONL exports, and watch each worker in a terminal UI. The controller is
written in Go and needs no root privileges. Masscan is the default; select
ZMap with `--backend zmap`. Workers use Alpine packages and root/password SSH.

[V1 project description](https://github.com/st3fan/masscanr/issues/1) defines the
scan engine and worker lifecycle. [V2 guidance](https://github.com/st3fan/masscanr/issues/21)
specifies the Bubble Tea terminal interface. [V3 guidance](https://github.com/st3fan/masscanr/issues/40)
defines the Alpine worker platform. Work is tracked in native version sub-issues
and small PRs ordered with `gh stack`. [V4 guidance](https://github.com/st3fan/masscanr/issues/33)
defines selectable masscan/ZMap backends and their shared scan contract.
[V5 guidance](https://github.com/st3fan/masscanr/issues/57) defines environment-only
Vultr credentials. [V6 guidance](https://github.com/st3fan/masscanr/issues/60)
defines self-contained text scan documents and offline inspection/export.
[V8 guidance](https://github.com/st3fan/masscanr/issues/72) defines IPv4 address/AS targets and result provenance.
[V7 guidance](https://github.com/st3fan/masscanr/issues/65) defines confirmation
before large scans, measured by planned probes.

## Build and configure

Requires Go 1.26 or newer and outbound HTTPS/SSH from the controller. No local
masscan or ZMap installation, SSH executable, SSH key, or privileged account is required.

```sh
go build -o masscanr ./cmd/masscanr
```

Set `VULTR_API_TOKEN` in the controller's environment before scanning. It must
contain a non-empty Vultr API token. For example, with the
[1Password CLI `op run` command](https://developer.1password.com/docs/cli/secrets-environment-variables/)
installed and signed in:

```sh
VULTR_API_TOKEN='op://Company/Vultr/api-token' \
  op run -- ./masscanr scan 192.0.2.0/24
```

Replace `op://Company/Vultr/api-token` with the secret reference for your vault,
item, and token field, and replace the documentation CIDR with your authorized
target range. `op run` resolves the reference and supplies the token to masscanr
for the duration of the process; the shell only receives the secret reference.

V5 no longer reads `.env` files, accepts `--env-file`, or uses `VULTR_API_KEY`.
When upgrading, supply your existing token through `VULTR_API_TOKEN` instead.
Keep the token on the controller and out of worker configuration and logs.

The token needs permissions to read OS/region availability and create, list,
inspect, and delete instances. The selected region must have capacity for the
chosen plan. The app fails before provisioning if the plan/image is unavailable.

## Run

Supply one or more company-owned, authorized IPv4 CIDRs, individual IPv4
addresses, or AS targets. These examples use documentation addresses; replace
them with your authorized scope:

```sh
./masscanr scan 192.0.2.0/24 198.51.100.0/24
./masscanr scan 192.0.2.42 198.51.100/24
./masscanr scan --shards=4 --rate=500000 --region=atl \
  --instance-type=vc2-1c-1gb --output=inventory.scan 192.0.2.0/24
./masscanr scan --ports=22,80,443,8000-8100 192.0.2.0/24
./masscanr scan --slug=epag 192.0.2.0/24
./masscanr scan --backend zmap --shards=4 --ports=22,80,443 192.0.2.0/24
```

Scan options work before or after targets, following `scan`. Use
`masscanr scan --help` for scan options and `masscanr --help` for commands.
Scanning requires the `scan` subcommand; a bare CIDR is an unknown command.

| Option | Default | Meaning |
| --- | --- | --- |
| `--backend` | `masscan` | Scan engine: exactly `masscan` or `zmap` |
| `--shards` | `2` | Number of temporary workers |
| `--rate` | `500000` | Requested packets/second **per worker** |
| `--region` | `atl` | Vultr region (Atlanta) |
| `--instance-type` | `vc2-1c-1gb` | Vultr plan |
| `--ports`, `-p` | Nmap 7.95 top 100 TCP ports | Comma-separated ports, inclusive ranges, or mixed forms |
| `--output` | Timestamped `masscanr-*.scan` | New scan document plus CSV/JSONL exports; `-` streams scan JSON Lines to stdout |
| `--slug` | Empty | Text inserted before the timestamp in generated output filenames |
| `--timeout` | `24h` | Maximum scanning time; cleanup follows |
| `--cleanup-timeout` | `5m` | Separate deadline for worker deletion |
| `--no-tui` | `false` | Lifecycle logs instead of the live table |
| `--version` | | Print version and exit |

For example, `--slug=epag` generates a name such as
`masscanr-epag-20261105T042952.086607609Z.scan`. Omitting the slug preserves
`masscanr-<timestamp>.scan`. Slug text is preserved; path separators and control
characters are rejected. An explicit `--output` path takes precedence over the
slug. With `--output=-`, CSV/JSONL filenames still use the timestamp and slug.

Port syntax accepts TCP numbers 0–65535, deduplicates overlaps and compacts
ranges. For example, `--ports=22,80,443,8000-8100` or `-p=1-65535`. IPv6, UDP,
service names and protocol-prefixed port syntax are outside V1.

CIDRs are masked and duplicate/contained ranges are removed. The normalized
target list is displayed before provisioning: for example, `192.0.2.42/24`
means `192.0.2.0/24`. All workers use the same targets, ports, randomization seed
and backend/build. Controller shard IDs are one-based: masscan receives
`--shards i/N`; ZMap receives `--shards N --shard i-1`. The shared effective
seed is retained in metadata without truncation. Unknown backends and known
unsupported settings fail before credentials, output creation or provisioning.

Two default workers request a combined 1,000,000 packets/second. The UI shows
observed rates; actual throughput depends on the VPS and network.

## IPv4 and AS targets

Full and abbreviated CIDRs are accepted: `10.0.0.0/8` and `10/8` both mean
`10.0.0.0/8`; `192.168/16` means `192.168.0.0/16`. Missing trailing octets are
zero-filled before masking. An individual IPv4 address such as `192.168.0.1`
means exactly `192.168.0.1/32`, including its result `target` value. Bare addresses
require four octets. Ambiguous leading-zero octets, IPv6, names and address ranges
are rejected.

`AS1234` resolves through the controller's HTTPS connection to the
[RIPEstat RIS Prefixes API](https://stat.ripe.net/docs/data-api/api-endpoints/ris-prefixes).
Use `masscanr scan AS1234` only when the entire expanded scope is authorized.
AS input is case-insensitive and accepts decimal 1–4294967295; for example,
`as001234` becomes `AS1234`. Bare numbers, AS0, signs and ASDOT syntax are rejected.

The lookup requests originating IPv4 prefixes from the latest available snapshot
(`list_prefixes=true`, `types=o`, `af=v4`, `noise=keep`). Transit networks are
excluded. Snapshot time and local fetch time are shown and saved. Routing
observations can lag current conditions and do not establish ownership or scan
authorization. You must be authorized to scan every expanded prefix.

Mixed targets are supported. Each distinct AS is resolved once per invocation;
identical canonical targets are deduplicated in first-occurrence order. No RIPE
request is made for direct-only scans. Resolution happens after local option
validation and before the V7 confirmation gate, artifact creation or workers.
Any lookup failure or AS with no IPv4 origin prefixes fails the entire invocation.
The controller does not continue with partial targets or an older cached mapping.
The approved mapping is frozen for the run; workers and offline tools do not look
up or refresh AS data.

Lookups have a 20-second deadline per AS, a 4 MiB response limit, at most 1,024
requested target arguments of at most 64 bytes each, and at most 32,768 total
resolved prefix memberships across all targets. Exceeding a limit fails before
provisioning. Responses must match the expected resource, origin-prefix schema,
counts, and snapshot. IPv6 values are excluded; malformed prefixes are errors.

Workers scan the exact normalized union of the resolved scope, removing duplicate
and contained ranges without adding addresses. Overlapping targets do not create
extra probes or result copies. For each accepted finding, the first matching
requested target in command-line order supplies `target`. Within that AS, the
most-specific resolved origin prefix containing the IP supplies `prefix`.
Real scanner duplicates remain in results.

## Large-scan confirmation

V7 asks for confirmation when **planned probes exceed 1,000,000**:

```text
planned probes = unique requested IPv4 addresses × unique requested TCP ports
```

The count covers the resolved union of all target types and deduplicated port
ranges. AS expansion therefore happens before the confirmation decision.
Shards divide this work and do not multiply it. Both masscan and ZMap use the
same requested-scope count, before any backend-specific exclusions. This is
planned scan work, rather than a prediction of exact wire packets or duration;
sending rate is a separate setting.

For example, at the default 100 ports, a /19 means 819,200 probes and a /18
means 1,638,400 probes. The latter requires confirmation. A /16 at 100 ports
means 6,553,600 probes, while all 65,536 ports on just 16 addresses means
1,048,576 probes. Exactly 1,000,000 probes does not trigger confirmation.

The dialog leads with the probe count and shows the port/address factors,
threshold, and normalized targets. **Cancel is selected by default**. Use Tab
or Left/Right to select Continue and Enter to accept; Up/Down or Page Up/Down
scroll the warning and targets. Escape, Ctrl-C, and Ctrl-D cancel. Resize very
small terminals to at least 24 columns and 10 rows to review the dialog.

With `--no-tui`, the same warning appears as a plain prompt; type `yes` and
press Enter to proceed. Enter alone, another answer, Escape, Ctrl-C, Ctrl-D,
EOF, or an input error prevents the scan. Confirmation finishes before output
or spool creation, provider operations, or scanning. Prompts use stderr, keeping
stdout scan results separate.

Large scans require interactive terminals on **both stdin and stderr**.
Redirecting either one fails clearly before starting a large scan; redirected
stdout (`--output=-`) is supported when stdin and stderr remain interactive.
V7 has no unattended bypass. Smaller scans retain their existing behavior.
The threshold is an initial review checkpoint; targets must always be company
networks authorized for scanning.

## Backend compatibility

Omitting `--backend` and passing `--backend masscan` select the same Alpine
masscan 1.3.2 path. Both engines support the top-100 default and TCP port
lists/ranges, including ports 0 and 65535, with the configured packet rate
applied per worker across the whole port set.

ZMap support is deliberately pinned to Alpine's x64 `zmap-4.3.4-r0` build.
Preparation verifies its package, executable checksum and output schema;
unsupported versions/builds fail without falling back to masscan. Known limits:

- Rate: 1..2147483647 packets/second per worker.
- Seed: 0..9223372036854775807, generated once within that range.
- Shards: 1..65535, no more than the eligible IP/port pair count. Small scopes
  using ZMap's smallest permutation group require fewer than 256 shards.
- One sender thread and one probe per IP/port pair; 10-second response cooldown.

ZMap uses a private per-run allowlist containing exactly the normalized CIDRs,
`--config /dev/null`, and an explicit empty blocklist. Distribution defaults
therefore cannot silently exclude private/company networks. Upstream always
excludes `0.0.0.0`, which is removed from the eligible-work count; a scope
containing only that address fails preflight. The common finding sink validates
requested CIDRs and ports independently of native filtering.

ZMap accepts only successful TCP SYN-ACK responses and uses a one-million-entry
IP/port repeat window. Multiple ports on the same IP survive. Window eviction
can admit late duplicates, and accepted duplicates remain in the output.
Different engines can discover different endpoints; identical results are not
promised. Sending failures produce an incomplete run.

ZMap's native percentage estimates elapsed time including cooldown, so it is
not displayed as a measured sending percentage. During sending, percent is
unknown while measured rate and estimated remaining time are shown. Zero active
sender threads marks response waiting (sending at 100%); successful process
exit and drained streams establish completion. Missing/stale telemetry never
proves completion.

The [compatibility contract](docs/zmap-compatibility.md) records the pinned
source evidence, schemas, exclusions, seed/port/shard limits and offline proof.

## Discover regions and instance types

```sh
./masscanr list-regions
./masscanr list-instance-types
./masscanr list-instance-types --region=ewr
```

`list-regions` shows each region's ID, name/city, country code and continent.
`list-instance-types` defaults to Atlanta (`atl`) and lists plans currently
available in that region, ordered by ID. Columns include family, vCPU count,
RAM in MB, disk count/capacity, storage type, CPU sharing and CPU vendor where
Vultr provides them. Missing values are shown as `-`.

These commands read the public catalog without requiring `VULTR_API_TOKEN` or
managing workers. Catalog pages are fetched automatically. Use
`masscanr help <command>` or `<command> --help` for command-specific usage.

The catalog includes plans that require block-storage boot. `scan` currently
requires an included local boot disk and rejects unsupported boot storage
before provisioning. Region availability is checked across plan families,
including local-disk High Frequency and High Performance plans.

## Progress and results

The Bubble Tea TUI runs on stderr and has two tabs:

- **Overview** identifies the selected backend and shows total scan progress, observed packets/second, discoveries,
  scan ETA, completed shards and VPSs whose deletion is not yet confirmed. The
  table retains each shard's address, lifecycle, progress and observed rate;
  wide terminals also show ETA and discoveries. Enter expands the selected
  shard's full instance ID, scan/cleanup outcomes and diagnostic details.
- **Config** shows every normalized CIDR with address counts, the exact TCP port
  selection, backend/version/build identity, per-worker and aggregate requested rates, worker settings, shared
  seed, timeouts and output destinations. These are the effective settings for
  the current run and are read-only. Long lists and paths wrap and scroll.

| Key | Action |
| --- | --- |
| Tab / Shift+Tab | Switch tabs |
| 1 / 2 | Open Overview / Config |
| Up / Down | Select a shard on Overview; scroll on Config |
| Enter | Expand/collapse selected-shard details |
| Page Up / Page Down | Scroll details when expanded, otherwise move through shards; scroll Config |
| Ctrl-C | During a run, stop scans and wait for cleanup; after the run settles, quit |
| q | Quit after the run settles; ignored while scanning or cleaning up |

Native shards have approximately equal shares of scan work; overall progress
uses equal weights and includes pending/failed shards in the denominator.
Missing telemetry shows `—`; a known update with unavailable sending percentage
shows `unknown`. Overall percentage remains unknown if an active shard cannot
measure it. Confirmed scanner exit establishes that shard at 100%. Failure
retains the last known percentage. Discoveries count only accepted findings
from the shared scope/port validator, including duplicates, rather than native
packet counters; they do not represent a count of unique hosts. Rates are observed throughput, distinct from Config's
requested limits. Telemetry becomes stale after five seconds without progress;
its last percentage remains visible, but its rate is excluded from totals.
Scan ETA is the slowest remaining shard's estimate, shown only when every
unfinished shard has fresh telemetry. It excludes provisioning, collection,
export and cleanup time.

Reaching 100% still permits response waiting, result collection and VPS cleanup.
Scan outcome and deletion outcome are retained separately, so deletion cannot
hide a scan failure. The UI remains open after cleanup and export finish,
including failed or cancelled runs. Inspect the final outcome, errors, shard
details and Config, then press `q` or Ctrl-C to quit. The terminal is restored
before printing a durable run summary. SIGTERM cancels active scans and starts
cleanup; it can also dismiss the settled interface.

The theme uses bright green on black, yellow for selection/waiting and bright
red for failures. Progress bars use ASCII, for example `[========>...........]`.
Narrow terminals use two lines per worker; long tables scroll with selection.
Set `NO_COLOR=1` to disable color. State labels remain readable without color.

`--no-tui` selects plain lifecycle logs. Redirecting stderr selects this mode
automatically. Native results on stdout (`--output=-`) remain separate from the
UI. A terminal UI failure requests scan cancellation and cleanup, and returns
an error.

Each worker is deleted as soon as its scanner finishes and its output is
collected, including the selected engine's response-wait phase. Its status becomes
`shutdown` after deletion is confirmed, while other shards continue scanning.
The row keeps its scan statistics and any scan failure.

Both engines stream into one private (`0600`), exclusively created `.scan`
document. It contains the effective configuration, shared seed, observed backend
and worker platform, instance identities and ownership tag, lifecycle changes,
sampled progress, every accepted finding, and final scan/cleanup outcomes. The
[format contract](docs/scan-format.md) specifies schema 2, schema-1 compatibility and recovery rules.

The file is UTF-8 JSON Lines: one record per line, with contiguous sequence
numbers, controller observation time, type, and matching payload. For example,
a finding record within a document looks like this:

```jsonl
{"type":"finding","seq":42,"at":"2026-11-07T15:01:00Z","finding":{"shard":1,"ip":"192.0.2.1","port":443,"protocol":"tcp","state":"open","timestamp":1794063660,"target":"192.0.2.0/24"}}
```

Configuration appears first and remains immutable. The seed is a decimal string
so general JSON tools cannot lose integer precision. Progress measurements may
be `null` (unknown), distinct from measured zero. Findings preserve arrival
order and duplicates; their numeric Unix-seconds timestamp is the scanner's
original observation time. Both backends use the common scope/port validator.
ZMap uses the response TCP source port and capture timestamp, with no local-clock
fallback. Raw ZMap CSV headers never enter the document.

The controller syncs configuration before provisioning, creation intent before
each create call, known instance identities and lifecycle transitions immediately,
and findings in batches on a one-second timer. A final `finished` record is
synced before success is reported. Disk, flush, sync or close errors still cause
an unsuccessful command and may prevent durable results or a final record.
Storage failure stops scans and triggers independent ownership-checked cleanup;
cleanup continues even when the file cannot be updated.

Scan completion requires every scanner's successful exit, final response wait
and drained streams. Cleanup is recorded separately: complete findings remain
complete when deletion fails, while the command exits unsuccessfully and identifies
unresolved resources. Export delivery errors also remain separate from scan outcome.

Inspect a saved document without credentials or cloud access:

```sh
./masscanr inspect inventory.scan
```

Inspection displays configuration, accepted finding count, observed backend,
last recorded worker state, errors and independent scan/cleanup outcomes.
These are historical observations, not evidence of currently running workers.
Without `finished`, completion is unconfirmed. An unterminated final record is
ignored with an explicit truncation diagnostic; the complete validated prefix
remains available. Malformed newline-terminated records, invalid state/sequence,
inconsistent summaries and unsupported schemas are errors, never silently skipped.
Records are limited to 4 MiB, JSON nesting to 64, and shards to 65,535. No API
tokens, passwords, environment snapshots or raw commands are persisted.

After a successful scan and cleanup, the controller also writes CSV and
findings-only JSONL exports. `inventory.scan` produces `inventory.csv` and
`inventory.jsonl`; custom filenames without a trailing `.scan` have extensions
appended (`inventory.txt.csv`, for example). Each finding becomes one row/object,
preserving order and duplicates:

```csv
ip,port,addr,protocol,state,timestamp,target,prefix
192.0.2.1,80,192.0.2.1:80,tcp,open,1700000000,192.0.2.0/24,
192.0.2.2,443,192.0.2.2:443,tcp,open,1700000001,192.0.2.0/24,
```

```jsonl
{"ip":"192.0.2.1","port":80,"addr":"192.0.2.1:80","protocol":"tcp","state":"open","timestamp":1700000000,"target":"192.0.2.0/24"}
{"ip":"192.0.2.2","port":443,"addr":"192.0.2.2:443","protocol":"tcp","state":"open","timestamp":1700000001,"target":"192.0.2.0/24"}
```

An AS finding also includes its origin prefix, for example (illustrative mapping):

```jsonl
{"ip":"192.0.2.42","port":443,"addr":"192.0.2.42:443","protocol":"tcp","state":"open","timestamp":1700000000,"target":"AS1234","prefix":"192.0.2.0/24"}
```

Direct CIDR/IP findings omit `prefix` in JSON and have an empty CSV cell.
Masscan-compatible list exports keep their existing finding lines and cannot
carry these per-finding fields; use `.scan`, CSV or JSONL to retain provenance.

An empty successful scan produces a CSV header and an empty findings JSONL file.
Exports stream from disk, use private permissions and never overwrite existing
files. Conversion failure preserves the `.scan` source, discards failed partial
exports, and returns an export-specific error. Failed/cancelled scans do not
produce automatic exports.

Export later, including a masscan-compatible `.list` for existing tools:

```sh
./masscanr export inventory.scan --format=list --output=inventory.list
./masscanr export inventory.scan --format=csv --output=inventory.csv
./masscanr export inventory.scan --format=jsonl --output=inventory.jsonl
./masscanr export interrupted.scan --format=csv --output=partial.csv --allow-incomplete
```

Explicit export requires confirmed complete results by default. It also works
when scanning completed but cleanup failed, with a cleanup diagnostic. The
`--allow-incomplete` flag permits the validated prefix of failed, cancelled,
unfinished or truncated documents, with a partial-results warning. It cannot
bypass corruption. Partial discoveries do not establish coverage of unobserved
endpoints. Input and export destinations are never overwritten.

With `--output=-`, the entire scan JSON Lines stream goes to stdout; the UI and
summaries remain on stderr. A private temporary spool supports automatic CSV/JSONL
conversion and is removed on normal exit. Exports use generated timestamp/slug
names in the current directory. Use a file destination to retain a `.scan`
artifact, or arrange persistence of stdout yourself.

**V6 migration:** primary output, including `--output=-` and explicit filenames
ending in `.list`, now contains scan JSON Lines. Change filenames to `.scan`,
update consumers, or use the explicit list export above. Existing old `.list`
files remain usable with their original tools; V6 does not import or rewrite them.
The findings-only `.jsonl` export is distinct from the `.scan` journal.

**V8 migration:** new scan documents use schema 2 and require ordered target
mappings and finding provenance. Older readers must be upgraded to read schema 2.
V8 still inspects and exports schema-1 documents offline, without inventing target
attribution or querying RIPE. Their new CSV provenance columns are empty and their
JSONL provenance fields are absent. New CSV exports append `target,prefix` after
the existing six columns; update consumers that require an exact column count.

The format is an append-only text journal with linear replay, not a transactional
database. Sync timing is not a hard loss bound during storage failure. It supports
inspection and manual reconciliation, not restart/resume or automatic post-crash
worker deletion. Session history, a cross-scan inventory database and automatic
shard replacement remain deferred.

## Temporary workers and cleanup

Workers carry the **`masscanr` label** and a unique **`masscanr-run-<id>` tag**.
Cleanup requires both the app label and the current run tag, verified with a
fresh API lookup before deletion. Other infrastructure and workers from other
runs are never selected. No IP denylist or account-wide deletion is used.

The app selects one unambiguous Vultr Alpine Linux x64 image, retrieves the
generated root password, and connects directly as root. The public OS catalog
does not advertise its patch release: after SSH readiness the app checks
`/etc/alpine-release`, accepting stable 3.24.4 and later 3.24 patches. An earlier
patch, another release series or an ambiguous catalog fails preparation/preflight
and triggers normal cleanup where workers were already created.

Preparation reads the image's APK repositories, requires stable v3.24 `main`,
and derives a missing `community` URL from the configured main mirror. Active
mixed-release, edge, tagged, local or credential-bearing repository entries are
rejected. Installation passes only those validated sources explicitly to APK
with its normal signature verification; ambient repository snippets are ignored
and the image's repository files remain unchanged.

For masscan, `apk add --no-cache` installs `masscan~1.3.2` and GNU `coreutils`.
The latter provides `stdbuf` and its musl-compatible preload library so findings
arrive before scanner exit. For ZMap, install only `zmap=4.3.4-r0`; its CSV
module flushes each record and needs no buffering helper. Neither path installs
a compiler toolchain or the unused scanner. Each ready row records the actual
Alpine release, packages and executable checksum. All workers must agree on the
selected backend, version and build before any shard starts.

The API key stays on the controller;
passwords are not logged, serialized, placed in command arguments, or persisted.
SSH trusts the first host key for each newly created worker and pins it in
memory for later connections in that run. V1 does not independently attest the
initial key.

Provisioning, readiness and package installation have bounded timeouts. Partial
provisioning, worker failures, local output failures, Ctrl-C and normal success
all attempt deletion. Cleanup has its own deadline even after scan cancellation,
continues across worker failures, and verifies deletion by waiting for API 404.
Final cleanup retries workers whose immediate deletion failed; workers with a
confirmed shutdown are not deleted again or shown returning to cleanup.
Ambiguous creation responses trigger repeated reconciliation by the run tag;
creation POSTs are never blindly retried.

Deletion failures identify the remaining instance IDs. Inspect those instances
in Vultr and remove the run's temporary resources manually if needed. SIGKILL,
host loss, network/API outages, or a very late ambiguous create can prevent
automatic cleanup. V1 has no persistent recovery catalog; use the run tag shown
in stderr/results to identify that run's resources.

## Provider error details

Failed Vultr requests print the HTTP status/reason, method and endpoint,
request/trace ID when available, and the provider's response body. JSON is
indented; plain-text responses retain their line breaks. Creation errors also
identify the region, plan and OS ID, alongside the shard number.

Example layout:

```text
masscanr: create shard 5: Vultr API error: HTTP 400 Bad Request
  request: POST /v2/instances
  request ID: <provider request ID>
  response:
    {
      "error": "<Vultr's explanation>",
      "status": 400
    }
```

Details are printed by default. The configured API key, password/token fields
and other credential fields are redacted; request headers and payloads are
not dumped. Oversized responses have an explicit truncation marker at 64 KiB.
An earlier failure's discarded response body cannot be recovered afterward.

Preflight distinguishes a plan missing from the global catalog from a plan
present in the catalog but absent from the current regional availability
response. The latter error names the region endpoint and suggests
`masscanr list-instance-types --region REGION`. A website catalog listing does
not explain why a particular API response omits a plan; the app reports the
API's result without guessing its capacity or account-policy cause.

## Development and validation

```sh
go test -race ./...
go vet ./...
go build ./cmd/masscanr
```

Default tests need no credentials and create no cloud resources. They exercise
Vultr HTTP fixtures, local root/password SSH servers, CLI validation, progress
parsing, combined output, concurrency, cancellation, ownership protection and
cleanup. A cross-component test runs the real HTTP/SSH clients against local
fixtures through the controller. V6 adds offline scan-document replay,
inspection/export, exact-integer and corruption checks, injected persistence
failures, periodic sync cancellation, and large streamed-result coverage.
The benchmarks in `internal/scanfile` measure in-memory append/replay overhead;
they do not measure disk sync latency or scanner throughput.

V2 UI checks use synthetic lifecycle events for aggregation, navigation, long
CIDR/port lists, resizing, stale telemetry, cancellation and cleanup failures.
A Linux pseudo-terminal test runs only the UI with fixture events and verifies
the completed screen staying open until explicit quit, then raw-mode,
alternate-screen and theme-color restoration. It skips
when pseudo-terminals are unavailable. These checks do not run the app or scan
any network.

CI retains the host Go race/vet/build checks and runs separate native masscan
and ZMap checks in Alpine 3.24.4 containers pinned by digest. Build-time APK installation requires
repository access; runtime checks use `--network none --cap-drop ALL`, with no
cloud credentials or workers. To run the same checks locally with Docker:

```sh
scripts/check-alpine.sh
scripts/check-zmap.sh
```

This verifies Alpine/version/repository policy, native shard configuration,
saved-result replay and a FIFO replay that emits a finding before input EOF.
The last check proves incremental streaming on musl while masscan is still
alive. The script creates a temporary test image/container and cleans them up.
If Docker's default build network cannot reach the repositories, use
`scripts/check-alpine.sh --network host` for build-time downloads; runtime
networking remains disabled. The ZMap script accepts the same build option.

ZMap checks the actual preparation probes and command plans against its package
and compares native shard unions/intersections with expected IP/port products.
The dryrun sender prints packet descriptions, with explicit synthetic network
metadata to avoid ARP/discovery. Tests cover tiny/disjoint scopes, uneven and
empty shards, non-power-of-two port counts, boundary ports/seeds and native
rejection of too many workers. No scanner traffic leaves these containers.
Local HTTP/SSH fixtures cover both adapters, package/release/schema failures,
accepted findings, prompt delivery, cancellation and owned-worker cleanup.

Optional checks against a local compatible masscan binary run only `--echo`
and saved-result replay, without probes:

```sh
MASSCAN_TEST_BINARY=/path/to/masscan go test ./internal/integration \
  -run TestMasscanConfiguration -v
```

Use `-run TestMasscanListOutput` with the same variable to replay one
synthetic saved discovery and verify that stdout contains only native list
records. This catches interactive-output flag regressions without a live scan.

An explicitly enabled cloud smoke test creates **one temporary VM**, verifies
the actual Alpine release, root/password SSH and APK preparation, runs masscan
`--echo` without scan traffic, and confirms deletion using an independent
cleanup context. Run it only with separate user authorization:

```sh
# Supply VULTR_API_TOKEN through the environment.
MASSCANR_LIVE_TEST=1 go test ./internal/integration \
  -run TestVultrWorkerSmoke -count=1 -v -timeout=20m
```

Repository guidance requires asking the user before running the actual app.
The authorized live scan test prefix is `64.98.138.0/22` (canonical network
`64.98.136.0/22`). Keep live scan tests within that scope and delete test VMs.

The exact default TCP port set is in
[`internal/scan/config.go`](internal/scan/config.go), derived from
[Nmap 7.95 nmap-services](https://svn.nmap.org/nmap-releases/nmap-7.95/nmap-services)
by descending frequency, with numeric tie-breaking. No Nmap executable or data
download is needed at runtime.
