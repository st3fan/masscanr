# HUMANS.md — Working on masscanr

This is the human-facing guide for developing `masscanr` with **Codex CLI/TUI**, GitHub Issues, sub-issues, labels, Projects, pull requests, and GitHub Stacks.

**You describe the outcome and make the important decisions. Codex does the planning, code, tests, and GitHub bookkeeping.** Work lands continuously on `main` after your review; a release happens separately when you decide the time is right.

GitHub stores the planning record. The **Issue body** holds intent, design, implementation plan, decisions, and acceptance criteria; comments are discussion. Native sub-issues organize outcomes, labels classify them, a GitHub Project tracks progress, PRs hold code review, and the Wiki holds durable architecture. Do not create `intent.md`, `spec.md`, `plan.md`, or GitHub Issue Artifacts. Git holds code, tests, product documentation/configuration, and four durable guides: `AGENTS.md`, `HUMANS.md`, `OBJECTIVE.md`, and `VERSIONS.md`. `OBJECTIVE.md` defines the project's purpose and direction; `VERSIONS.md` defines its release policy. Neither is an intent, spec, or implementation plan.

## How to bootstrap a project

**No special Issue #1 or permanent “project guidance Issue.”** Use a normal Intent Issue, with whatever number GitHub assigns, to establish the project objective.

1. Tell Codex the project idea: who it is for, which problem it should solve, the desired outcomes, rough direction, constraints/non-goals, and guiding principles.
2. Codex creates/refines a normal Intent Issue with that material **in the Issue body**. You review and resolve major questions before authorizing implementation.
3. Codex prepares a small PR containing `OBJECTIVE.md`: a concise, durable statement of purpose and direction, **not** a specification, roadmap, or version plan.
4. You review and explicitly approve the PR merge. The bootstrap Intent Issue can close; it does not become a permanent authority. Future work is checked against `OBJECTIVE.md`.
5. Codex adapts `AGENTS.md`, `HUMANS.md`, and `VERSIONS.md` for the project. These are guidance/policy files, not implementation plans.

**Bootstrap prompt:**

> Help me establish a new project's objective. Ask about the problem, audience, desired outcomes, direction, non-goals, and guiding principles. Create a normal GitHub Intent Issue containing our decisions in its body. When I approve the objective, propose `OBJECTIVE.md` in a PR. Do not start implementing features or merge the PR without my approval.

For an established project like `masscanr`, adopting `OBJECTIVE.md` is itself an ordinary Intent Issue and PR. Older Issues can be consulted as history but have no special authority.

**Changing project direction:** use the same Intent process to propose material changes to `OBJECTIVE.md`; do not silently edit it while completing unrelated feature work.

## How to start new work

**Recommended: start in Codex.**

1. In a checkout of `masscanr`, run `codex` and describe the problem, desired outcome, and any constraints you already know. Codex reads `OBJECTIVE.md` and checks that the proposed work supports the project's direction.
2. Ask Codex to search for an existing Issue, discuss important gaps, and **create or refine an Issue body**. Tell it not to write code yet.
3. Review the proposed intent, scope, acceptance criteria, design, and risks. Approve material decisions and tell Codex when implementation may begin (or mark the Issue `Ready`).
4. Codex creates sub-issues for substantial work, classifies them with labels, and prepares small, testable PRs. Related PRs can form a GitHub Stack when they depend on each other.
5. Review diffs and checks. You authorize merges. Safe approved slices can land on **`main`** without waiting for the entire feature or intent to finish.
6. When you want a release, tell Codex to read `VERSIONS.md` and propose a release from a known-good `main` commit. Tagging, publishing, and deploying require your separate authorization.

**Starting prompt:**

> I want to improve masscanr's preflight error messages so users can diagnose invalid input. Help me clarify intent, scope, constraints, and acceptance criteria. Search for existing Issues first. Create or update a GitHub Issue with the current design, plan, and open questions in its **body**. Apply our labels and create sub-issues if the outcome needs them. Do not implement anything until I approve.

**After approving the Issue:**

> I approve the design in #123 and authorize implementation. Use `AGENTS.md`. Break it into small, safe, reviewable PRs, with a GitHub Stack only for dependencies. Run checks and open draft PRs. Do not merge, start live scans, deploy, tag, or release.

**To land approved work:**

> I have reviewed PR #456, the checks, and its risks. You may merge this PR into `main`. If it belongs to a stack, merge only the approved bottom portion and preserve/restack the remaining PRs. Do not release or deploy.

