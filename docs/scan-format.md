# Scan documents, schema 1

A `.scan` file is UTF-8 JSON Lines: one object and newline per record, one
run per file. New files are private (`0600`) and exclusively created. Schema
versions are independent of application versions. No credentials belong in
these documents. Configuration records describe the effective authorized scope.

Every record has `type`, contiguous unsigned integer `seq` starting at 1,
UTC RFC3339 `at`, and exactly one matching payload object:

| Type and payload key | Contents |
| --- | --- |
| `scan` | Header, effective configuration, run identity and ownership metadata |
| `backend` | Shard, observed name/version/build and platform/packages |
| `worker` | Shard/instance identity, phase, cleanup status and bounded detail |
| `progress` | Shard, nullable percent/rate/ETA seconds and response-wait flag |
| `finding` | Shard, IP, port, protocol, state and scanner Unix-seconds timestamp |
| `finished` | Scan/cleanup outcomes, findings, shard outcomes, undeleted IDs and errors |

The header is first and immutable. It has `format: "masscanr.scan"`, `schema: 1`,
`run_id`, `created_at`, `controller_version`, `label: "masscanr"`,
`ownership_tag: "masscanr-run-<run_id>"`, and `config`.

Configuration contains normalized/deduplicated IPv4 `targets`, canonical TCP
`ports`, `backend`, decimal-string `seed`, `shards`, `rate_per_worker`, `provider`,
`region`, `instance_type`, all seven effective `timeouts` as duration strings,
`output`, and two `exports` destinations. The timeout keys are `create`, `ready`,
`prepare`, `scan`, `cleanup`, `delete`, and `poll`. Seeds retain their full
unsigned 64-bit range; ZMap restricts them to its supported signed 63-bit range.

Each complete record, including its newline, is limited to 4 MiB. Shards are
limited to 65,535, with at most that many additional reconciliation-only worker
identities. Readers retain worker state and counts, not findings. These bounds
also apply before provisioning. Unknown fields on recognized records may be
ignored; unknown types and unsupported schema versions fail. Duplicate JSON keys
(including case variants), missing required values, excessive nesting, invalid
UTF-8, sequence gaps and invalid state transitions are errors. Maximum JSON
nesting is 64. Numeric fields decode directly as integers or typed measurements.

Worker scan phases are `pending`, `provisioning`, `booting`, `preparing`, `ready`,
`scanning`, `waiting`, `collecting`, `completed`, `failed`, and `cancelled`.
Cleanup independently moves through `pending`, `deleting`, `incomplete`, and
`complete`; failed deletion may be retried. A completed scan phase survives
deletion. Reconciliation-only workers have shard zero and an instance ID.
Backend identity is observed during preparation; all shards must be prepared
with a common build before scanning. Findings are accepted only while scanning
or waiting and must match the configured scope and ports.

Findings retain arrival order and duplicates. Their `timestamp` is the scanner's
observation time in Unix seconds; record `at` is the controller's collection
time. Counts refer to accepted observations, not unique endpoints. Unknown
telemetry is JSON `null`, distinct from measured zero. Progress is historical,
sampled at most once per second per shard plus phase boundaries.

Exactly one `finished` record terminates the document. `scan_outcome` is
`complete`, `failed`, or `cancelled`; `cleanup_outcome` is `complete` or
`incomplete`. Completion requires successful scanner exit, response waiting and
stream draining for all shards. Its `found`, ordered `shards` outcomes and sorted
`undeleted` IDs must agree with replayed state. `reconciliation_unresolved`
records uncertainty about additional owned instances. Nothing may follow it.

The writer serializes worker callbacks, buffers at most 64 KiB, and flushes/syncs
dirty data once per second. Configuration, create intent, identity, lifecycle
transitions and completion have immediate flush/sync barriers. Storage failures
are sticky and cancel scanning; cleanup uses in-memory tracking and must still
run when the document cannot be written. Sync scheduling is not a hard loss
bound during stalled or failing storage. The filesystem's normal durability
guarantees apply; the text format does not provide database transactions.

Offline readers validate the complete newline-terminated prefix. An
unterminated final line is ignored with a truncation diagnostic, even if it
contains valid JSON. Missing `finished` means completion is unconfirmed, not
proof that a controller is still running. Malformed terminated records are
corruption errors, never silently skipped. A live consumer must retain its
unfinished line while awaiting more data; the offline replay API consumes a
stable file/snapshot. Callbacks see provisional findings until replay succeeds.

The document aids manual reconciliation but cannot guarantee cloud cleanup
after controller loss. It is not a resume protocol. Manual edits, silent storage
corruption and external activity cannot be authenticated from its contents.
