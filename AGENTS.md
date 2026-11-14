# masscanr

## Project and workspace

Build a distributed port scanner in Go to inventory networks owned by the
user's company. Keep scan scope explicit and limited to authorized networks.

The project root is `st3fan/masscanr`, not the parent `st3fan` directory. Run
project commands here and keep changes inside this repository. The parent
contains unrelated projects.

## GitHub workflow

- [Issue #1](https://github.com/st3fan/masscanr/issues/1) records the historical
  V1 scope and decisions. For new work, use a normal GitHub Intent Issue and
  keep its body current with confirmed scope, design decisions, acceptance
  criteria, and plan. Use native sub-issues for distinct outcomes.
- Use the GitHub CLI (`gh`) for issues, PRs, reviews and API calls (`gh api`).
- For dependent slices, use the installed `gh-stack` skill and `gh stack` for
  stacked branches and PRs. Read its command guidance before stack operations.
- Create or identify an Intent Issue before implementation. Obtain separate
  approval to start coding. Give sub-issues clear scope, acceptance criteria,
  dependencies, and validation expectations.
- Deliver small, manageable PRs linked to their issues. Keep dependent stacks
  to approximately 2–5 PRs, with explicit dependencies and review order. Keep
  descriptions consistent with final scope and include validation results.
- Merge PRs, create tags, and publish GitHub releases only with separate user
  authorization. Releases do not automatically accompany merges.

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

## Local PostgreSQL

- Development and tests may create databases as the current operating-system
  user through the local Unix socket; no password or credentials are needed.
- Give every development/test database a unique name (for example,
  `masscanr_test_<random-id>`). Never reuse or drop an unrelated database.
- Tests must clean up only the database they created. Keep connection URLs in
  the environment and out of logs and commits.

<!-- sdlc:managed:start -->
# Agent guidance

This repository uses **Stefan's Development Lifecycle**, the user-installed Codex skill `$sdlc`. Invoke `$sdlc bootstrap`, `$sdlc objective`, or `$sdlc intent` for lifecycle work; the detailed reusable rules are maintained in that skill, not copied into every project. **Bootstrap creates a reviewable PR for these guides, never a direct trunk change.**

- Read `OBJECTIVE.md` and `TECHNOLOGY.md` (when present) before proposing significant work. The former defines enduring purpose and direction; the latter defines the project's chosen technology stack and engineering preferences. Use normal GitHub Intent Issues to propose changing it.
- Work **conversation-first, GitHub-backed**: discuss questions, drafts, decisions, and approvals with the human in Codex. Keep current intent, acceptance criteria, design decisions, risks, and implementation plan **synchronized in GitHub Issue bodies**. Do not require the human to edit or approve an Issue in the GitHub UI; direct Issue edits are valid and must be respected. Do not commit `intent.md`, `spec.md`, `plan.md`, or use GitHub Issue Artifacts.
- Use labels, native sub-issues/dependencies, GitHub Projects for status, and small, reviewable PRs or stacked PRs. Integrate approved safe changes into the default branch (trunk), independently of releases.
- The human approves significant decisions, start of implementation, PR merges, impactful operations, releases, and deployments separately. Never infer one approval from another.
- Do not silently change `TECHNOLOGY.md` or ignore its constraints when implementing an Issue; discuss meaningful stack changes with the human and propose them in a reviewed PR.
- If `VERSIONS.md` exists, follow it for release policy. Do not create Version Issues or assume a release accompanies a merge.
- Preserve and obey the project's stricter security, testing, operational, and repository boundaries below. If generic and project-specific instructions conflict, raise the conflict rather than silently weakening safeguards.
<!-- sdlc:managed:end -->
