# Owned discovery source pruning and reintroduction

Date: 2026-10-16. Branch: `pr/johalley/catc-support`.
Pinned NetAsCode module: 0.5.0, commit
`a435555fe828f03cf3311d52fe1ab6572a247916`.

## Execution and ownership contract

The pinned `cc_discovery.tf` consumes `try(local.catalyst_center.inventory.discovery, [])`.
CVK now accepts explicit empty lists and an empty selected inventory object for
Report/Retain Apply and discovery-only source pruning. Null/unknown fields remain
invalid. Under Delete, all omitted jobs with this config's verified creation
receipts are considered; imports and other configs' records are preserved. Retain
preserves omitted jobs. Keep the original config UID/name/namespace.

The existing owned-discovery executor now plans pruning before new writes, using
the same section ownership, task journal, transport, task observer, atomic result
publisher and receipt release gate. A source-removal operation appends `:prune`
to its original non-secret fingerprint. This distinguishes a removal tombstone
from an intermediate same-input replacement deletion. Recreation after pruning
requires a later bound config/source revision. No new public API fields,
controller, worker, ownership database or infrastructure-provider abstraction were
introduced. The existing IPAM final dispatch guard and input-revision comparison
were extracted into adapter-local shared helpers and reused without changing
IPAM semantics. Existing mask-less and masked discovery records keep their prior
interpretation; the new suffix is accepted only for Delete. An older actor without
this protocol cannot interpret a pending source-prune record, so recovery needs
an actor that understands the marker.

Before any cohort write, both retained desired jobs and obsolete owned jobs must
have their original native ID/name and selected-field fingerprint, with Complete/Inactive
state. Missing owned jobs, external replacement/rename, legacy mask-less ownership,
incomplete native data or drift refuse mutation. References to original registered
credentials are compared through their original IDs; empty cleanup does not need
to resolve current credential names. Unrelated jobs and unselected fields do not
force a stale-snapshot failure.

Native snapshots are repeated before one-use submission. The final durable check
reobserves the config/source revision, controller UID/generation/pause state,
section owner, exact Submitting operation and the selected result receipt plus
capacity. Denial and uncertain acknowledgement consume the dispatch closure.
Known-task Unknown recovery observes the retained task without another request.
Completion needs task success and two complete reads proving both the original
native ID and logical name absent; a same-name foreign replacement cannot satisfy it.
Only then is a deletion tombstone atomically published and the operation retired.

