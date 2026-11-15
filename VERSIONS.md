# Release and version policy

## Version identifiers

- Name Git tags and GitHub Releases `vN.0.0`, with `N` increasing by exactly one for each release. The next release after `v10.0.0` is `v11.0.0`.
- This is a release counter in a familiar version format. The major number does not indicate a breaking change or otherwise follow semantic versioning. Describe actual compatibility changes in the release notes.
- Do not reserve a number through an Intent Issue, PR, stack, or feature name. Check existing tags and GitHub Releases before choosing the next number; resolve any mismatch before proceeding.
- Never move or reuse a published tag. If a release is defective, explain the problem and propose a new version.

## Release timing and contents

- Release only when the human requests one. A merge, completed issue or PR stack, or calendar date does not trigger a release.
- Select an exact, tested commit already on `main`. The release contains everything in that commit; it is not assembled from a chosen list of Issues or PRs. Do not create a version branch merely to collect work for a release.
- Git tags and GitHub Releases with notes are the current publication contract. Downloadable binaries and registry images remain undecided and are not required for a release.

## Release review and publication

1. Read this policy and inspect existing tags and GitHub Releases. Propose the next unused `vN.0.0` identifier and the full SHA of a tested `main` commit.
2. Review CI and relevant test evidence. Compare the candidate commit with the preceding release and identify delivered changes, known problems, upgrade steps, and actual compatibility or security implications.
3. Draft release notes that summarize the delivered behavior and link significant Issues or PRs. State breaking changes explicitly when present; do not infer them from the version number.
4. Present the identifier, full commit SHA, validation evidence, release notes, and any proposed assets to the human before changing tags or publishing.
5. Obtain explicit authorization for creating and pushing the tag **and** for publishing the GitHub Release. One response may authorize both when it names both actions. Then create the tag at the approved commit, publish the corresponding release, and verify both.
6. Treat deployment as a separate operation requiring its own authorization.

Changing this policy requires a reviewed PR and human approval. Approval to merge a PR does not authorize a release.
