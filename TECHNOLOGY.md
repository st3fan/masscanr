# Technology

## Application and runtime

- Go 1.26 is the application language and module toolchain. Build separate `masscanr-server` and `masscanr-cli` executables. The client offers only `watch` and communicates exclusively through server HTTP APIs.
- The service uses Go HTTP handlers, PostgreSQL 17, DBOS workflows, and `pgx`. The watch terminal interface uses Bubble Tea. Keep the service and viewer compatible through the versioned HTTP API.
- Scan workers are temporary Vultr VPS instances running Alpine Linux. The controller executes masscan or ZMap over SSH and keeps provider credentials on the controller.

## Packaging and distribution

- Build native executables with `go build ./cmd/masscanr-server ./cmd/masscanr-cli`. The repository also has a multi-stage Dockerfile; Docker Compose builds the service image locally and runs it with PostgreSQL.
- Distribute source through the GitHub repository and document delivered versions with Git tags and GitHub release notes. Existing releases contain no downloadable binary assets. Publishing binaries or a registry image is undecided.

## Hosting and deployment

- Run the service locally or on a user-managed Docker host with Compose; native process hosting with a PostgreSQL database and artifact directory is also supported.
- Compose binds the API to loopback by default and persists PostgreSQL data and scan artifacts in named volumes. The service has no built-in API authentication or TLS; exposure beyond loopback requires an existing private-network or reverse-proxy arrangement. Use one service instance per database.
- The terminal viewer is an HTTP client and needs no database, Vultr, SSH, or artifact access.

## Development and validation

- Use Go modules, `gofmt`, `go test ./...`, race tests, `go vet`, and `go build`. CI runs these checks plus offline Alpine masscan and ZMap compatibility checks.
- Default tests use fixtures and local services without cloud credentials or scan traffic. PostgreSQL tests create and clean up unique databases they own. Live app runs, scans, and temporary cloud-worker checks require separate authorization and must respect the repository's authorized network and cleanup rules.
- Keep credentials in the environment or ignored `.env`, never in commits, logs, worker configuration, or example values.

## Engineering preferences and constraints

- Keep Go code idiomatic, provider orchestration separated from scanner execution, and changes small enough to review. Preserve explicit preflight and confirmation of scan scope.
- Prefer the two native executables and locally built Compose service image for current needs. Additional hosting platforms, registries, and binary packaging remain undecided until a concrete requirement warrants a decision.
