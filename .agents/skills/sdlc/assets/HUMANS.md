<!-- sdlc:managed:start -->
# Working with Stefan's Development Lifecycle

This project uses the **`$sdlc` Codex skill**. Install it once for your user account to use it across repositories. Run Codex from the target project directory and invoke one of the following:

| What you want | What to type in Codex |
| --- | --- |
| Discuss technology choices and open a Bootstrap PR for the project guides | `$sdlc bootstrap` |
| Define or change project purpose and direction | `$sdlc objective` |
| Start a feature, fix, investigation, or other stream of work | `$sdlc intent` |

These are instructions to a single skill, **not shell commands** or three separate installed skills. You can add detail after the action, for example:

> `$sdlc intent Improve scan preflight reporting. Help me clarify acceptance criteria and create an Issue; don't implement yet.`

You can also describe new work naturally, such as “Let's work on target exclusions” or “There is a bug in scan preflight.” Codex treats those as `$sdlc intent` requests without requiring the skill name. Follow-ups continue the existing Intent. Informational questions, status requests, merges, releases, and deployments do not create a new one. You can explicitly opt out of SDLC for a particular task.

**Typical first-time setup:** `$sdlc bootstrap` → review and merge the Bootstrap PR → `$sdlc objective` → `$sdlc intent`. Bootstrap asks relevant questions about the stack, **packaging and distribution**, and **hosting/deployment when applicable**, plus preferences and tradeoffs. It accepts **Not applicable** and **Undecided** instead of assuming containers or Kubernetes. Codex then creates or updates `AGENTS.md`, `HUMANS.md`, and `TECHNOLOGY.md` **on a temporary branch and opens a PR**, preserving custom sections and existing technology choices unless you approve changes; it never writes directly to trunk or merges for you. Re-run it after updating the installed skill to propose managed-section updates in another PR. When nothing has changed, no PR is needed.

**Conversation-first, GitHub-backed:** review drafts, answer questions, refine ideas, and approve direction **inside Codex**. Codex summarizes the proposal in the conversation and updates the GitHub Issue body as decisions are made. The Issue is the durable record, **not required reading or manual editing homework**. You may edit an Issue directly if you prefer; Codex must pick up those edits when it continues.

**Human decisions:** describe the desired outcome, approve significant scope/design choices, authorize implementation, review and explicitly approve PR merges. Starting a stream of work does **not** automatically authorize coding, merging, running live operations, releasing, or deploying.

**Where information lives:**
- `OBJECTIVE.md` in Git describes *why the project exists* and its enduring direction.
- `TECHNOLOGY.md` in Git records the chosen stack, technology preferences, reasoning, and open constraints; bootstrap discusses these choices with you first.
- GitHub Issue **bodies** contain the current intent, plans, decisions, and acceptance criteria; native sub-issues break up outcomes.
- GitHub labels classify Issues, Projects track status, PRs/stacks carry code review, and the Wiki holds reusable architectural knowledge.
- `AGENTS.md` contains project-specific agent safeguards; `VERSIONS.md` (if present) defines the project's independent release rules.

**Resuming:** give Codex the Issue number: `$sdlc intent Continue #123: read the Issue body, sub-issues, linked PRs and current status before proposing a next step.`

**Releasing:** ask Codex to inspect `VERSIONS.md` and the tested trunk commit, propose a tag and release notes, and wait for your separate approval. Merge approval is never release approval.
<!-- sdlc:managed:end -->

## Project-specific human notes

Add any local onboarding commands or project-specific review instructions here. This section is never overwritten by `$sdlc bootstrap`.
