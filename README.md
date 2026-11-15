# masscanr

## Service and terminal viewer

`masscanr-server` exposes Target and ScanPlan management, frozen preflight, start/stop, run history,
inspection, saved exports, and provider catalogs through a REST API backed by
PostgreSQL and DBOS. `masscanr-cli` offers only the HTTP-based `watch` command.
See the [REST API and operations guide](docs/rest-api.md) for database setup,
request examples, recovery semantics, and local test commands.
The [project objective](OBJECTIVE.md) and [technology policy](TECHNOLOGY.md)
explain the operating boundaries.

### Running locally with Docker Compose

Requires Docker Engine/Desktop with Docker Compose v2 or newer. Compose builds
the Go service and starts PostgreSQL 17, so no local Go or PostgreSQL installation
is needed. From the repository root, initialize a database password in the ignored
`.env` file (this command leaves an existing `.env` untouched):

```sh
test -e .env || (umask 077; printf 'MASSCANR_POSTGRES_PASSWORD=%s\n' "$(openssl rand -hex 24)" > .env)
docker compose up --build -d --wait
curl --fail-with-body http://127.0.0.1:8080/healthz
```

If `.env` already exists, add `MASSCANR_POSTGRES_PASSWORD` to it before starting;
[.env.example](.env.example) lists the available settings with placeholders.
Compose reads `.env` and passes configuration to the service as environment
variables. Set `VULTR_API_TOKEN` in `.env` or the launching shell to enable scan
starts; it can be empty for CRUD and preflight. Recreate the service after changing
runtime variables: `docker compose up -d --force-recreate masscanr`. Never commit
real tokens or passwords, and avoid printing resolved Compose configuration.

