# E09 physical transfer measurement and cache decision

Date: 2 October 2026

Qualified cohort: Cisco C9300, IOS XE 17.18.02–17.18.03, Ubuntu16 k3s
worker placement

Final runtime candidate: `1556238a`

This record separates source-to-worker materialization from worker-to-device
gNOI streaming. It contains no credentials, Secret values, management
addresses or maintenance tokens.

## Frozen input and safety boundary

Both runs used the same immutable 17.18.03 image:

- digest: `sha256:df6055e4e1e88135b311998d721ff6d20a94a475113c1ff678fb65d14dc11049`;
- verified size: 1,249,368,115 bytes;
- strategy: `PrepareOnly`;
- global, cache-domain and applicable risk-group transfer concurrency: one;
- configured aggregate worker-to-device ceiling: 25,000,000 bytes/second.

`PrepareOnly` submitted no activation and caused no reload. The final `.103`
run left 17.18.02 running/committed and 17.18.03 installed/inactive. The Node
remained Ready and the campaign completed its post-mutation network-health
soak before releasing the ledger reservation.

## Measurements

| Physical run | Segment/cache | Bytes | Duration | Result |
| --- | --- | ---: | ---: | --- |
| `.100`, candidate `050ab07a` | origin → worker, cold SFTP fetch | 1,249,368,115 | 39.361 s | digest and size verified |
| `.100`, candidate `050ab07a` | origin → worker, same-Pod cache validation | 0 origin bytes | 8.721 s | verified cache hit |
| `.100`, candidate `050ab07a` | worker → device | 1,247,805,440 reported | 144.122 s | IOS XE emitted `Validated`, but its last progress event stopped 1,562,675 bytes short |
| `.103`, candidate `1556238a` | origin → worker, cold SFTP fetch | 1,249,368,115 | 51.225 s | digest and size verified |
| `.103`, candidate `1556238a` | origin → worker, same-Pod cache validation | 0 origin bytes | 7.854 s | verified cache hit |
| `.103`, candidate `1556238a` | worker → device | **1,249,368,115** | 159.111 s | complete successful stream, native install corroborated |

The worker was scheduled on Ubuntu16 for both segments. The source and worker
were local to that host; the physical switch received the second segment over
the management network. These measurements therefore qualify this lab path,
not a WAN throughput claim.

The `.100` run began after `050ab07a` was deployed and before `9e6cdfcc` was
created; the earlier table incorrectly attributed that run to the later fix.
IOS XE's `TransferProgress` stream is not a byte-complete accounting source:
on both physical switches the final progress event was 1,247,805,440 bytes
(99%) even though `OS.Install` then returned `Validated` and native inventory
proved the exact 1,249,368,115-byte image. Candidate `9e6cdfcc` fixes the
metric contract: a successful content-bearing stream records the verified
resolved size; a standby supervisor synchronization that sends no content
records zero. The `.103` run on `1556238a` physically confirms the corrected
1,249,368,115-byte counter and 100% status while retaining the last device
progress event only as interim progress.

## Queue and ownership defects found during the run

The `.103` campaign initially waited behind an older Prepared receipt whose
exact activation had already succeeded and settled. The rollout manager knew
the receipt was consumed, but the provider queue yielded only while
reconciling that activation itself. Candidate `1556238a` makes one fail-closed
predicate authoritative in both controllers. Only an exact, terminal,
verified, mutation-settled activation with matching receipt, device, Node,
campaign, policy and plan bindings releases queue ownership. The Prepared
object remains immutable audit evidence. Nonterminal, mismatched or unsettled
successors still block.

The physical retry then advanced through `Granted`, worker acknowledgement,
`PrimaryInstall`, `Prepared`, continuous health soak and manager/worker
`Settled`. The final ledger had no reservation. A separate `.101` campaign
correctly stopped before mutation because a workload was running on the
target Node; it was closed through monotonic audited cancellation without
deleting or scaling that workload.

## Durable-cache decision

Do **not** add a shared PVC cache in this roadmap increment.

The cold source fetch was 39–51 seconds, while device streaming and native
validation took 144–159 seconds. The existing digest-addressed ephemeral
cache already avoids a second origin transfer inside the worker lifetime and
revalidates in 8–9 seconds. A durable cache would add storage provisioning,
writer exclusion, credential reauthorization, corruption/GC recovery and
cross-Pod ownership to save only the source-materialization portion on this
measured local path. The evidence does not justify that security and
operational surface.

This is a bounded decision for the measured local SFTP path, not a general E09
performance qualification. It does not include a second origin type or WAN
path, a cold fetch after Pod replacement, storage high-water measurement, or
CPU-cost comparison. Those measurements are required before changing the
decision or claiming a broader supported envelope. E09-B/C and the volume-loss
portion of E09-D are not applicable while durable caching is not selected.
Any future cache must retain the roadmap's digest, source/trust/Secret
identity, current authorization, atomic publication, active-reader and
bounded-GC requirements.

## Verification

- focused provider/gNOI tests and the full repository test suite passed;
- race-enabled provider and controller packages passed on `1556238a`;
- Helm revision 155 ran the exact manager and all six C9K functional workers;
- final IOS XE inventory showed 17.18.02 committed and 17.18.03 inactive;
- transfer status was exactly 1,249,368,115/1,249,368,115 (100%);
- the campaign ended `Succeeded`, the leaf ended `Prepared/Settled`, all three
  physical C9K Nodes were Ready, and the topology ledger was empty.
