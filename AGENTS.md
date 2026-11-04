# masscanr

- V1 is described by https://github.com/st3fan/masscanr/issues/1.
- Use `gh` for GitHub interactions and the installed `gh-stack` skill with
  `gh stack` for this project's single V1 PR stack. Read the skill before
  stack operations. Create/link an issue before implementation; keep PRs small.
- Write idiomatic Go, run `gofmt`, `go test ./...`, and appropriate checks.
- Do not run the actual app without asking the user first. Tests may target
  the authorized IPv4 prefix `64.98.138.0/22` (masked: `64.98.136.0/22`).
- Do not leave test VMs running. Cleanup may delete only workers with the
  `masscanr` label and the current run's unique ownership tag. Never modify
  unrelated infrastructure, including the existing VM at `144.202.29.87`.
- Keep credentials out of commits, logs and worker configuration. Do not merge
  PRs or publish releases without authorization.
