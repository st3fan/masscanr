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
- When an Intent's work is ready for user testing, start a separate Docker Compose deployment on localhost after obtaining the required approval to run the app. Pass the Intent number to `scripts/start.sh`, `scripts/stop.sh`, or `scripts/destroy.sh`. This selects host port `10000 +` the Intent number (for example, Intent 187 uses port 10187) and a distinct Compose project so its database and artifact volumes are isolated. Keep deployments for different Intents available concurrently. Use `scripts/start.sh --detach <intent>` to leave a healthy preview running and `scripts/status.sh` to list active previews. After code changes during PR review, refresh the same preview once active scans have finished and verify it is healthy before reporting it ready. When handling an authorized merge of the final PR for the Intent, run `scripts/cleanup-merged.sh <intent> <pr>` after GitHub confirms the merge to stop the local preview and retain its volumes; destroy retained data only as a separate operation.
- The terminal viewer is an HTTP client and needs no database, Vultr, SSH, or artifact access.

## Development and validation

- Use Go modules, `gofmt`, `go test ./...`, race tests, `go vet`, and `go build`. CI runs these checks plus offline Alpine masscan and ZMap compatibility checks.
- Prefer to start each Intent in a new Git worktree. Copy the main repository's ignored `.env` into the worktree when it needs credentials for the services in use; keep the copy ignored and out of commits and logs.
- Default tests use fixtures and local services without cloud credentials or scan traffic. PostgreSQL tests create and clean up unique databases they own. Live app runs, scans, and temporary cloud-worker checks require separate authorization and must respect the repository's authorized network and cleanup rules.
- Keep credentials in the environment or ignored `.env`, never in commits, logs, worker configuration, or example values.

## Engineering preferences and constraints

- Keep Go code idiomatic, provider orchestration separated from scanner execution, and changes small enough to review. Preserve explicit preflight and confirmation of scan scope.
- Prefer the two native executables and locally built Compose service image for current needs. Additional hosting platforms, registries, and binary packaging remain undecided until a concrete requirement warrants a decision.
