# VERSIONS.md — masscanr release policy

This file defines **how masscanr versions and publishes releases**. It does not define feature scope, implementation planning, release cadence, or what belongs in an Intent Issue. Those remain independent of releases.

`AGENTS.md` defines the development workflow; `HUMANS.md` describes human interaction with it. Other projects may define an entirely different policy in their own `VERSIONS.md`.

## Version identifiers

- Use Git tags and GitHub Releases named `vN.0.0`, where `N` is an increasing positive integer.
- Increment `N` by exactly **one** for every new release. Keep the minor and patch components at `0.0`.
- After `v10.0.0`, the next releases are `v11.0.0`, `v12.0.0`, `v13.0.0`, and so on. Determine the next `N` from existing release tags, not from an Intent Issue or PR name.
- This is a **release counter in SemVer-compatible syntax**, not standard SemVer major/minor/patch compatibility signaling. A new `N` does **not** automatically imply a breaking change. Describe actual compatibility changes in the release notes.
- Do not create parallel `v11` / `v11.0` aliases, move existing release tags, or reuse a released identifier.

## When and what to release

- `main` is the only normal integration branch and the source for releases.
- Releases are **on demand**, only when the human explicitly requests and approves one. Merging a PR, completing a PR stack, closing an Intent Issue, or reaching an arbitrary date does not trigger a release.
- Select a tested, known-good commit already present on `main`. The release includes everything contained in that commit, not a hand-picked set of Issues.
- Do not create version-planning Issues, version branches, release trains, or implementation milestones just to make a release.

## Codex release procedure

1. Read this file. Inspect existing Git tags/GitHub Releases, identify the latest `vN.0.0`, and propose `v(N+1).0.0`. Confirm that the proposed tag does not already exist.
2. Confirm the exact `main` commit, CI status and relevant tests. Compare merged PRs since the preceding release and identify any known regressions, migrations, feature flags, security or compatibility implications.
3. Prepare concise GitHub Release notes summarizing delivered behavior and linking significant PRs/Issues. Note actual breaking changes explicitly if present; do not infer them from the major number.
4. Present the proposed tag, full commit SHA, validation results, and release notes for human approval. **Do not tag or publish yet.**
5. Only after explicit approval, create/push the Git tag for that exact commit and publish the corresponding GitHub Release with the same identifier. Verify the tag points to the intended commit and the release is published successfully.
6. Deployment is a **separate human authorization** and is not implied by approval to tag or publish.

Published tags should be treated as immutable. If a release is defective, disclose the problem and propose a new release rather than moving an existing tag. Changes to this versioning policy require human approval.