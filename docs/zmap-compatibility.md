# ZMap 4.3.4 compatibility contract

V4 uses Alpine 3.24.x (starting at 3.24.4), signed stable v3.24 main,
and the exact `zmap-4.3.4-r0` x64 package. The observed executable SHA-256 is
`fee78888264d48120a71df24bef0963f3dbf6c40970460528614eb75e7d3c229`.
Workers record package and executable identity and must agree before scanning.
No source build, compiler or alternate engine is installed for ZMap.

Run `scripts/check-zmap.sh` (optionally `--network host` for Docker build
package downloads). Runtime checks use `--network none --cap-drop ALL`.
The native `--dryrun` path prints packets instead of calling `send_batch`.
Explicit loopback interface, synthetic source/gateway MACs and source IP avoid
ARP/interface discovery. No actual app, cloud VM, or network probe is used.
The tests compare every shard's output to the expected IP/port Cartesian
product, including 0/65535 ports, lists/ranges, tiny/disjoint scopes, 1/2/3/5
shards and seeds 0/42/9223372036854775807. Unsupported tiny-scope sharding is
checked for a native rejection rather than silently dropping work.

## Command policy

- Exactly one sender thread and one probe per IP/port pair. Common shard i/N
  becomes `--shards N --shard i-1` with the same seed on every worker.
- Seeds use 0..9223372036854775807, the native signed long-long parser's range.
  Generate within that range for ZMap, retain the effective seed unchanged.
- Rate is explicit, 1..2147483647 packets/second per worker (native signed int).
  Ports are normalized unique TCP ports 0..65535; up to all 65536 are supported.
- Native shard range is 1..65535. Require shards <= eligible IP/port pairs;
  for small permutation groups (p=257), additionally require shards < 256
  to avoid the upstream iterator assertion. Never manufacture empty workers.
- `--config /dev/null` excludes ambient config. A private run-owned allowlist
  contains exactly the requested normalized CIDRs, and `--blocklist-file
  /dev/null` disables distro exclusions. Upstream always excludes 0.0.0.0;
  account for that address when validating eligible work. Other requested
  addresses, including private networks, are not silently excluded.
- No sampling, result cap or max-runtime. The controller timeout produces an
  incomplete run, and cancellation still reaches ownership-checked cleanup.
- TCP SYN module, source-port validation enabled, 10-second response cooldown,
  explicit window deduplication with 1,000,000 IP/port entries. Window eviction
  can admit late duplicates; preserve them as accepted records, like masscan.
  IP-only full deduplication is unsuitable for multiple ports.

## Results and status

Request CSV fields `saddr,sport,timestamp_ts,success,classification,repeat`.
`sport` is the target port, not the local destination response port.
`timestamp_ts` is the capture observation time in Unix seconds, with no local
clock fallback. Filter `success = 1 && repeat = 0`; the decoder also checks
successful SYN-ACK classification/repeat flags, and the shared finding sink
independently checks scope and ports. CSV is flushed after each record.

Use structured `--status-updates-file /dev/stderr` and `--quiet`.
Status and diagnostics share the SSH stderr transport but have separate
schemas: exact CSV header/records versus diagnostic text. The pinned monitor
has 21 header fields and **22 data fields** (an extra empty field before the
last two send-failure rates). Fixtures must retain this quirk.
`active-send-threads == 0` indicates response cooldown; keep waiting distinct
from successful process exit. Native `percent-complete` estimates total time,
including cooldown, and is not a measured sending percentage. Sending percent
therefore remains unknown; cooldown establishes sending at 100%. Measured rate
and estimated remaining time are displayed when valid. EOF alone is not scan
success: require successful command exit and drain both streams. Discovery
counts come only from the common accepted-finding sink, never packet totals.

## Pinned implementation evidence

- [Options and native parser types](https://github.com/zmap/zmap/blob/v4.3.4/src/zopt.ggo.in)
- [Package/version/config and shard handling](https://github.com/zmap/zmap/blob/v4.3.4/src/zmap.c)
- [Sender and non-sending dryrun path](https://github.com/zmap/zmap/blob/v4.3.4/src/send.c)
- [Iterator and shard partitioning](https://github.com/zmap/zmap/blob/v4.3.4/src/shard.c)
- [Source-port aware repeat detection](https://github.com/zmap/zmap/blob/v4.3.4/src/recv.c)
- [TCP SYN fields](https://github.com/zmap/zmap/blob/v4.3.4/src/probe_modules/module_tcp_synscan.c)
- [Capture timestamps](https://github.com/zmap/zmap/blob/v4.3.4/src/probe_modules/probe_modules.c)
- [Status schema and cooldown estimates](https://github.com/zmap/zmap/blob/v4.3.4/src/monitor.c)
- [CSV flush behavior](https://github.com/zmap/zmap/blob/v4.3.4/src/output_modules/module_csv.c)

A real Vultr boot/login/scan is not part of these checks and requires separate
authorization. These checks make no throughput or cost comparison claims.
