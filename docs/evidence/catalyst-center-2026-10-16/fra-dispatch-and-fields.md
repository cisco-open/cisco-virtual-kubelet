# RRM FRA dispatch and pinned-field qualification — 2026-10-16

RRM FRA now rereads its complete selected summary identities and typed native
details immediately before a prepared write. This includes already-converged
siblings, omitted/preserved unlocked attributes and desired in-memory input.
A missing target must remain absent. A replaced UUID, changed system flag,
selected native edit or changed desired input refuses dispatch. Every prepared
write is consumed once, including stale observation and ambiguous receipts.
The native API still has no atomic compare-and-swap between the final read and PUT.

CleanAir and FRA reuse one adapter-local typed feature snapshot collector and
one-use submission wrapper. Their native readers, field/default handling,
protected-system checks, payloads and verification remain resource-specific.
No shared provider contract, journal state machine, transport, public field or
schema was added. CleanAir's existing resource-qualified retained Update task
policy is unchanged; FRA Unknown task recovery was not enabled at this stage; subsequent scoped
Update support is recorded in [combined recovery evidence](wireless-feature-update-recovery.md).

The pinned module/provider model remains authoritative: both band-pair labels
map to native enums, sensitivity is emitted only for `2.4GHz_5GHz`, interval is
1–24 hours, and the six sensitivity values are preserved. Definition API acceptance
does not attest WLC firmware eligibility or RF behavior.

## Native qualification

[Recorded native test](fra-guarded-transitions-native.log) passes in 8.77s
(race package 10.906s), using generated definition `CVK_16025511`, UUID
`b81aaf99-cdcd-4932-8148-49244eff1c46`:

- Create task `01a142a2-8e71-7807-ae9d-3c5f28e316cf` completes and readback/no-op verifies.
- A generated-only concurrent freeze change completes with task
  `01a142a2-928c-79a9-85e7-86147871aa1c`. The previously prepared interval update
  refuses its stale snapshot; reuse of that consumed permit is also refused.
- Fresh interval update task `01a142a2-94d3-7e2f-99c3-2abb0c531042` completes on
  the same UUID. Prepared create/update reuse cannot dispatch another request.
- Same-UUID updates/readback/no-op cover Low, Medium, High, Higher, Even Higher
  and Super High, both freeze/status values, and interval boundaries 1/24.
- Band changes complete 2.4/5 → 5/6 → 2.4/5 GHz, omitting sensitivity for the
  5/6 pair and restoring Medium for the 2.4/5 pair. Tasks are recorded in the log.
- Exact generated UUID cleanup task `01a142a2-ae33-78dc-a16f-694bac806ac5`
  completes and the full FRA summary baseline is restored.

The earlier transition-only run also passed (8.21s, package 10.334s;
`/tmp/cvk-fra-transitions-native.log`) before adding the concurrent native edit.
Both runs cleaned their generated definitions; no WLC/AP association or physical
operation was performed.

## Public and persistence verification

Ten public manifest revisions exercise create, interval update, all six
sensitivity values, freeze/status changes and both band directions, preserving
one UUID and exactly ten writes across simulated adapter restarts. The real
Kubernetes API test runs this sequence with actual CRD metadata/status persistence
and controlled native HTTP: FRA passes in 7.62s. Existing CleanAir band and
known-task/fencing cases also pass; combined package 25.886s. See
[real-API test log](fra-feature-envtest.log). These private ephemeral API servers
never use the installed cluster.

Selected-state fixtures cover all FRA attributes, unlocked fields, identity,
system protection, a converged sibling, concurrent appearance, desired changes,
ambiguous receipts and sixteen simultaneous submission attempts. Only one stable
attempt can dispatch. The focused FRA/CleanAir suite passes 4.984s
(`/tmp/cvk-fra-dispatch-tests.log`); broader wireless/composition checks pass
14.197s (`/tmp/cvk-fra-field-manifest-tests.log`). Shared journal, discovery and
IPAM regression passes 18.483s (`/tmp/cvk-fra-journal-regression.log`).

After extracting the helper, the existing counted live CleanAir manifest run
again recovers six serialized Unknown Update tasks with one POST, six PUTs and
one exact-ID cleanup DELETE. It passes 12.33s (package 14.253s), restoring the
baseline (`/tmp/cvk-fra-shared-guard-cleanair-native.log`). Private evidence:
`/private/var/folders/w5/1l6hg4ks3wvcdv_bkztxwtyw0000gn/T/cvk-cleanair-manifest-3080941524/`.

Complete model, ND, IOS XE software lifecycle, software-upgrade and migration
checks pass (migration 1.894s; `/tmp/cvk-fra-feature-foundations.log`). Report/apply
example compilation passes 2.219s (`/tmp/cvk-fra-feature-examples.log`). Strict
documentation builds and whitespace checks pass.

## Remaining scope

FRA uncertain Create/taskless outcomes and retained Unknown task observation,
durable result ownership/adoption, pruning, installed actors, unattended combined
FRA/CleanAir recovery, cross-collection late-edit admission, unknown native-field
update semantics and actual WLC/AP/firmware/RF behavior remain unqualified.
Subsequent acknowledged Update recovery qualification is tracked separately in
[combined recovery evidence](wireless-feature-update-recovery.md). This remains
partial resource support, not whole feature-template or model parity.
