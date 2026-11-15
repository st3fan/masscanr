# Browser dashboard development

The dashboard is a read-only React client built from `web/`. The Go service owns scope,
scan execution, persistence, API validation, and access boundaries. The current listener
must stay on loopback or a private network because the server has no authentication.
The dashboard never stores API credentials or tokens. Adding authentication requires a
separate Intent.

## Build and run

```sh
cd web
npm ci
npm run build
cd ..
go build -tags webui -o masscanr-server ./cmd/masscanr-server
```

`npm run build` writes hashed assets to `web/dist` and copies them to
`internal/webui/dist` for Go embedding. Default Go builds omit the dashboard and
keep the API usable. The Dockerfile performs the Node build before the Go build;
Node is absent from the final image. The Go server serves `/` and client routes,
while `/v1`, `/healthz`, `/openapi.json`, `/docs`, and missing assets keep their
own behavior. The Vite dev server proxies these API paths to the local Go server.
The default Go and Compose listener is `127.0.0.1:18080`; the Vite proxy points
there. The browser app itself uses Vite's separate development port.
For a complete foreground local stack, use `./scripts/start.sh` from the repo
root. `./scripts/stop.sh` retains the dedicated project's volumes;
`./scripts/stop.sh --prune` and `./scripts/destroy.sh` remove that project's
containers, volumes, and locally built images. No other Compose project is
targeted by the default script project name.

To work on the frontend against a separately started private Go service, run
`npm run dev` from `web/` and open the local Vite address. Start the Go service
only after following the repository's application-run approval rule. The browser
must use the same origin as its API in production.

## Conventions

- Put route views in `web/src/pages`, shared application components in
  `web/src/components`, and shadcn registry components in `web/src/components/ui`.
  Reuse semantic tokens in `web/src/index.css` for light and dark themes.
- Put HTTP parsing, JSON:API errors, resource validation, and URL construction in
  `web/src/api/client.ts`. Pages use TanStack Query for server state and polling.
  Do not add local mock data to production views or duplicate authorization logic.
- Use `relatedResources` when a JSON:API response includes related records;
  follow relationship identifiers without assuming the records are present.
- Keep table pagination and supported sort values on the server. The UI passes
  `links.next` cursors to the API and labels first-page counts as such.
- Render loading, empty, error, and partial-coverage states explicitly. A missing
  positive observation never means a port is closed. The `/patterns` route shows
  shared visual states and component variants.
- Route code is lazy loaded. Keep dependencies and components local; no CDN assets
  are required. Theme preference may use localStorage, but credentials may not.

Regenerate TypeScript API types after changing Huma routes or response schemas:

```sh
cd web
npm run generate:api
```

This command builds the registered OpenAPI document without opening a network
listener. Commit the generated `src/api/schema.ts`; its intermediate JSON is
ignored. Review the actual JSON:API envelope in `client.ts` because generated
OpenAPI types do not validate wire data at runtime.

## Checks

```sh
cd web
npm run typecheck
npm run lint
npm run format:check
npm run test
npm run build
npm run e2e
```

`npm run e2e` starts a local Vite preview and uses Playwright route fixtures; it
never contacts a scanner, cloud provider, or a live Go API. Obtain the required
application-run approval before invoking it. Playwright writes light/dark and
responsive screenshots under `web/test-results`; inspect them visually. The
normal CI frontend job runs the other checks and the embedded Go build. It does
not start the dashboard. The repository Go suite and disposable PostgreSQL tests
cover the real API contract.

Current API limits: runs expose creation time and lifecycle phase, but no separate
start or finish timestamps. Targets have positive selectors only, without exclusions.
Global collection counts are unavailable, so overview counts label their first-page
limit. Add backend capabilities through a separate scoped Intent when needed.
