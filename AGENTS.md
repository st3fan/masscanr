# masscanr

## Project and workspace

Build a distributed port scanner in Go to inventory networks owned by the
user's company. Keep scan scope explicit and limited to authorized networks.

The project root is `st3fan/masscanr`, not the parent `st3fan` directory. Run
project commands here and keep changes inside this repository. The parent
contains unrelated projects.

## GitHub workflow

- V1's authoritative project description is
  https://github.com/st3fan/masscanr/issues/1. Record material design decisions
  there; do not silently expand its scope or choose major components.
- Use the GitHub CLI (`gh`) for issues, PRs, reviews and API calls (`gh api`).
- Use the installed `gh-stack` skill and `gh stack` for stacked branches and
  PRs. Read [.agents/skills/gh-stack/SKILL.md](.agents/skills/gh-stack/SKILL.md)
  before stack operations and follow its non-interactive command guidance.
- Create or identify an issue before implementation. Link iteration sub-issues
  under the guidance issue, with clear scope, acceptance criteria, dependencies
  and validation expectations.
- Deliver small, manageable PRs linked to their issues. Use a single V1 PR stack
  with explicit dependencies and review order. Keep descriptions consistent
  with final scope and include relevant validation results.
- Do not merge PRs or publish releases without user authorization.

## Implementation and validation

- Write idiomatic Go and keep changes focused on the issue being addressed.
  Run `gofmt`, `go test ./...`, and checks appropriate to the change.
- Ask before running the actual app. Existing user authorization in the current
  session remains valid for the approved run and necessary retries.
- Tests may target the authorized IPv4 prefix `64.98.138.0/22` (masked:
  `64.98.136.0/22`). Use fixtures/offline checks for other test targets.
- Do not leave test VMs running. Cleanup may delete only instances with the
  `masscanr` label and the current run's unique ownership tag. Never modify
  unrelated infrastructure, including the existing VM at `144.202.29.87`.
- Keep credentials in ignored `.env` or the environment, out of commits, logs,
  worker configuration and `.env.example`. The example contains placeholders.
