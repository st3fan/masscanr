# Action: objective

**Goal:** Establish or deliberately revise an enduring `OBJECTIVE.md` through a normal Intent Issue and reviewed PR.

1. Read the repository, README, current `OBJECTIVE.md` and `TECHNOLOGY.md` if any, active Issues and historical documents. Distinguish the project's purpose from its technical implementation preferences. Existing project guidance Issues (including #1 in old projects) may inform the draft but have **no privileged authority**.
2. **Use the Codex conversation as the working interface.** Ask focused questions in short rounds about the problem and audience, purpose, desired outcomes, direction, scope/non-goals, and principles. Skip answered questions. Share an understandable draft or summary *in conversation*, propose alternatives when consequential, and ask the human for a decision there. **Do not tell the human to go to GitHub to review/edit the Issue as the default next step.**
3. Find an appropriate existing Intent Issue or create a **regular GitHub Issue** with no special number. Keep its **body** synchronized with the direction as the conversation progresses: settled purpose, outcomes, constraints, open questions, and acceptance criteria. Comments are discussion, not the canonical draft. If the human edits the Issue directly, re-read and reconcile those edits before updating it; direct Issue edits are valid input, not a problem to undo.
4. Draft a concise `OBJECTIVE.md` with these headings (adapt as justified):
   - Purpose
   - Problem and audience
   - Desired outcomes
   - Direction
   - Scope and non-goals
   - Guiding principles
   Do not add detailed feature plans, architecture designs, milestone dates, version numbers, or release calendars.
5. **Present a concise but complete approval summary in Codex**: purpose, audience, desired outcomes, direction, non-goals, and any remaining risks/assumptions. Ask for explicit approval of that direction **in the conversation**; the Issue URL is for reference, not required homework. Sync the approved direction into the Issue body. Only then use a short-lived branch and open a PR adding/updating `OBJECTIVE.md`, linked to the Intent Issue. Do not merge without separate authorization.
6. For a new project without a configured GitHub remote, prepare a reviewable draft locally, tell the human what is missing to open the Issue/PR, and do not invent an issue number or claim it has been saved in GitHub.
7. After approved merge, close the bootstrap/update Intent Issue when its criteria are met. Future work should be evaluated against the objective, but changing it requires a new or existing approved Intent Issue and PR.

Example prompt: `$sdlc objective Help me define the purpose and direction of this repository; ask questions before proposing the charter.`