The [Cisco delete-by-ID contract](https://developer.cisco.com/docs/catalyst-center/2-3-7-5/delete-discovery-by-id/)
removes the selected discovery job. CVK uses that exact-ID endpoint, not a delete-all
endpoint or device inventory deletion. Discovery creation contacts the declared
lab target; no software upgrade or provisioning request is part of this test.

## Fixture and real API-server checks

Initial existing discovery/IPAM ownership checks pass (10.443s). The new public
prune/recreate/final-prune and dispatch tests pass (4.588s). The expanded discovery
and shared IPAM safety suite passes (12.888s). Cases include explicit/omitted
selection, Retain/no-write, opaque lost acknowledgement, backend refusal,
changed source, active/renamed/replaced/missing jobs, whole-cohort active-member
refusal, duplicate/legacy receipts, foreign tombstone replacement, source/owner/
receipt/pause changes and unrelated native churn. Same-input recreation is refused;
a later config generation or bound ConfigMap revision permits a new create.

Isolated real Kubernetes 1.35 API-server tests pass for discovery (6.10s) and IPAM
(6.11s; package 14.346s). They cover creation/tombstone schema preservation, atomic
publication, missing-result release refusal, stale-writer conflict, uncertain
journal retention, operation retirement and later-revision recreation. Native
proof is synthetic in those schema tests; they are not installed-worker or live
CC execution evidence.

## Native qualification

`TestLiveDiscoveryOwnedPruning` passes (66.27s; package 68.084s), with
`CATC_DISCOVERY_PRUNE=existing-lab-single`. Generated job:
`CVK_DISC_2bea31c352fe6861`, initial native ID `55`, reintroduced native ID `60`.
The public reconciler created, pruned from an explicit empty list, reintroduced
from a later source revision and pruned from an omitted discovery key. Each
acknowledged operation was JSON-persisted and restored as Unknown, then completed
without replay. Final native discovery inventory matches the original baseline;
target device ID/serial and inventory count remain unchanged.

| Revision | Action | Native task |
| --- | --- | --- |
| 1 | Create | `01a14236-4cc4-7b27-a0e2-08eb911fbebc` |
| 2 | Source-prune Delete | `01a14236-9f13-777b-a44d-624afd1636d8` |
| 3 | Reintroduce Create | `01a14236-bcef-75a4-87ea-5546155c0ee1` |
| 4 | Source-prune Delete | `01a14237-3204-77c9-a6eb-b488a4a85e84` |

Private evidence: `/private/tmp/cvk-discovery-owned-3860462764/`.
`complete.json` records cleaned/publicPrune true, two POSTs, two DELETEs and four
known-task recoveries. The final journal has no operation and one verified
source-removal tombstone. Native log: `/tmp/cvk-discovery-prune-native-final.log`.
No credential values or tokens are retained in these artifacts.

A separate partial-prune public test passes (test 0.69s, package 2.794s): an
obsolete owned job is deleted while a still-declared matching imported job and a
foreign config's valid result receipt remain untouched and unadopted. It was
added during the full regression run and checked separately. The complete Catalyst
Center race-test package passes (622.335s;
`/tmp/cvk-discovery-prune-package.log`). All production changes were present in
that run; the envtest-tag and additional partial-prune test results are recorded
separately above. Strict documentation validation passes and `git diff --check`
is clean. Kubernetes persistence in the native harness is simulated; the separate
API-server test above establishes only its stated schema/CAS/release properties.

## Harness failure and exact cleanup

The first live attempt created `CVK_DISC_50077fe21e93da3c` (native ID `50`, task
`01a14234-3b73-72ed-8e9e-239d9938e79a`) and recovered its acknowledged task, but the
harness used a stale config resourceVersion after its no-op checks. Its first source
revision failed the fake Kubernetes update before any prune request. The helper now
refreshes the config before every revision; no product CAS check was relaxed.

The retained job was removed using the existing qualification-only exact-ID cleanup
helper, after verifying original creation/task/native ID/field lineage. Task
`01a14236-009f-7f11-9cf4-15363ab5f2d2` completed, repeated absence restored the
original baseline, and the helper passed (2.01s). Private evidence:
`/private/tmp/cvk-discovery-owned-3880192030/`; cleanup log:
`/tmp/cvk-discovery-prune-retained-cleanup.log`. No registration, device inventory
removal or upgrade was performed.

## Remaining scope

Explicit stop/delete of active jobs remains unsupported by this executor; it
waits for Complete/Inactive even though the native delete contract can stop a job.
Mixed discovery/tag/template/device pruning, inline credentials, per-site discovery,
legacy ownership migration/adoption, unchanged-intent explicit rerun, whole-CR
deletion, owner handoff and installed actors remain pending. Tombstone capacity and
history lifecycle still need broader scale qualification. Discovery job convergence
is not device provisioning or report-mode discovered-device outcome parity. These
results do not establish full resource/section parity or branch merge readiness.


## Shared regression checks

ND, software lifecycle/SWIM, the NaC model catalog and migration-tool race tests
pass (migration tool 1.322s; other packages cached). Direct-device gNOI race checks
pass (cached). No non-Catalyst-Center provider code, upgrade backend, foundation
API or existing cluster state was changed.


## Follow-on active lifecycle

The original Complete/Inactive pruning boundary above is superseded for fully
receipt-owned masked jobs by [active lifecycle execution](discovery-active-lifecycle.md).
Creation ownership is now separate from unchanged-job completion; the exact-ID
API can stop/remove an owned active job. Unverified initial creates remain fenced.
