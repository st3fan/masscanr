# masscanr — Project Objective

## Purpose

Make large-scale **authorized TCP port discovery** practical to operate without depending on a commercial scanning service. `masscanr` coordinates distributed, high-throughput port scans across temporary cloud workers while giving the operator explicit control over scope, execution, visibility, and recovery.

## Problem and audience

Security engineers need to discover which TCP ports are exposed across networks they are authorized to assess. At scale, a single scanner can be operationally awkward: capacity must be provisioned, work distributed, progress observed, interruptions recovered, and temporary infrastructure cleaned up.

`masscanr` is for operators who want this capability in their own environment, with predictable controls and reusable machine-readable results.

## Desired outcomes

- **Controlled scope:** specify, review, and constrain authorized scan targets and ports before starting work; support exclusions and guard against accidental expansion.
- **Distributed execution:** partition work across temporary cloud workers and support suitable scanning backends without requiring users to manage each worker manually.
- **Reliable operation:** handle interruption, stopping, recovery, and cleanup without losing track of scan state or leaving unrelated infrastructure at risk.
- **Operator visibility:** expose configuration, progress, results, errors, and final scan/cleanup outcomes through an API and useful terminal tooling.
- **Reusable output:** retain useful port-discovery results and provide formats that other security tools can consume.

## Direction

Continue developing a focused, API-driven scan orchestration service with a small, usable CLI and terminal client. Improve scan configuration, observability, reliability, and the operator's ability to manage large jobs. Keep components modular enough to evolve scanner backends and infrastructure providers when justified by a concrete need.

Features should deliver measurable improvements to **port scanning**, not expand the product into unrelated security workflows.

## Scope and non-goals

- In scope: port discovery, scan planning and control, sharding, worker lifecycle, safe scope/exclusions, run history, recovery, monitoring, and result export.
- Out of scope: vulnerability identification, exploitation, generalized penetration-test automation, and a full asset-management or SIEM platform. Those capabilities may consume `masscanr` results through separate tools.
- Scanning is restricted to explicitly authorized targets. The project must not trade scope safety, worker cleanup, or operator control for raw throughput.

## Guiding principles

1. **Safety before speed.** Make scope and scan intent explicit; never silently broaden targets.
2. **Small, composable capabilities.** Prefer clear APIs and reusable results over a monolithic security suite.
3. **Recoverable by design.** Preserve authoritative state and show uncertainty instead of guessing success.
4. **Operator control and transparency.** Surface the current plan, progress, costs/worker state where available, failures, and cleanup outcomes.
5. **Simple deployment and evolution.** Make local development straightforward and scale execution only when needed.

---

This is enduring project direction, **not** a feature specification, backlog, implementation plan, or release calendar. Individual changes belong in GitHub Intent Issues; release policy lives in `VERSIONS.md`. Material changes to this objective require a human-approved Intent Issue and pull request.
