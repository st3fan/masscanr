# Technology

## Application and runtime

- Go 1.26 is the application language and module toolchain. Build separate `masscanr-server` and `masscanr-cli` executables. The client offers only `watch` and communicates exclusively through server HTTP APIs.
- The service uses Fiber v3 with Huma v2.40.0 for its incoming documented REST API, JSON:API 1.1 resource and error documents, PostgreSQL 17, DBOS workflows, and `pgx`. It serves generated OpenAPI 3.1 and a locally bundled Stoplight Elements 9.0.29 interface on the same listener. The watch terminal interface uses Bubble Tea. The CLI and outbound provider clients use Go's `net/http`. Keep the service and viewer compatible through the versioned HTTP API.
- The private browser dashboard is a React and TypeScript client-side application built with Vite. It uses shadcn/ui components, Tailwind CSS design tokens, TanStack Query and Table, and React Router. The Go JSON:API remains its source of truth. Approved Target management uses same-origin browser writes with server-side origin and Fetch Metadata checks. The browser has no token storage or independent authorization system; authentication is deferred to a separate intent.
- Scan workers are temporary Vultr VPS instances running Alpine Linux or DigitalOcean Droplets running Fedora 44. The controller executes masscan or ZMap over SSH and keeps provider credentials on the controller. DigitalOcean uses a recoverable per-run Ed25519 key registered with the account and removed after confirmed Droplet cleanup.

## Packaging and distribution

- Build native executables with `go build ./cmd/masscanr-server ./cmd/masscanr-cli`. The repository also has a multi-stage Dockerfile; Docker Compose builds the service image locally and runs it with PostgreSQL.
- Build the frontend with `cd web && npm ci && npm run build`, then embed the generated assets in a native server with `go build -tags webui ./cmd/masscanr-server`. The Dockerfile performs both build stages; Node.js is absent from the runtime image. Default Go builds remain API-only.
- Distribute source through the GitHub repository and document delivered versions with Git tags and GitHub release notes. Existing releases contain no downloadable binary assets. Publishing binaries or a registry image is undecided.

## Hosting and deployment

- Run the service locally or on a user-managed Docker host with Compose; native process hosting with a PostgreSQL database and artifact directory is also supported.
- Compose binds the API to loopback by default and persists PostgreSQL data and scan artifacts in named volumes. The documented API remains on loopback or a private listener and has no built-in API authentication or TLS; exposure beyond loopback requires an existing private-network or reverse-proxy arrangement. Use one service instance per database.
- Local service, Compose, CLI, and Vite proxy defaults use port 18080. Port 8080 is left for the developer's other applications; an explicit listen address or `MASSCANR_PORT` can override the default.
- Reviewable Intent work is deployed through Preview Controller using the committed `compose.yaml` and `./preview.sh --wait <intent> <pushed-commit-sha>`. The controller fetches the exact public commit, builds an isolated PostgreSQL and service stack, probes `/healthz`, and serves a stable per-Intent URL. Its management API is reachable only from Tailscale or the home network. The preview ingress must remain private because the API has no built-in authentication. The user has standing-authorized review preview deployments without a separate permission request. Agents give the user a clickable stable link immediately after submission, note that the page shows build progress, then verify the active deployment before reporting it ready. Wait until active scans finish before refreshing an existing preview.
- Local per-Intent Compose stacks remain available for development after obtaining approval to run the app. Pass the Intent number to `scripts/start.sh`, `scripts/stop.sh`, or `scripts/destroy.sh`. This selects host port `10000 +` the Intent number and an isolated Compose project. Use `scripts/start.sh --detach <intent>` to leave a healthy local stack running and `scripts/status.sh` to list them. After an authorized merge of the final PR, `scripts/cleanup-merged.sh <intent> <pr>` stops only the local stack and retains its volumes; destroy retained data separately.
- The terminal viewer is an HTTP client and needs no database, Vultr, SSH, or artifact access.

## Development and validation

- Use Go modules, `gofmt`, `go test ./...`, race tests, `go vet`, and `go build`. CI runs these checks plus offline Alpine masscan and ZMap compatibility checks.
- Prefer to start each Intent in a new Git worktree. Copy the main repository's ignored `.env` into the worktree when it needs credentials for the services in use; keep the copy ignored and out of commits and logs.
- Default tests use fixtures and local services without cloud credentials or scan traffic. PostgreSQL tests create and clean up unique databases they own. Local live app runs, scans, and temporary cloud-worker checks require separate authorization and must respect the repository's authorized network and cleanup rules. Review preview deployments have the standing authorization described above.
- Keep credentials in the environment or ignored `.env`, never in commits, logs, worker configuration, or example values.

## Engineering preferences and constraints

- Keep Go code idiomatic, provider orchestration separated from scanner execution, and changes small enough to review. Preserve explicit preflight and confirmation of scan scope.
- Prefer the two native executables and locally built Compose service image for current needs. Additional hosting platforms, registries, and binary packaging remain undecided until a concrete requirement warrants a decision.
