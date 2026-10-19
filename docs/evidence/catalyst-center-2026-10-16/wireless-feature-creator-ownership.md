# Public feature creator ownership — 2026-10-16

New public FRA/CleanAir Creates now use `feature-create:v1` intent identities
containing kind, name digest and typed desired-effect digest. Existing guarded
resource planners, group snapshots, REST transport and task lifecycle remain
shared. After native task success, the strict NCNC result verifier binds the
returned UUID to the selected collection/name and declared fields. The existing
atomic action-receipt publisher commits Verified and one `feature-created:v1`
creator record together, retaining the unchanged original operation and its
manifest UID/name/generation, controller generation and source UID/version.

The normal metadata Verified path cannot complete these new marker operations.
Release requires exactly one valid matching creator record; missing/pruned,
malformed or duplicate logical records cannot release the journal. Receipt
capacity is checked for the entire declared missing set before the first effect,
again at operation preparation, after native preflight against the live durable
permit and at publication. Current maximum is the existing 256 action records.
Late owner/source changes and status conflicts cannot publish stale authority.

A selected owned name must still resolve to its original UUID and owner. Foreign
lineage, disappearance, replacement or protected-system identity is refused.
New protocol acknowledged Creates may observe retained Unknown tasks without
resubmission; missing-task outcomes remain fenced. Retained legacy Create paths
keep their original semantics and cannot be retroactively adopted. Existing
unowned/imported definitions retain normal update/no-op behavior without acquiring
creator records. Ordinary updates preserve immutable creation lineage but do not
yet publish an applied owned-state fingerprint or update receipt.

No new public field, CRD, transport/provider contract or journal phase was added.
CC-local journal hooks add capacity, reserved-marker verification exclusion and
receipt-required release for this explicit protocol; other resource protocols
and ND/direct-device/SWIM implementations are unchanged.

## Verification

Public creator/update/legacy/taskless cases pass 9.480s
(`/tmp/cvk-feature-owner-public-vetted.log`). Capacity/import/identity and
missing/duplicate/pruned receipt release tests pass 8.909s
(`/tmp/cvk-feature-owner-safety-final.log`). Source and owner mutations during
native proof observation refuse publication, leave Verifying retained, publish
zero receipts and cannot use generic Verified: safety/race selection 8.988s
(`/tmp/cvk-feature-owner-races.log`). Imports no-op/update without ownership;
255 existing action rows plus two missing definitions cause zero native effects.

Initial fixtures omitted authoritative UUID results; they now supply canonical
IDs, exact detail paths and NCNC task result fields. An intermediate build caught
a duplicated metadata check with Go vet; the duplicate was removed. An initial
receipt test used nanosecond timestamps that do not survive metav1.Time JSON
round-trip; fixtures now use the same second precision as the producer. These
initial failures are not counted as successful qualification.

Actual CRD/status persistence passes the field/recovery suite (package 39.710s),
including fourteen pure/combined task/legacy/source/owner cases. See
[API lifecycle log](wireless-feature-creator-envtest.log). A separate actual API
release test accepts the valid record but refuses absent, distinct-key duplicate
logical and simulated-pruned records (6.35s, package 8.501s). See
[release log](wireless-feature-creator-release-envtest.log). Pruning is simulated
in the writer; no installed schema is changed. All API servers are private and
ephemeral, not the stopped lab cluster.

Feature, shared journal, discovery and IPAM race regression passes 37.017s
(`/tmp/cvk-feature-owner-regression.log`). The final focused executor/public
recovery checks pass 9.600s (`/tmp/cvk-feature-owner-executor-final.log`),
including read-only verification of versioned creator operations. The model
package passes separately in 0.412s after the coverage description update.

The initial complete CC package run exceeded Go's default ten-minute limit
(601.125s; `/tmp/cvk-feature-owner-full-package.log`) during template inventory
fixtures, without a preceding test assertion failure. This timeout is not counted
as a pass. The complete rerun uses an explicit twenty-minute limit and retains
structured results in `/tmp/cvk-feature-owner-full-final.jsonl`. It passes with
the race detector in 629.975s, including the complete CC package regression.
There are no failed test events. The longer limit accommodates the existing
serial fixture workload; no production timeout or safety guard was relaxed.

## Native public lifecycle

The [native run](wireless-feature-creator-native.log) passes seven combined public
manifest revisions in 33.51s (race package 35.420s). Both accepted Creates are
serialized as Unknown before result verification/publication, then all twelve
accepted Updates are likewise retained and recovered. All fourteen recoveries
complete without another native mutation attempt.

| Definition | Native UUID | Original Create task |
| --- | --- | --- |
| `CVK_MB_16041336` | `1fd9c4d9-3f51-4cf9-8e46-cb75c6f926a3` | `01a142ea-6007-7098-9832-ea39b79494b8` |
| `CVK_MB_16041336_FRA` | `1eab9c94-7e41-4745-af87-d62e566031bd` | `01a142ea-5a5a-763a-bbcb-3c1f4dc7a725` |

Both versioned creator records survive subsequent band/sensitivity/flag/interval
updates and source generation changes, retaining the original Create operation,
task and native UUID. Per-kind transport guards count exactly one POST, six PUTs
and one qualification-only exact generated UUID DELETE. Cleanup tasks
`01a142ea-d5a1-7ecf-82cb-eb68f7652420` (CleanAir) and
`01a142ea-d6f1-775f-864e-22362a3072f8` (FRA) finish, restoring both summary baselines.
Teardown does not publish tombstones or implement source pruning.

Private evidence directory:
`/private/var/folders/w5/1l6hg4ks3wvcdv_bkztxwtyw0000gn/T/cvk-cleanair-manifest-82232875/`.
`creator-records.json` contains both Verified original Create records.
`complete.json` records creatorRecords=2, durableOwnershipPublished=true,
knownTaskRecoveries=14 and cleaned=true. The native run uses a Kubernetes client
fixture; actual API persistence is separately qualified above. No installed worker
rollout or physical WLC/AP behavior is attested.

## Remaining lifecycle

Creator identity is now public and durable; full feature ownership is not complete.
Owned update-state receipts/fingerprints, dependency/association-aware pruning,
delete/tombstone/recreation, cross-owner/controller handoff, legacy adoption and
installed runtime/scale qualification remain pending. Missing-task/no-dispatch
journals are not automatically retired. Missing or pruned mandatory records fence
in-flight release; this does not reconstruct arbitrary post-release lost history.
A valid creator record is not a complete current-state or deletion eligibility
proof. Retain policies stay in force and whole-model parity remains incomplete.


The subsequent [owned-update stage](wireless-feature-applied-state.md) adds
replaceable applied-state receipts while preserving this creation lineage. The
remaining lifecycle list above describes the boundary at the creator-only stage.
