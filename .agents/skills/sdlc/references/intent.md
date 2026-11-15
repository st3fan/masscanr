# Action: intent

**Goal:** Turn an idea or problem into a well-scoped GitHub workstream, not an immediate unreviewed coding session.

1. Read `OBJECTIVE.md` and `TECHNOLOGY.md` (when they exist), existing guidance, relevant code, Issues/PRs, Project board, and any Wiki documentation. Respect confirmed stack choices and preferences when proposing designs; surface any needed technology-policy change for human approval. Search for duplicate or overlapping work. If the idea conflicts with the objective, explain the conflict; propose a separate objective revision rather than silently expanding scope.
2. Clarify **problem, outcome, constraints, non-goals, success criteria, and important uncertainties** **in the Codex conversation**. Present a short draft and specific questions there, not a direction to go review or edit an Issue. Ask only materially useful questions and offer a recommended design when tradeoffs can be reasoned about.
3. Create or update a normal Issue. For a substantial outcome, use an **Intent Issue** with `scope:intent`. Record canonical content in its body:
   ```markdown
   ## Intent
   ## Acceptance criteria
   ## Design
   ## Implementation plan
   ## Risks and open questions
   ## Decisions
   ## Delivery
   ```
   Use only sections that add value. **Update the body as decisions are confirmed through conversation**; do not make the human maintain it manually. If the human edits an Issue directly, re-read and reconcile their changes before subsequent updates. Do not overwrite direct edits, bury authoritative plans in comments, or commit planning Markdown.
4. Label issues using `type:feature|bug|maintenance|research` (one), `area:*` (one or more relevant areas), and `priority:p0|p1|p2` (one). Add `scope:intent` for parent work, `risk:security|breaking|data` for applicable risks, and `delivery:stack` if dependent PR slices are planned. Treat GitHub Project Status as the status authority rather than using status labels. Check labels exist before applying them; propose a one-time label setup rather than inventing labels on an unrelated Issue.
5. For large work, propose **native GitHub sub-issues** representing independently verifiable deliverables and native dependency relationships for blockers. PRs are *reviewable code slices*, not necessarily one per sub-issue. Large dependent changes may be stacks of approximately 2–5 PRs based on trunk, with small independently safe merges.
6. **Summarize the resulting intent and plan in Codex**, including scope, acceptance criteria, design tradeoffs, open questions, and risks. Ask for explicit human approval **in conversation** before implementation; linking the Issue is useful for traceability but is not a request to leave Codex and review it elsewhere. An Issue being created or marked Ready is not automatically permission to run live operations, merge, release, or deploy.
7. If implementation is subsequently approved, use short-lived branches, small PRs, tests and CI. Keep Issue body and Project status current; merge only on explicit human approval. Releases are separate, governed by project `VERSIONS.md` when present.

When the repository has not yet been bootstrapped, recommend `$sdlc bootstrap` or `$sdlc objective` first, unless the user specifically requests a standalone Issue. Do not force Issue #1 or version-based parent Issues.

Example prompt: `$sdlc intent Add an exclusion policy for scan targets; help me define scope and acceptance criteria, but do not implement yet.`
