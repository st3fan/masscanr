# Project objective

## Purpose

Help operators maintain a trustworthy view of reachable TCP services on networks they are authorized to assess. masscanr should make repeatable, large-scale port discovery and change monitoring practical without relying on a commercial scanning service.

## Problem and audience

Internal security and network teams, as well as external operators assessing authorized networks, need to know what TCP services are exposed and how that exposure changes. Repeated scans at scale require careful scope review, distributed capacity, visibility into progress and failures, retained evidence, and safe cleanup of temporary resources.

## Desired outcomes

- A current, evidence-backed inventory of observed reachable TCP ports, with enough time and target provenance to compare runs and recognize changes.
- Explicit, reviewable target and port scope for every authorized scan or approved recurring scope, without silent expansion.
- Repeatable scans that can scale across temporary workers while keeping execution, resource use, interruption, and cleanup visible to operators.
- Durable run outcomes and reusable results that downstream security tools can consume.

## Direction

Evolve the existing scan service and operator-facing client toward continuous inventory and change visibility. Keep operators in control of scope and execution. Decide scheduling, comparison, and presentation mechanisms through separate Intent Issues.

## Scope and non-goals

Port discovery, scan control, run history, result provenance, comparison, and monitoring are in scope as enduring directions. Exploitation, vulnerability determination, scanning outside explicitly authorized scope, and a general asset-management or SIEM platform are outside this objective.

## Guiding principles

1. Make authorization and effective scope explicit before scans run; never silently broaden targets.
2. Preserve evidence and distinguish observed facts from unknown or incomplete coverage.
3. Make progress, failures, resource use, and cleanup outcomes visible; never claim success or cleanup without evidence.
4. Keep results portable and interfaces understandable so operators can use the inventory in other workflows.
5. Prefer bounded, operator-controlled operation and evolve scale only for concrete needs.
