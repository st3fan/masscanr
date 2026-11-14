# AGENTS.md — masscanr

You are Codex working on `st3fan/masscanr`, a Go-based distributed port scanner. Read `HUMANS.md` for the human-facing workflow and `VERSIONS.md` for this project's release/version policy. The human owns intent, significant design and risk decisions, approval to begin implementation, and authorization to merge, deploy, tag, or release. You may maintain GitHub Issues, labels, native sub-issues/dependencies, Project items, Wiki pages, and draft PRs for authorized work. **An existing Issue is not permission to implement it.**

## Sources of truth

- **Issue body:** the current intent, scope, acceptance criteria, design, implementation plan, risks, decisions, and delivery links. Edit it when decisions are resolved. Comments are discussion, not a competing specification. Never use GitHub Issue Artifacts.
- **Intent Issue:** a parent Issue representing a substantial desired outcome. Native sub-issues describe independently verifiable deliverables; native dependencies show blockers. An Intent Issue is **not** a version, milestone, release train, or promise to ship everything together.
- **Global project context:** [Issue #1](https://github.com/st3fan/masscanr/issues/1) contains historical/project-level guidance. Use it for enduring project-wide principles. Decisions specific to one outcome belong in its Intent Issue. Existing version Issues remain historical; **do not create new version-planning Issues**.
- **Labels:** classification and review cues. **GitHub Project Status:** `Triage`, `Ready`, `In progress`, `In review`, `Done`; do not add status labels.
- **Wiki:** durable architecture, operations, and reusable product knowledge; link relevant pages from Issues. **PRs:** code diffs, links to Issues, CI/test evidence, review, and stack relationships.
- **Git:** code, tests, build/runtime configuration, existing product documentation, and minimal project operating guidance: `AGENTS.md`, `HUMANS.md`, and `VERSIONS.md`. Do not commit `intent.md`, `spec.md`, `plan.md`, PRDs, or SDLC planning documents. Temporary local scratch files may be used but must not be committed.

## Start and plan work

1. Use `gh` for GitHub operations. Read `AGENTS.md`, `HUMANS.md`, the target Issue body, parent/sub-issues, dependencies, labels, linked PRs, Project status, relevant Wiki pages, and the code/tests. Search for duplicate Issues before creating one.
2. When given an idea, help the human describe the problem, desired outcome, constraints, non-goals, and testable acceptance criteria. Create or update the Issue **body**, using sections as appropriate: `## Intent`, `## Acceptance criteria`, `## Design`, `## Implementation plan`, `## Risks and open questions`, `## Delivery`.
3. For a substantial outcome, create an **Intent Issue** with `scope:intent` and native sub-issues for separately verifiable deliverables. Small changes use a single Issue. Sub-issues are delivery outcomes; PRs are reviewable code slices and need not map 1:1.
4. Propose material architecture, security, scope, compatibility, cost, or operational choices and wait for approval. Do not implement until the Issue is `Ready` or the human explicitly authorizes the work. If the design materially changes while coding, update the Issue with the proposed change and request approval.
5. Classify Issues with exactly one `type:feature|bug|maintenance|research`, at least one `area:cli|api|ui|infra|security|docs`, and exactly one `priority:p0|p1|p2`. Add `scope:intent` for parent Intent Issues, `risk:security|breaking|data` for relevant risks, and `delivery:stack` for stacked work. Only apply labels that actually exist; ask for one-time setup if required labels or the Project are missing. Do not invent labels or silently omit classification.

## Trunk-based development

- **`main` is trunk** and the only normal integration and release source. Do not create version branches, version-gated integration branches, or release-driven parent Issues. Keep the trunk green and releasable whenever practical.
- Work in short-lived branches/worktrees created from current `main`. Integrate approved changes frequently through reviewed PRs. Prefer small, independently testable slices rather than waiting for an entire Intent Issue to finish.
- A small change normally produces one PR. For dependent slices use a **GitHub Stack** of roughly 2–5 coherent PRs, generally ~100–400 changed lines per layer where practical. The bottom PR is based on `main`; subsequent PRs depend on the preceding layer. Independent changes use separate branches/PRs, not artificial stacks.
- A stack is **not** a release unit. Under explicit human authorization, merge approved PRs bottom-up (or an approved contiguous lower portion) as soon as each slice is safe; unmerged upper layers remain open and must be restacked/synced as trunk advances. Confirm what GitHub will merge before triggering a partial-stack merge.
- Do not merge incomplete externally visible behavior into trunk unless it is safe and inert (e.g. behind a disabled feature flag or through backward-compatible incremental changes). Test flag-off behavior and do not enable behavior for users without authorization. Prefer revert or fix-forward promptly when trunk breaks.
- Use GitHub's supported `gh stack` extension when available. Verify `gh stack --help`. For non-interactive Codex sessions, prefer `gh stack submit --auto` and `gh stack view --json`; inspect current command help rather than assuming syntax. If stack tooling is unavailable, explain and use a supported GitHub stacked-PR alternative. Do not depend on an unverified `.agents/skills/gh-stack/SKILL.md`.

## Implement and verify

- Keep diffs narrow and idiomatic Go; preserve existing behavior and add appropriate tests with each slice. Run `gofmt` on changed Go files, `go test ./...`, and relevant checks. When relevant, run offline script tests: `python3 -B -m unittest discover -s scripts -p 'test_start_scan.py'`.
- Review correctness and security before submitting, including scan scope, credentials, rate limits, failure/recovery paths, persistence, stop semantics, and cloud cleanup. Report real test commands and their actual results; never invent passing checks.
- Open/update draft PRs with the intent/sub-issue links, rationale, implementation summary, stack position/base, tests, risks, and rollout/rollback considerations. Keep Project status and Issue `## Delivery` links accurate. Close sub-issues only after merged work satisfies their criteria. Close an Intent Issue after all its accepted outcomes are complete, **not** when a release is published.
- **Never merge or deploy without explicit human approval.** Never bypass review, CI, or branch protections. Approval of the design is not approval to merge. Approval to merge is **not** approval to tag, publish a release, or deploy.

## Releases and versioning (independent of feature work)

- **`VERSIONS.md` is the authoritative, project-specific versioning and release policy.** Read it before proposing a version, tag, or GitHub Release. This `AGENTS.md` intentionally does not prescribe numbering, cadence, or compatibility semantics; other projects may choose different policies. If `VERSIONS.md` is absent or ambiguous, ask the human to define the policy rather than inventing one.
- A release is a **separate human-triggered decision**, made from a tested, known-good commit already on `main`. Do not schedule or create releases merely because an Intent Issue or PR stack is completed.
- When asked to prepare a release: read `VERSIONS.md`, inspect trunk health, CI, tests, merged PRs since the last release, breaking changes, migration requirements, flags, and open regressions. Follow the project's policy to propose an identifier, exact target commit, concise notes, and any operational actions. Ask for release authorization before creating tags or publishing a GitHub Release; deployments require explicit permission as well.
- Do not silently cherry-pick features into a release or create long-lived release branches. A release includes the behavior present at the selected trunk commit; if something must not ship, use a safe feature flag, defer its merge, or raise the concern to the human.
- Do not modify `VERSIONS.md`, change a published tag, or alter the versioning convention without explicit human authorization.

## masscanr safety rules (retained)

- Work only inside the `masscanr` repository, not its parent directory. Scan only networks the human authorizes; never silently broaden scope.
- Ask permission before **running the actual application**, starting scans, or provisioning workers. Offline builds/tests are separate. An approval covers the specified run and necessary retries, not unrelated live activity.
- The approved example/test IPv4 prefix is `64.98.138.0/22` (canonical `64.98.136.0/22`). Use fixtures/offline tests for other targets unless separately authorized.
- Do not leave test VMs running. Delete only instances with the `masscanr` label and the **current run's unique ownership tag**. Never touch unrelated infrastructure, including the existing VM at `144.202.29.87`.
- Keep secrets in ignored `.env`, environment, or a secrets manager, never in commits, issue/PR bodies, logs, worker configuration, or `.env.example`. The example contains placeholders only.
- For local PostgreSQL use the current operating-system user's Unix socket, create a unique dev/test database such as `masscanr_test_<random-id>`, and clean up **only** the database created by the task. Never reuse/drop unrelated databases or print connection secrets.
- Current architecture uses PostgreSQL/DBOS and Vultr workers. Changes to scan exclusions, worker lifecycles, stop behavior, persistence, or recovery need explicit regression and safety checks.

## Definition of done

The agreed Issue acceptance criteria are met; the relevant PRs were reviewed, tested, and **human-authorized** for merge; trunk remains healthy; sub-issue/parent status and delivery links are current; operational changes are reflected in the Wiki if appropriate. Follow-ups remain in GitHub Issues, not untracked files. **A release is never part of the definition of done for an Intent Issue.**