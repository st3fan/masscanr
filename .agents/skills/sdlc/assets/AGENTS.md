<!-- sdlc:managed:start -->
# Agent guidance

This repository uses **Stefan's Development Lifecycle**, the user-installed Codex skill `$sdlc`. Invoke `$sdlc bootstrap`, `$sdlc objective`, or `$sdlc intent` for lifecycle work; the detailed reusable rules are maintained in that skill, not copied into every project. **Bootstrap creates a reviewable PR for these guides, never a direct trunk change.**

- Treat natural-language requests or reports of new features, bugs, refactors, maintenance, or investigations as implicit `$sdlc intent` requests, even when the human does not name the skill. Continue an existing Intent for follow-up work. Informational questions, status requests, merges, releases, and deployments do not create a new Intent. An explicit request to bypass SDLC applies only to that task. Invoking SDLC does not authorize coding.
- Read `OBJECTIVE.md` and `TECHNOLOGY.md` (when present) before proposing significant work. The former defines enduring purpose and direction; the latter defines the project's chosen technology stack and engineering preferences. Use normal GitHub Intent Issues to propose changing it.
- Work **conversation-first, GitHub-backed**: discuss questions, drafts, decisions, and approvals with the human in Codex. Keep current intent, acceptance criteria, design decisions, risks, and implementation plan **synchronized in GitHub Issue bodies**. Do not require the human to edit or approve an Issue in the GitHub UI; direct Issue edits are valid and must be respected. Do not commit `intent.md`, `spec.md`, `plan.md`, or use GitHub Issue Artifacts.
- Use labels, native sub-issues/dependencies, GitHub Projects for status, and small, reviewable PRs or stacked PRs. Integrate approved safe changes into the default branch (trunk), independently of releases.
- The human approves significant decisions, start of implementation, PR merges, impactful operations, releases, and deployments separately. Never infer one approval from another.
- Do not silently change `TECHNOLOGY.md` or ignore its constraints when implementing an Issue; discuss meaningful stack changes with the human and propose them in a reviewed PR.
- If `VERSIONS.md` exists, follow it for release policy. Do not create Version Issues or assume a release accompanies a merge.
- Preserve and obey the project's stricter security, testing, operational, and repository boundaries below. If generic and project-specific instructions conflict, raise the conflict rather than silently weakening safeguards.
<!-- sdlc:managed:end -->

## Project-specific agent guidance

Add repository-specific build/test commands, architecture constraints, security boundaries, and operational safeguards here. This section is never overwritten by `$sdlc bootstrap`.
