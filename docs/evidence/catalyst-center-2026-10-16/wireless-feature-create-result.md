# Native feature create-result identity — 2026-10-16

A new adapter-private result verifier supplies the native identity prerequisite
for future durable feature ownership. It reuses the existing REST transport and
shared task ID/time/failure/completion decoder. It requires service `NCNC`, a
nonzero canonical UUID string in `data`, and exact action-specific native progress.
Create binds the declared design name; Update/Delete additionally require the
original target UUID. Opaque text, object/embedded JSON/uppercase/zero UUIDs,
foreign service/action/name/task/time and failed tasks cannot produce a result.
Pending tasks produce no identity.

The private Create verifier then queries the selected native collection, requires
the task-returned UUID on the expected name, rejects system designs and verifies
desired typed fields using the existing reader/matcher. Absent, replaced,
wrong-collection or drifted metadata cannot become a proven Create result.

This is a prerequisite, not public ownership integration. The normal public
Create verifier, journal release rules and ownership/pruning capabilities are
unchanged. The helper does not publish an ActionApplication, bind an owner/source
CAS, grant deletion authority or enable uncertain/taskless Create recovery.
A caller must still preserve the original operation and authoritatively bind
owner/source/controller evidence before any durable ownership claim.

## Evidence

Read-only inspection of known generated historical create/update/delete tasks
confirmed `serviceType=NCNC`, `data=<native UUID>` and progress strings naming the
created/updated design or deleted UUID. The decoder's action-specific refusal
matrix and Create readback tests pass with race detection (2.190s,
`/tmp/cvk-feature-create-result-tests.log`). Matching names alone, a same-name
replacement UUID, protected design and altered fields are explicitly refused.
Update/Delete format guards are unit-tested; new native qualification below is
for Create identity only, not ownership or destructive action authority.

The [live recorded run](wireless-feature-create-result-native.log) captures each
original Accepted Create reference from the public config journal, preserving its
task ID and submission time. After the initial combined manifest reaches InSync,
the private helper proves both task-returned UUIDs against their original task
reference, selected collection/name and manifest-derived fields:

| Generated definition | Original create task | Task-returned/readback UUID |
| --- | --- | --- |
| `CVK_MB_16033455` (CleanAir) | `01a142c6-f5f0-714e-a06c-39aa6d6395b8` | `faf649bd-1fdc-49e1-bc27-bee1b5165bd7` |
| `CVK_MB_16033455_FRA` (FRA) | `01a142c6-f156-7eeb-8161-592053c3dea6` | `ae3b2ffc-d767-468f-b935-b3c78aa040d5` |

The existing seven-revision public cycle and twelve serialized acknowledged
Update recoveries then complete, with one create, six PUTs and one exact cleanup
DELETE per kind. Cleanup tasks `01a142c7-5551-7eb3-b50a-ac615676efb3` (CleanAir)
and `01a142c7-56b2-77ee-908a-850cab8ccf46` (FRA) finish and both full summary
baselines are restored. Native test 29.11s, race package 31.243s.

Private evidence:
`/private/var/folders/w5/1l6hg4ks3wvcdv_bkztxwtyw0000gn/T/cvk-cleanair-manifest-2750285781/`.
It retains both original `create-reference-*.json` files, the UUID proof in
`create-results-proven.json`, ordinary recovery/readback/cleanup records and
`complete.json`. The proof explicitly records durableOwnershipPublished=false;
the final summary records createResultsProven=true and cleaned=true. The final
controller snapshot has no operation and zero ActionApplications, confirming no
durable ownership receipt was published by this qualification. Native
qualification uses a Kubernetes client fixture; no new persisted API field or
installed worker rollout is attested.

Feature, common journal, discovery and IPAM regression passes 32.319s
(`/tmp/cvk-feature-create-result-regression.log`). Complete model, ND, IOS XE software lifecycle, software-upgrade and migration
checks pass (`/tmp/cvk-feature-create-result-foundations.log`). Final result
refusal cases and report/apply example compilation pass 2.583s
(`/tmp/cvk-feature-create-result-final-tests.log`). Strict documentation and
whitespace checks pass. No source pruning, adoption,
owned updates, automatic no-dispatch retirement or full-model parity is claimed.

## Required follow-through

Before public ownership/pruning can be enabled, bind this verified native result
to the unchanged owner/source/controller operation in the existing atomic receipt
publisher. Reserve receipt capacity before dispatch, require durable result
retention before journal release, reject foreign/duplicate/pruned lineage and
preserve legacy task protocols. Owned updates, removals, dependency/association
checks, tombstones and recreation need separate lifecycle qualification. Matching
metadata or the private result reference alone cannot authorize those actions.

Subsequent public creator-receipt publication, capacity/release guards and
acknowledged new-protocol Create recovery are qualified in
[creator ownership evidence](wireless-feature-creator-ownership.md). The private
qualification above remains historical; it did not publish ownership at that stage.
