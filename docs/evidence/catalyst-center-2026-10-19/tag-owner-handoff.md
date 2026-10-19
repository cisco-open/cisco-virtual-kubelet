# Tag owner handoff and successor epochs

Subsequent qualification: [projected ConfigMap lifecycle](tag-configmap-lifecycle.md)
now covers native static source-revision, source-loss and handoff execution. The
source hashes and qualification limits below describe this earlier phase.

A new metadata-only tag config can receive the same section claim after the old
Delete-policy config is fully finalized and its UID is absent. Handoff requires
its durable retirement witness, complete unchanged receipt history, two repeated
native absence/catalog observations, unchanged incoming source/controller
snapshots and an optimistic controller status update. Handoff itself sends no
native request; normal planning and the common journal run on a later reconcile.

The old owner keeps every original operation, result and verification time inside
sealed archive wrappers. One short Delete result carries the owner frame within
the existing 512-byte result limit. Frames form an ordered prior-history digest
chain, preserving same-owner recreation ancestry as well. New Creates explicitly
bind the current predecessor seal. Verification rejects old native UUID reuse;
missing, changed or disconnected history cannot silently become a shorter audit.
No new CRD field, transport, write permit or physical-device actor is introduced.

Custody is bound to the successor UID, name, section set and retained-history
root. Source edits still use current ordinary source/journal fences; they do not
grant permission to switch resource family or section ownership. The next owner
can independently create, update, delete, retire and hand off again. Retain does
not authorize the next ownership transfer.

Use `tags-owned-deletion-apply.yaml` first. Wait for actual CR removal, then apply
`tags-owner-successor-apply.yaml`, which preserves the section set and logical tag
name under a new object identity. Imported tags are not adopted. An old CR still
held by any finalizer, unavailable retirement witness or native resurrection
keeps the ownership fence.

## Native qualification

All three scopes passed using an isolated Kubernetes API and the status-only
worker client. Each run closed the original owner and two same-name successor
owners. All nine original Create/Update/Delete tasks were moved through Unknown
and recovered without replay. Exactly three POST/PUT/DELETE requests, three
distinct native UUIDs and two sealed retired-owner cohorts remained per run.
The manager callback was checked before harness metadata removal; unrelated
finalizers were preserved at that boundary. Every old CR was then actually
removed from the isolated API. Full Catalyst Center tag baselines were restored.

| Scope | Native UUIDs in ownership order | Recovered tasks |
| --- | --- | --- |
| inventory | `73c0871f-d4e3-4391-9aa5-dafdbd6d01b0` → `8a62c6e1-f751-4137-8f26-c61d8a5bf940` → `e8e80e8f-6b22-41c6-a9f5-c9f04eacbce3` | 9 |
| combined | `b05ec394-706d-4aca-9861-969847623c33` → `88e07425-4cc6-4b61-832c-e27c3dcc1764` → `cc5790c1-b3c0-4322-aed3-eadd7106baf0` | 9 |
| templates | `ffec363d-66af-4577-b68e-87982ec303db` → `ca96bbd6-0e78-4b69-af71-1095576aad1b` → `891ac128-2d1f-4cb8-8faa-73460ec4a4ec` | 9 |

See [qualification data](tag-owner-handoff.json) and the
[final persisted inventory ledger](tag-owner-handoff-native-records/inventory/handoff-1-closed-and-gone.json).
This is isolated API/native controller qualification, not installed manager or
worker rollout evidence.

The controlled matrix also passes three repeated replacement cycles and refusals
for an active old UID, missing witness, resurrected old UUID, missing carrier,
missing entire history, changed history and a returned reused UUID. The real-API
matrix and archive integrity checks passed (77 pass events). Compatibility passed
with 1,486 pass events. The final claim-CAS/example gate also passed: lost
acknowledgement recovers without a second transfer, a concurrent status write
retries through fresh guarded CAS, and a changed incoming source prevents claim
transfer. Its native reader is stateless and permits no mutation. Counts include
parent/package pass events. ND, direct software lifecycle, maintenance, manager,
SWIM/Reload/XFSU/fallback and the main build passed.

## Still open

Section changes, legacy owners gone without witnesses, ConfigMap handoff, empty/recreated/dynamic owner
combinations, installed actors and existing shared scale ceilings need further
qualification or implementation. The full pinned 93-row model remains incomplete.

The native build used package source `5d9e3f43526432124eb3717bf24b51a452022f151314759a6d6b15945b8750af`.
A focused claim-CAS test file was added afterward; production source remained
identical (`732494eb6a62cf3060203648bf7c07c94be01a1aada74dbdf56c8cf6721a9768`).
Both corpus fingerprints and the test-only difference are recorded in the JSON.
