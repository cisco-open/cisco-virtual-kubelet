# Feature owned-update state — 2026-10-16

Owned FRA and CleanAir definitions now retain a replaceable applied-state record
alongside their immutable creator record. The `feature-update:v1` operation marker
contains kind, logical-name digest and typed desired-effect digest; the operation
retains the original native UUID. Native NCNC task success must identify an Update,
the exact task, name and UUID, followed by selected-kind/name/UUID and declared
attribute readback. A successful generic task or matching metadata alone is not
sufficient. This reuses the common feature result decoder and existing transport.

The existing atomic action publisher commits Verified plus a `feature-applied:v1`
record. Publication revalidates owner, source, controller generation, retained
operation and immutable creator identity. Later updates replace exactly one
applied row per logical definition; original creator task/operation/source fields
remain unchanged. New row capacity is checked before preparation, at the final
submission permit and again at publication. Replacing an existing applied row
needs no extra slot, including at the existing 256-record limit. An update can
stop before dispatch if its new record would exceed capacity; this is not an
all-or-nothing multi-definition transaction.

The final native preflight also compares creator and applied records against the
planning snapshot. Generic Verified cannot finish this explicit protocol; release
requires the exact operation's valid, unique applied record and valid original
creator. Missing, malformed, foreign, duplicate or schema-pruned evidence stays
fenced. Known acknowledged Unknown Updates recover by observing the retained task
without another mutation. Missing tasks and legacy raw operation markers retain
their prior protocol; no retrospective applied receipt is inferred. Existing
imports remain unowned and use their existing Update/no-op behavior.

Records prove the last successful CVK write, not continuous absence of external
drift or a source-only revision requiring no mutation. They do not grant deletion
eligibility or reconstruct arbitrary history lost after release. Source pruning,
dependency/association-aware delete/tombstone/recreation, cross-owner handoff,
legacy adoption and installed worker qualification remain incomplete. Whole
resource/section parity flags remain false. No shared provider contract, CRD,
journal phase, ND or software-upgrade implementation changes were required.

## Verification

Final creator/applied receipt and public restart recovery checks pass 10.336s
(`/tmp/cvk-feature-applied-final-lineage.log`), including unchanged creator-record
comparison after every source revision. These include original UUID checks,
known/missing/failed/stale/foreign task outcomes and source/owner changes during
native proof observation. Update publication races retain the previous creators,
publish no new applied authority and cannot bypass generic Verified exclusion.
Release checks cover absent receipts, distinct-key malformed duplicates, simulated
writer pruning, missing creators and foreign applied owners. Capacity checks
cover new rows at the limit and replacement without an additional slot.

Feature/shared-journal/discovery/IPAM regression passes 39.024s
([regression log](wireless-feature-applied-regression.log)). Actual CRD/status persistence,
root/group acknowledged recovery, field transitions and six applied-release cases
pass 38.350s ([API persistence log](wireless-feature-applied-envtest.log)). The native service is
a fixture in these API tests; the API servers are private and ephemeral and do
not load the installed cluster's configuration.

Initial fixture failures are retained separately and not counted as passes:
timestamp round-trip comparison used a pre-serialization time location; it now
compares the stored creator before/after. The actual API rejected an exact
list-map duplicate, so the corruption test uses a distinct raw key and verifies
logical/malformed receipt rejection. Older single-kind native fixtures reported
Create progress for Updates; they now return the action-specific NCNC progress,
as the live appliance does. No production verification guard was weakened.

Pinned model, Nexus Dashboard, direct-device software lifecycle, shared software-upgrade
and migration-tool race checks also pass (`/tmp/cvk-feature-applied-foundations.log`);
unchanged packages use cached results.

## Native combined lifecycle

The [live log](wireless-feature-applied-native.log) passes seven manifest revisions
in 35.20s (race package 37.123s), with two retained Unknown Creates and twelve
retained Unknown Updates. Every acknowledged task resumes without replay. Each
kind has exactly one POST, six PUTs and one qualification-only DELETE.

| Definition | UUID | Original Create task | Latest Update task |
| --- | --- | --- | --- |
| `CVK_MB_16044547` | `9e2408de-123f-4aff-84a3-402f72af771d` | `01a14307-d88b-7d2c-aef7-451b4945cb78` | `01a14308-4b85-7ad6-bae6-d7210752f5b8` |
| `CVK_MB_16044547_FRA` | `4a658eda-3792-4f5a-938b-b92483033de0` | `01a14307-d2f3-7f1f-ad5f-3677b3272164` | `01a14308-44c5-73b4-872b-123ede63a8f5` |

Private artifacts are under
`/private/var/folders/w5/1l6hg4ks3wvcdv_bkztxwtyw0000gn/T/cvk-cleanair-manifest-726489024/`.
`creator-records.json` contains all four rows: two unchanged generation-2 creators
and two latest generation-8 applied Updates. `create-results-proven.json` separately
confirms both original creator task/UUID pairs; these were compared with the
stored creator rows after the live run. `applied-records.json` and `complete.json`
confirm appliedRecords=2, creatorRecords=2, knownTaskRecoveries=14 and cleaned=true.

Exact generated UUID cleanup tasks are
`01a14308-54cc-74fa-8cb5-028294a95096` (CleanAir) and
`01a14308-5649-706e-b0b1-cebca70db6af` (FRA). Both finish and restore their summary
baselines. Qualification cleanup does not publish deletion/tombstone receipts or
reconcile the removed definitions again. The native lifecycle uses a Kubernetes
client fixture, with actual API persistence separately checked above. No WLC/AP
association, device deployment or installed worker operation is attested.
