# masscanr

Run masscan shards on temporary Vultr VPS workers, stream discoveries back to a
local list file, and watch each worker in a terminal UI. The controller is
written in Go and needs no root privileges. Workers use Debian's packaged
masscan and root/password SSH.

[V1 project description](https://github.com/st3fan/masscanr/issues/1) defines the
scope. Work is tracked in linked sub-issues and one `gh stack` PR stack.

## Build and configure

Requires Go 1.26 or newer and outbound HTTPS/SSH from the controller. No local
masscan, SSH executable, SSH key, or privileged account is required.

```sh
go build -o masscanr ./cmd/masscanr
cp .env.example .env
# Edit .env and set VULTR_API_KEY, or supply it through the environment.
```

`.env` is read from the current directory. Use `--env-file=/path/to/.env` for
another location. An existing `VULTR_API_KEY` environment variable takes
precedence. Simple unquoted, single-quoted, double-quoted, and `export` entries
are supported; no shell commands or interpolation are evaluated.

The key needs permissions to read OS/region availability and create, list,
inspect, and delete instances. The selected region must have capacity for the
chosen plan. The app fails before provisioning if the plan/image is unavailable.

## Run

Supply one or more company-owned IPv4 CIDRs. These examples use documentation
addresses; replace them with your authorized target ranges:

```sh
./masscanr scan 192.0.2.0/24 198.51.100.0/24
./masscanr scan --shards=4 --rate=500000 --region=atl \
  --instance-type=vc2-1c-1gb --output=inventory.list 192.0.2.0/24
./masscanr scan --ports=22,80,443,8000-8100 192.0.2.0/24
```

Scan options work before or after CIDRs, following `scan`. Use
`masscanr scan --help` for scan options and `masscanr --help` for commands.
Scanning requires the `scan` subcommand; a bare CIDR is an unknown command.

| Option | Default | Meaning |
| --- | --- | --- |
| `--shards` | `2` | Number of temporary workers |
| `--rate` | `500000` | Requested packets/second **per worker** |
| `--region` | `atl` | Vultr region (Atlanta) |
| `--instance-type` | `vc2-1c-1gb` | Vultr plan |
| `--ports`, `-p` | Nmap 7.95 top 100 TCP ports | Comma-separated ports, inclusive ranges, or mixed forms |
| `--output` | Timestamped `masscanr-*.list` | New local file; `-` sends list output to stdout |
| `--env-file` | `.env` | Local credentials file |
| `--timeout` | `24h` | Maximum scanning time; cleanup follows |
| `--cleanup-timeout` | `5m` | Separate deadline for worker deletion |
| `--no-tui` | `false` | Lifecycle logs instead of the live table |
| `--version` | | Print version and exit |

Port syntax accepts TCP numbers 0–65535, deduplicates overlaps and compacts
ranges. For example, `--ports=22,80,443,8000-8100` or `-p=1-65535`. IPv6, UDP,
service names and protocol-prefixed port syntax are outside V1.

CIDRs are masked and duplicate/contained ranges are removed. The normalized
target list is displayed before provisioning: for example, `192.0.2.42/24`
means `192.0.2.0/24`. All workers use the same targets, ports, randomization seed
and masscan version, with distinct native `--shards i/N` assignments.

Two default workers request a combined 1,000,000 packets/second. The UI shows
observed rates; actual throughput depends on the VPS and network.

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

These commands read the public catalog without loading `.env`, requiring an
API key or managing workers. Catalog pages are fetched automatically. Use
`masscanr help <command>` or `<command> --help` for command-specific usage.

The catalog includes plans that require block-storage boot. `scan` currently
requires an included local boot disk and rejects unsupported boot storage
before provisioning. Region availability is checked across plan families,
including local-disk High Frequency and High Performance plans.

## Progress and results

The TUI runs on stderr, with a row for each worker: instance/address, lifecycle,
scan progress, observed rate, ETA, discoveries and deletion status. Narrow
terminals use two lines per worker. Redirecting stderr automatically selects
plain lifecycle logs. Ctrl-C and SIGTERM stop scans and start cleanup.

Each worker is deleted as soon as its scanner finishes and its output is
collected, including masscan's response-wait phase. Its status becomes
`shutdown` after deletion is confirmed, while other shards continue scanning.
The row keeps its scan statistics and any scan failure.

Results stream independently into one local masscan list file. Files are
created with mode `0600`, and existing files are never overwritten. Example:

```text
#masscan
# masscanr run=... seed=... targets=... ports=... rate=... shards=...
# shard 1
open tcp 80 192.0.2.1 1700000000
# shard 2
open tcp 443 192.0.2.2 1700000001
# status complete
```

Comment lines record configuration, shard attribution, and completion status.
Discoveries use native list format: state, protocol, port, address, timestamp.
Response-wait completion and the remote exit status are checked before a shard
is complete. Results are checked against the requested addresses and ports.

A failed/cancelled run preserves collected results, writes `# status incomplete`
where output is still writable, and exits nonzero. No completion footer means
completion was not confirmed. A disk/write/sync error can prevent a footer or
durable data; stderr and exit status take precedence. No inventory database,
job/session history, resume, or automatic shard replacement is included in V1.

## Temporary workers and cleanup

Workers carry the **`masscanr` label** and a unique **`masscanr-run-<id>` tag**.
Cleanup requires both the app label and the current run tag, verified with a
fresh API lookup before deletion. Other infrastructure and workers from other
runs are never selected. No IP denylist or account-wide deletion is used.

The app installs masscan on Debian 13 x64, retrieves Vultr's generated root
password, and connects directly as root. The API key stays on the controller;
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
fixtures through the controller.

CI also installs masscan and exercises native configuration and saved-result
replay checks. These checks send no probes and require no cloud credentials.

Optional Debian masscan compatibility checks run only `--echo`, without probes:

```sh
MASSCAN_TEST_BINARY=/path/to/masscan go test ./internal/integration \
  -run TestDebianMasscanConfiguration -v
```

Use `-run TestDebianMasscanListOutput` with the same variable to replay one
synthetic saved discovery and verify that stdout contains only native list
records. This catches interactive-output flag regressions without a live scan.

An explicitly enabled cloud smoke test creates **one temporary VM**, verifies
Debian installation and root/password SSH, runs masscan `--echo` without scan
traffic, and confirms deletion using an independent cleanup context:

```sh
# Supply VULTR_API_KEY through the environment; tests do not load .env.
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
