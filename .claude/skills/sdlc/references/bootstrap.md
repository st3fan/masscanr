# Action: bootstrap

**Goal:** Through a short conversation, establish or confirm the project's technology stack and preferences, then open a **Bootstrap pull request** for `AGENTS.md`, `HUMANS.md`, and `TECHNOLOGY.md`. Creating files only in the current checkout is **not** a completed bootstrap.

## Conversation-first preparation

1. Identify the target GitHub repository and default branch. Read existing `AGENTS.md`, `HUMANS.md`, `TECHNOLOGY.md` (if any), the dependency files, existing code, and operational safeguards. Work in the target project, not the skill repository. Never overwrite unrelated changes.
2. Read [technology.md](technology.md). First identify whether this is a CLI, library, application, service, or combination. Ask focused questions **inside Codex** about language/runtime, frameworks, data needs, **packaging**, **distribution**, **hosting**, **deployment**, testing, tooling, and engineering preferences **only where relevant or not already decided**. Separate how software is packaged and obtained from where and how it runs; a CLI may need no hosting at all. If the existing stack establishes a choice, summarize it and ask for confirmation or clarification rather than starting from zero. Discuss the reason for important choices and alternatives deliberately excluded. Prefer the simplest suitable delivery model, not Kubernetes or containers by default. Mark choices as **Not applicable** or **Undecided** where appropriate rather than inventing requirements.
3. **Present a concise proposed technology policy for approval in the Codex conversation**, including any relevant packaging, distribution, hosting, and deployment choices (or why they do not apply). Do not ask the human to edit an Issue or Markdown file manually. Only after approval, write the project-specific draft to a temporary Markdown file **outside** the repository. Never copy a generic, unfilled technology template into Git. Do not include credentials or sensitive environment details.
4. If `TECHNOLOGY.md` already exists and its contents remain approved, preserve it unchanged; no new draft is required. If it is missing, an **approved** draft is required. If its contents need revision, obtain approval for the revision and provide the new draft. Changing the technology policy is an explicit decision, not a side effect of refreshing guides.

## Create the Bootstrap PR

5. Preview using the bundled orchestration script from the target project root:

   ```sh
   python3 /path/to/installed/sdlc/scripts/bootstrap_pr.py --root . \
     --technology-file /tmp/approved-technology.md --dry-run
   ```

   Resolve the script path from the installed skill. Use the **same approved file** for the dry run and PR; omit `--technology-file` only when an existing `TECHNOLOGY.md` is retained unchanged. The script uses an isolated Git worktree based on the remote default branch, leaving the user's checkout unchanged.
6. Inspect the proposed diff and reconcile conflicts in legacy instructions. In particular, preserve stricter project-specific security and operational rules. The script's low-level updater only replaces sections between `<!-- sdlc:managed:start -->` and `<!-- sdlc:managed:end -->`; on first adoption it appends managed guidance rather than deleting existing custom text. Stop for a human decision when you find a material conflict.
7. **Complete bootstrap by opening the PR**:

   ```sh
   python3 /path/to/installed/sdlc/scripts/bootstrap_pr.py --root . \
     --technology-file /tmp/approved-technology.md
   ```

   The script stages **only** `AGENTS.md`, `HUMANS.md`, and `TECHNOLOGY.md`, commits on a short-lived branch, pushes, opens **Bootstrap: Stefan's Development Lifecycle**, and verifies the PR URL. When no technology draft is necessary, omit the corresponding flag for both commands.
8. Return the **actual PR URL** and a concise summary of the selected stack and created/changed files. **Never merge without separate human approval**, start coding, or create a bootstrap Issue or release.
9. If all three artifacts are already current, report **already up to date** (no empty PR). If a Bootstrap PR already exists, link it instead of creating a duplicate. If the remote, authentication, Git identity, or write permissions are missing, report the blocker; do not claim bootstrap is complete.

## Responsibilities and safe updates

- [scripts/bootstrap.py](../scripts/bootstrap.py) updates the two generic guides in a directory. It is an internal building block, **not** the full `$sdlc bootstrap` action.
- [scripts/bootstrap_pr.py](../scripts/bootstrap_pr.py) creates the reviewed branch/PR and writes the approved `TECHNOLOGY.md` in that isolated worktree when required.
- After upgrading the installed skill, run `$sdlc bootstrap` again to propose updated managed guidance. Preserve existing technology choices unless the human approves changes.
- Bootstrap does **not** create `OBJECTIVE.md` or `VERSIONS.md`. Suggest `$sdlc objective` afterward when the project objective is missing.