**Alternatively, start in GitHub:** create an [Issue](https://github.com/st3fan/masscanr/issues/new) with a descriptive title and only enough text to begin:

```markdown
## Intent
**Problem:** What is wrong or missing?
**Desired outcome:** What should be possible?
**Constraints / non-goals:** What must not change?

## Acceptance criteria
- [ ] What would demonstrate success?

## Open questions
- What still needs a decision?
```

Then tell Codex: *“Refine `st3fan/masscanr#123` into a plan in its body. Ask me about significant decisions. Do not implement yet.”*

## Intent Issues replace Version Issues

The previous `masscanr` **Version Issue** was already functioning as an **Intent Issue**: it described the outcome, decisions, and sub-issues. Keep that useful structure, but **remove version numbers and release commitments from planning**.

- **Small bug or maintenance task:** one Issue, usually one PR.
- **Substantial feature or outcome:** one parent **Intent Issue** (`scope:intent`), native sub-issues for independently verifiable outcomes, one or more PRs/stacks.
- **Unknown problem or feasibility:** one `type:research` Issue with findings and a recommendation in its body; implementation requires approval.

A sub-issue is a separately verifiable **outcome**. A PR is a reviewable **code slice**. A PR stack is a sequence of **dependent** slices. These structures do not have to match one-to-one. Keep independent code changes unstacked.

`masscanr`'s older Issues (including #1 and V1–V10) remain historical records, **not** the authority for purpose or direction; that role belongs to `OBJECTIVE.md`. Existing releases remain historical facts; do not rewrite them. **Do not create new Version Issues** or require an Issue to belong to a version.

## Trunk-based development

`main` is trunk and is normally in a healthy, releasable state. The development cycle is:

<code>Intent Issue → short-lived branch(es) → reviewed PR(s) → main → optional later release</code>

Use short-lived branches, small PRs, and frequent integration rather than large release branches. For a dependent change, use a GitHub Stack of typically **2–5 PRs**, aiming for **~100–400 changed lines per PR** when practical. A stack begins from `main`, and safe approved lower layers can merge first. GitHub can rebase/re-target the remaining stack; Codex must verify the resulting branch/PR state. A stack is **not** a release.

A partial feature may be merged only if it is safe in the running product—for example, backward-compatible plumbing or behavior behind a disabled feature flag. Never leave trunk broken or expose unfinished behavior accidentally. When trunk regresses, prioritize fixing or reverting it.

**The merge and release decisions are separate:** approving a PR merge never automatically approves a release. An Intent Issue can be closed when its outcome is delivered on trunk even if no new release exists.

## Labels and Project status

Codex proposes and applies labels; you can correct them when triaging. The suggested minimal taxonomy:

| Dimension | Labels | Rule |
| --- | --- | --- |
| Type | `type:feature`, `type:bug`, `type:maintenance`, `type:research` | Exactly one |
| Area | `area:cli`, `area:api`, `area:ui`, `area:infra`, `area:security`, `area:docs` | One or more |
| Priority | `priority:p0`, `priority:p1`, `priority:p2` | Exactly one |
| Scope | `scope:intent` | Parent outcome Issues only |
| Risk | `risk:security`, `risk:breaking`, `risk:data` | When relevant |
| Delivery | `delivery:stack` | When relevant |

The GitHub Project tracks **Status**, not labels: `Triage → Ready → In progress → In review → Done`. A newly created Issue is not authorization to code. Use native dependencies for blockers. Codex should propose one-time label/Project setup if they are not configured.

## Your decisions

- **Intent:** Is this the desired outcome? Are constraints and acceptance criteria correct?
- **Design:** Approve meaningful architecture, security, compatibility, cost, and operations choices.
- **Implementation:** Mark `Ready` or explicitly authorize starting.
- **Merge:** Review PRs and CI, then authorize specific PRs or the exact approved portion of a stack. Keep trunk healthy.
- **Live operations:** Separately authorize running the real scanner, cloud provisioning, production configuration, or deployment. `masscanr` has strict scope and worker-cleanup guardrails.
- **Release:** Separately authorize selecting/tagging a tested `main` commit and publishing a GitHub Release. Deployment also needs permission.

## How versioning works

Versioning is **not part of the SDLC workflow**. Each project defines its own numbering and release conventions in `VERSIONS.md`, which Codex must consult before preparing a release. You can replace that policy for another project without changing the intent/sub-issue/PR/trunk process.

For `masscanr`, release identifiers are a simple increasing counter in SemVer-shaped tags: `v10.0.0`, `v11.0.0`, `v12.0.0`, and so on. The leading integer increases by one for each release; `.0.0` stays fixed. This is **not a claim that each release breaks compatibility**. Releases are tags in Git and published as GitHub Releases. See `VERSIONS.md` for the exact rules.

## How to release when you are ready

You do not need a Version Issue or release branch. Start a new Codex conversation:

> Read `VERSIONS.md`. Review the current `main` branch and merged PRs since the latest release. Verify checks and identify breaking changes, migrations, regressions, and feature flags. Propose the next identifier under our policy, an exact commit, and GitHub Release notes. **Do not tag, publish, or deploy until I approve.**

Codex provides the release proposal and supporting links. After you review it, you can authorize tagging/publishing (and deployment separately if applicable). The release contains the changes present in the selected trunk commit, not an arbitrary set of unfinished Intent Issues.

## Continue work in another Codex session

Give Codex the Issue number; GitHub retains the durable plan:

> Continue `st3fan/masscanr#123`. Read `OBJECTIVE.md`, its full Issue body, labels, sub-issues, dependencies, PRs, Project status, `AGENTS.md`, and `HUMANS.md`. Summarize the current state and next safe action before changing anything. Do not merge, deploy, or release.

Useful commands:

```sh
gh issue list -R st3fan/masscanr
gh issue view 123 -R st3fan/masscanr
gh pr list -R st3fan/masscanr
gh stack view --json  # if gh stack is available
```

**Default rule:** use `OBJECTIVE.md` for enduring project direction, start each new change with an ordinary Intent Issue, build small PRs, merge approved safe slices into trunk, and release only when you choose to.