The API is available at `http://127.0.0.1:8080`; use the [Manage a scan](#manage-a-scan)
examples below. PostgreSQL is accessible only within the Compose network. Both
database state and `/data` scan artifacts use persistent named volumes. The image
runs the service as a non-root user, with no local scanner/root privileges needed.

```sh
# Check readiness and follow service logs.
docker compose ps
docker compose logs -f masscanr

# Copy retained artifacts to the host (also works with a stopped container).
mkdir -p ./masscanr-data
docker compose cp masscanr:/data/. ./masscanr-data/

# Stop the service and database while preserving data volumes.
docker compose down

# Rebuild after updating this checkout; existing volumes are reused.
docker compose up --build -d --wait
```

Compose waits for PostgreSQL readiness before starting the service and stops the
service before PostgreSQL. The service has up to 32 minutes to shut down and clean
up workers; avoid shortening that timeout during active scans. Copy artifacts
before `down` removes the container, or after starting it again. An explicit
`docker compose down --volumes` permanently deletes this Compose project's
database and artifacts; use it only for a deliberate reset after scans and cleanup
have settled. Changing the password in `.env` does not rotate an initialized
PostgreSQL volume's password—keep the original value when reusing that volume.

For hosting, keep this checkout, `.env`, and volumes on the Docker host. The
containers use `restart: unless-stopped`. Set `MASSCANR_PORT` to change the host
port. To publish beyond loopback, explicitly set `MASSCANR_BIND_ADDRESS` to the
host interface (or `0.0.0.0`); the service has no API authentication or TLS,
so use your existing private-network/reverse-proxy arrangement. Only one service instance may
use a database. Use a stable `docker compose -p <project-name>` on every command
for multiple isolated deployments; assign each a different host port. Compose
project names scope the containers, networks, and volumes.

### Run locally

The local PostgreSQL server accepts connections as your current operating-system
user through its Unix socket. Create a uniquely named development database:

```sh
masscanr_db="masscanr_dev_$(openssl rand -hex 8)"
createdb --host=/tmp "$masscanr_db"
export DBOS_SYSTEM_DATABASE_URL="host=/tmp dbname=$masscanr_db sslmode=disable"

go build -o masscanr-server ./cmd/masscanr-server
./masscanr-server --listen 127.0.0.1:8080 --data-dir ./masscanr-data
```

The service initializes its PostgreSQL and DBOS schemas automatically. Keep the
same database and artifact directory when restarting. Set `VULTR_API_TOKEN` in
the service's environment through your secret manager before starting scans;
CRUD and preflight work without it. For example, with 1Password:

```sh
VULTR_API_TOKEN='op://Company/Vultr/api-token' \
  op run -- ./masscanr-server --listen 127.0.0.1:8080 --data-dir ./masscanr-data
```

Use Ctrl-C or SIGTERM to stop the service and wait for worker cleanup. Only one
service process may use a database. This version has no users or authentication.

### Manage a scan

In another terminal, create a Target containing only networks you are authorized
to scan, then a reusable ScanPlan. These commands use `curl` and `jq`.

```sh
curl --fail-with-body http://127.0.0.1:8080/healthz

target_id=$(curl --fail-with-body -sS http://127.0.0.1:8080/v1/targets \
  -H 'Content-Type: application/vnd.api+json' \
  -d '{"data":{"type":"targets","attributes":{"name":"External network","entries":["YOUR_AUTHORIZED_IPV4_OR_CIDR"]}}}' \
  | jq -r .data.id)

scan_plan_id=$(curl --fail-with-body -sS http://127.0.0.1:8080/v1/scan-plans \
  -H 'Content-Type: application/vnd.api+json' \
  -d "{\"data\":{\"type\":\"scan-plans\",\"attributes\":{\"name\":\"Routine inventory\",\"target_id\":\"$target_id\"}}}" \
  | jq -r .data.id)

# Resolve and inspect the exact scope before starting.
curl --fail-with-body -sS -X POST \
  "http://127.0.0.1:8080/v1/scan-plans/$scan_plan_id/preflight" > preflight.json
jq . preflight.json
preflight_id=$(jq -r .data.id preflight.json)

# Start only after reviewing the returned targets and planned probe count.
# Keep this request key when retrying a lost response.
request_key=$(openssl rand -hex 16)
run_id=$(curl --fail-with-body -sS \
  "http://127.0.0.1:8080/v1/scan-plans/$scan_plan_id/start" \
  -H 'Content-Type: application/vnd.api+json' \
  -H 'Accept: application/vnd.api+json' \
  -H "Idempotency-Key: $request_key" \
  -d "{\"data\":{\"type\":\"runs\",\"attributes\":{\"preflight_id\":\"$preflight_id\",\"confirm\":true}}}" | jq -r .data.id)

curl --fail-with-body -sS "http://127.0.0.1:8080/v1/runs/$run_id" | jq .

# Request stop when needed; poll run status until cleanup settles.
curl --fail-with-body -sS -X POST \
  "http://127.0.0.1:8080/v1/runs/$run_id/stop" | jq .
```

Preflights expire after ten minutes. Only one scan can run at a time, including
worker cleanup. PostgreSQL stores queryable open-port results as the scan proceeds;
the `.scan` journal retains every observation as durable evidence. Completed scans
also retain CSV/JSONL exports. See the [full API guide](docs/rest-api.md) for
revisions, coverage semantics, recovery behavior, and test setup. The generated
OpenAPI contract is at `/openapi.json`; interactive offline Stoplight documentation
is at `/docs` on the same listener.

### Watch scans

Build the server and client from the same checkout. The viewer needs only
HTTP access to the service; it needs no PostgreSQL connection, Vultr credentials,
SSH access, or local scan artifacts.

```sh
go build -o masscanr-cli ./cmd/masscanr-cli

# Attach to one current or historical run.
./masscanr-cli watch "$run_id"

# Leave a window open: wait while idle and discover new runs automatically.
./masscanr-cli watch --all

# Choose a service and polling interval (100ms to 1m; default 2s).
./masscanr-cli watch --all --server http://127.0.0.1:8080 --interval 1s

# Plain status output, also selected automatically when input/output is not a TTY.
./masscanr-cli watch "$run_id" --plain
```

`--all` and a run ID are mutually exclusive. All-runs mode shows provisioning,
scanning, stopping, and cleanup. When nothing is active it displays **No active
scans — waiting for new runs** and stays running. It retains the last observed
settled summary while idle and replaces it when another run appears. It is a
current-activity view: a run that starts and settles entirely between polls can
be missed. The service still permits only one active run through cleanup.

| Key | Action |
| --- | --- |
| `q`, Ctrl-C, Ctrl-D | Detach; scans and the service continue |
| Tab, `1`, `2` | Switch Overview / Config |
| Up/down, PageUp/PageDown, Home | Scroll the selected view |
| `[` / `]` | Select the previous/next active run |
| `s` | Ask to stop the selected active run |
| Left/right or Tab, then Enter | Choose Cancel (default) or Stop in the confirmation |
| Escape, Ctrl-C, `q`, `n` in confirmation | Dismiss without stopping |

Stop confirmation shows and retains the exact run ID even if a newer run appears.
There is no stop-all action. A stop acknowledgement is only a request; keep
watching the separate scan and cleanup outcomes. Terminal views remain open
when the run settles. Plain single-run output exits once `settled:true` is seen;
plain `--all` stays running while idle and prints only changes. Exit zero describes
successful viewing/detachment, not successful scanning: inspect the displayed
scan and cleanup outcomes for failures. EOF or terminal loss detaches the TUI;
plain mode does not read stdin. SIGTERM/SIGHUP also detach.

After a successful initial connection, temporary connection failures and HTTP
408/429/5xx preserve the last snapshot, mark it stale, and retry with backoff capped
at 30 seconds. Stop is unavailable while the view is stale. Malformed or
incompatible responses and missing runs exit with an error. Startup failures fail
immediately before changing terminal settings. Run IDs, instance IDs, unknown
telemetry, last telemetry timestamps, cleanup errors, and server artifact paths
remain visible; the viewer never treats a lost connection as scan completion.

**A 404 from `/v1/runs?active=true` means the server URL is wrong or the
server lacks active-run discovery.** Running `go run ./cmd/masscanr-cli watch --all`
rebuilds only the client. Rebuild/restart the service too: for Compose use
`docker compose up --build -d --wait`; for native hosting, gracefully stop the old
process and restart the new `masscanr-server` with a compatible fresh database and artifact
directory. Ensure an old native service is not occupying Compose's host port.
The client requires versioned run snapshots from the server.

### Debugging scan starts

Use the Python 3 script to see each request stage, its payload, HTTP status,
server errors, and the scan/plan/run IDs:

```sh
# Inspect the created Target, ScanPlan, and preflight without starting a scan.
python3 scripts/start-scan.py --preflight-only --ports 22,80,443 64.98.138.0/22

# Create and start a scan, then observe status for early asynchronous errors.
python3 scripts/start-scan.py --ports 22,80,443 --wait 60 64.98.138.0/22
```

The example uses the authorized company test prefix; substitute only networks you
are authorized to scan. `--server` changes the API URL (default
`http://127.0.0.1:8080`). Run `python3 scripts/start-scan.py --help` for backend,
shard, rate, placement, and timeout options. It uses only Python's standard library.
The Vultr token must be configured on the service, not on this client.

Large scans require interactive approval of the displayed frozen plan, or an
explicit `--yes`. `--preflight-only` still saves a Target and ScanPlan. The script
does not automatically retry mutations, archive Targets, or stop scans. If a
start response is lost, use the printed exact start request and idempotency key;
do not blindly rerun the whole script and create another Target and ScanPlan.

A `202` means accepted, not successful scanner startup. The script polls for 30
seconds by default, prints worker/cleanup diagnostics, and exits nonzero for HTTP,
protocol, connection, or reported run errors. Exit zero means no error was seen in
the observation window; continue checking the printed status URL for completion.
Ctrl-C detaches the script and leaves any accepted scan running. For server detail:
`docker compose logs --tail=100 masscanr postgres`. Review logs before sharing them.

Offline script tests: `python3 -B -m unittest discover -s scripts -p 'test_start_scan.py'`.

### Inspect and download results

Use server run IDs to inspect the validated scan journal and download saved
exports. Inspection reports `complete` and `truncated`; unfinished, failed, and
cancelled runs may still show validated partial state. A partial result cannot
establish coverage of unobserved endpoints. Exports are available only for
complete scan results, including when cleanup failed. The server reads existing
CSV and JSONL files; it does not regenerate missing files.

```sh
curl --fail-with-body -sS "http://127.0.0.1:8080/v1/runs/$run_id/inspection" | jq .
curl --fail -o findings.csv "http://127.0.0.1:8080/v1/runs/$run_id/exports/csv"
curl --fail -o findings.jsonl "http://127.0.0.1:8080/v1/runs/$run_id/exports/jsonl"
```

A missing saved export returns an error and leaves the artifact directory intact.
Use the [scan document format](docs/scan-format.md) for the journal contract.

### Provider catalog

```sh
curl --fail-with-body -sS http://127.0.0.1:8080/v1/catalog/regions | jq .
curl --fail-with-body -sS 'http://127.0.0.1:8080/v1/catalog/instance-types?region=atl' | jq .
```

Catalog responses come from Vultr's public API. They are read-only and do not
need a Vultr token. Regional availability may change between catalog lookup and
scan start; preflight validates the frozen plan.

## Development and validation

```sh
gofmt -l cmd internal
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/masscanr-server ./cmd/masscanr-cli
python3 -B -m unittest discover -s scripts -p 'test_start_scan.py'
```

Default tests use local fixtures and no cloud credentials or scan traffic.
PostgreSQL tests create and clean up uniquely named databases when
`MASSCANR_TEST_POSTGRES` is set. CI also checks native masscan and ZMap
compatibility in network-disabled Alpine test containers. Running the actual
application, a live scan, or a cloud worker check requires separate authorization
and must follow [repository guidance](AGENTS.md).
