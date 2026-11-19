---
name: sdlc
description: Stefan's Development Lifecycle for Codex. Use for project bootstrap or objective work and for new features, bug reports, refactors, maintenance tasks, or investigations, even when the developer does not name the skill. Route new work to $sdlc intent. Explicit actions are $sdlc bootstrap, $sdlc objective, and $sdlc intent.
---

# Stefan's Development Lifecycle

A GitHub-native, intent-driven workflow for a solo developer working in one repository or monorepo. The human owns project direction and high-impact decisions. Codex assists with discovery, planning, GitHub organization, implementation, testing, and drafting PRs. Releases are independent of landing changes on trunk.

## Dispatch

Interpret the word after `$sdlc` as the **action argument** (not a separate installed skill or CLI command):

- `bootstrap`: **discuss and approve technology choices**, then open a Bootstrap PR that creates or refreshes `AGENTS.md`, `HUMANS.md`, and project-specific `TECHNOLOGY.md` (when missing or deliberately revised). File creation alone is not completion.
- `objective`: clarify or revise enduring project direction and propose `OBJECTIVE.md`.
- `intent`: create/refine a GitHub Intent Issue, with optional sub-issues and proposed implementation slices.

If the developer names an action, follow it. Otherwise, treat a natural-language request or report of new feature work, a bug, a refactor, maintenance, or an investigation as `intent`. Continue an existing Intent for follow-up work. Informational questions, status requests, merges, releases, and deployments do not start a new Intent. Honor an explicit request to bypass SDLC for a particular task. Ask which action the developer wants only when the action remains genuinely unclear after checking context. Invoking the skill does not authorize implementation; do not silently start coding. Follow the action-specific reference files below.

## Global rules

1. Read existing repository guidance first: `AGENTS.md`, `HUMANS.md`, `OBJECTIVE.md`, `TECHNOLOGY.md`, and `VERSIONS.md` when present; preserve stricter project-specific security, testing, and operational requirements. Never treat the skill as authority to override those.
2. **Conversation-first, GitHub-backed:** use the Codex conversation to explore, question, refine, and approve plans. The human must not have to leave Codex to review or edit an Issue. Present meaningful drafts, tradeoffs, questions, and an approval summary **in the conversation**. Keep the **GitHub Issue body** synchronized as the durable, authoritative record of confirmed decisions, intent, acceptance criteria, and plan. Issue comments are discussion, not a competing specification. Do not create committed `intent.md`, `spec.md`, `plan.md`, or GitHub Issue Artifacts. If the human directly edits an Issue, re-read it and incorporate those edits rather than overwriting them.
3. `OBJECTIVE.md` describes *why this project exists*; `TECHNOLOGY.md` records the **project-specific technology stack, engineering preferences, rationale, and constraints**. Both are durable guidance in Git, not feature specifications, work plans, or release calendars. Consult `TECHNOLOGY.md` before proposing implementations; material technology changes require conversation and human approval.
4. Use GitHub labels for type, area, priority, scope and risk; native sub-issues for distinct outcomes; Projects for status; Wiki for durable technical knowledge; PRs and CI for code review and delivery. Do not create a special authoritative Issue #1 or Version Issues.
5. `main` (or the repository's configured default branch) is trunk. Use short-lived branches and small, reviewable PRs. For dependent slices, prefer stacks of approximately 2–5 PRs. A stack is not a release boundary. Merge safe, approved slices independently of any release.
6. Human approval is required for significant scope, architecture, security, compatibility, cost or operational decisions; starting implementation; merging PRs; running impactful/live operations; tagging, publishing releases; and deploying. These are separate approvals. Never treat an Issue as implementation authorization.
7. Project-specific release/version policy belongs in `VERSIONS.md`, if present. Never assume numbering or auto-release after a merge. If no policy exists, ask for one when preparing a release.
8. Work inside the target repository. Inspect current modifications before editing. Never clobber project-specific instructions, change unrelated files, or imply a write succeeded without verifying it.
9. The design assumes one developer and one repository or monorepo. For teams or multiple independent repos, surface ownership and coordination questions instead of inventing policy.

## Actions

### `$sdlc bootstrap`

Read [references/bootstrap.md](references/bootstrap.md) and [references/technology.md](references/technology.md). First identify the project type, discover the existing stack, and **ask focused questions in Codex** about packaging, distribution, hosting, and deployment *when relevant*, as well as other technology preferences. Prefer the simplest current fit; allow **Not applicable** and **Undecided** instead of forcing infrastructure decisions. Summarize choices and rationale for human approval. For a new or revised technology policy, supply the approved `TECHNOLOGY.md` draft using `--technology-file`. Preview and reconcile conflicts, then use [scripts/bootstrap_pr.py](scripts/bootstrap_pr.py) to prepare an isolated branch, apply the templates and approved technology policy, stage **only `AGENTS.md`, `HUMANS.md`, and `TECHNOLOGY.md`**, open a **Bootstrap** pull request, and return its verified URL. [scripts/bootstrap.py](scripts/bootstrap.py) is only the internal file updater, **not** the full action. Never modify the current checkout or directly write to trunk. Never merge the PR without human approval. If already current, report no diff; if GitHub access is blocked, report the blocker rather than claiming completion.

### `$sdlc objective`

Read [references/objective.md](references/objective.md). Ask focused questions and **review/refine the proposed direction in the Codex conversation**, while maintaining the draft in a normal GitHub Intent Issue body. Summarize the full proposal and request approval **in conversation**. Upon approval propose the durable `OBJECTIVE.md` in a PR. Never create or privilege Issue #1. No implementation without approval.

### `$sdlc intent`

Read [references/intent.md](references/intent.md). Check alignment with `OBJECTIVE.md`, discover duplicate work, then **discuss intent, design, and tradeoffs conversationally** while updating the Issue body as decisions are confirmed. Classify with labels, propose native sub-issues/dependencies for complex outcomes, and present a conversational summary and approval gate before coding.

## Expected reporting

End each action with a concise report: what changed (or was proposed), relevant Issue/PR URLs, unresolved decisions, and the next human approval needed. Do not merge, release, or deploy merely to complete the action.
