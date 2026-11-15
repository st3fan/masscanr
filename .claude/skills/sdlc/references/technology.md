# Bootstrap technology discussion

`TECHNOLOGY.md` is a **project-specific, durable technology policy**. It describes chosen technologies, preferences for how to use them, the reasoning behind important choices, and meaningful constraints. The SDLC skill must not prescribe a single stack across projects.

## Conversation-first discovery

Before opening a Bootstrap PR for a new project:

1. Inspect existing code, dependency manifests, configuration, README, deployment setup, and any existing `TECHNOLOGY.md`. Derive only what the repository actually establishes.
2. Start by identifying the **kind of project** (CLI tool, library, desktop app, web app, API/service, background worker, or a combination). Ask **a few focused questions at a time** in Codex, never a long mandatory questionnaire. Cover only relevant choices:
   - Programming languages and runtime versions, supported platforms, and preferences such as typing conventions
   - Application style and framework choices: API, CLI, UI, SSR/SPA, and key libraries
   - Persistence and messaging, when needed
   - **Packaging:** how to build and package the software (for example native executable, Python/npm package, container image, or desktop bundle)
   - **Distribution:** how users obtain or install it (for example GitHub Releases, package registry, container registry, installer, or hosted SaaS)
   - **Hosting:** where it runs, if remotely hosted at all (for example developer's machine, user-managed server, VPS, managed platform, or cloud service)
   - **Deployment:** how an application or service is installed and operated (for example native process, systemd, Docker Compose, Kubernetes, managed service, or serverless), including environments and IaC where useful
   - Testing, tooling, code style, dependency management, and CI
   - Constraints, choices deliberately avoided, and the rationale behind key decisions
3. **Adapt the conversation to the project type.** For a CLI, ask about native binaries, supported operating systems and how users install it; hosting/deployment may be **Not applicable**. For a library, focus on package publishing and compatibility rather than servers. For a web service, ask where it should run initially and whether a native process, Docker Compose, Kubernetes, serverless, or a managed service makes sense. Do not assume Docker, Kubernetes, or any cloud provider by default.
4. **Prefer the simplest packaging and deployment approach that meets current requirements.** Do not introduce Kubernetes or complex infrastructure for hypothetical future scale. Offer a compact set of reasonable alternatives and a recommended default with tradeoffs when the user has not chosen. Use **Not applicable** for irrelevant concerns and **Undecided** for relevant decisions deferred intentionally. Never invent a choice or force premature infrastructure decisions.
5. **Present the proposed technology policy in conversation for explicit approval.** Do not send the human to a file or Issue for manual editing. Bootstrap does not need a special Issue number.
6. Once approved, draft a complete `TECHNOLOGY.md` outside the current project worktree (a temporary file is fine). Reuse that exact draft during the dry run and final Bootstrap PR command. Check that it contains no tokens, passwords, private infrastructure secrets, or irrelevant implementation plans.

For an **existing project**, read and preserve its current `TECHNOLOGY.md` by default. Ask targeted questions about gaps or revisions; only replace the file when the human explicitly approves a new draft. If it is missing, produce an approved draft based on the confirmed existing stack and preferences.

## Suggested TECHNOLOGY.md shape

Use the headings that materially describe this project; keep it concise.

```markdown
# Technology

## Languages and frameworks
- Language/runtime: ...
- Frameworks and key libraries: ...
- Storage/messaging (if relevant): ...

## Packaging and distribution
- Package/build output: ...
- Supported platforms and installation/distribution channel: ...

## Hosting and deployment
- Hosting environment: ...
- Deployment approach and environments: ...

## Development environment
- Local tooling and dependency management: ...

## Testing and quality
- Test strategy and validation/CI: ...

## Engineering preferences
- Typing/style/architecture: ...

## Rationale and tradeoffs
- Choice: ... because ...
- Deliberately avoided: ... because ...

## Constraints and open choices
- Must support: ...
- Undecided: ...
```

Keep only relevant sections. For a CLI or library, omit hosting/deployment when it is not applicable, or state **Not applicable** where clarity helps; mark unresolved but relevant choices **Undecided**. Avoid empty placeholder bullets or invented version numbers. Separate *selected technology* from *preference* and *reasoning*. This is not a detailed architecture design, product objective, implementation plan, or release policy. The objective belongs in `OBJECTIVE.md`; implementation decisions belong in GitHub Issue bodies; releases belong in `VERSIONS.md`.

## Passing the draft to the Bootstrap PR script

Create an approved file outside the project checkout. For example:

```sh
technology_draft="$(mktemp /tmp/sdlc-technology.XXXXXX)"
# Codex writes the approved TECHNOLOGY.md Markdown to "$technology_draft"
python3 /path/to/installed/sdlc/scripts/bootstrap_pr.py --root . \
  --technology-file "$technology_draft" --dry-run
python3 /path/to/installed/sdlc/scripts/bootstrap_pr.py --root . \
  --technology-file "$technology_draft"
```

Only after the human approves the choices should Codex execute the final command. Handle the temporary draft securely and remove it when it is no longer needed.

If the remote default branch already has an approved `TECHNOLOGY.md` and no changes are proposed, omit `--technology-file`; the script leaves that file unchanged. If the file is absent, `--technology-file` is mandatory. Never complete bootstrap by committing a generic, unfilled technology template.
