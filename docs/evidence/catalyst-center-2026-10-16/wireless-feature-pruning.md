# Public feature source pruning — 2026-10-16

Feature-only Apply now supports `prunePolicy: Delete` for omitted, CVK-created
CleanAir/FRA definitions. The config must retain the original owner UID and use
`deletionPolicy: Retain`. The existing modular config executor, REST transport,
durable journal and atomic action-receipt publisher are reused. No shared provider
contract, public CRD field or journal phase is added.

## Deletion authority and execution

Each candidate requires its original creator record and a complete native-state
fingerprint from the last applied record, or creator record if never updated.
Legacy UUID-only receipts cannot authorize deletion. Kind, logical name, original
UUID, non-system identity and the complete detail fingerprint must still match.
Missing resources are not assumed to be successful CVK deletions. Native imports
and foreign-owner records are left untouched.

The complete paginated wireless-profile inventory is checked for candidate UUID
references. Unmapped profile data or missing feature-reference metadata refuses
cleanup; references are not automatically detached. The existing strict profile
reader is reused. Cisco's [feature-template design guide](https://www.cisco.com/c/en/us/td/docs/cloud-systems-management/network-automation-and-management/catalyst-center/3-2-x/user-guide/cisco-catalyst-center-user-guide-3-2-x/b_cisco_catalyst_center_ug_3_1_x_chapter_01001.html)
describes profile attachment and provisioning for both designs. This bounded
implementation deletes controller-side definitions; it does not deconfigure
previously provisioned devices or claim physical WLC/AP dependency qualification.

Candidates are sorted by logical identity. All candidate tombstones must fit the
existing 256-record action capacity before the first Delete. This is cleanup
cohort admission, not an atomic transaction across all desired writes. Every
prepared Delete rereads the complete omitted cohort and profile references,
then revalidates the durable source, owner, controller and unchanged selected
receipts immediately before the native mutation. A prepared permit can be
consumed only once. Relevant changes refuse dispatch; unrelated profiles with
complete empty feature references do not block it.

The retained operation uses `feature-delete:v1:<kind>:<nameHash>:<nativeHash>` and
binds the original target UUID. Native NCNC Delete task identity/action/UUID and
two subsequent absence observations must succeed. An original UUID or same-name
replacement observed during verification prevents publication. Verified plus a
`feature-deleted:v1:<kind>:<nameHash>:<uuid>` tombstone are committed together.
The creator and last applied records are preserved unchanged. A generic metadata
Verified transition cannot complete this operation, and missing/malformed/pruned
tombstones cannot release a Verified journal.

Acknowledged Unknown Deletes resume observation of their retained task without
resubmission. Missing tasks, lost acknowledgements, failed/foreign task results,
source/owner changes and incomplete absence evidence remain fenced. Tombstones
are retained across subsequent no-op reconciles and prevent Delete replay.
Reintroduction after a tombstone remains fenced; recreation is not implemented.

The example is
`examples/configs/catalyst-center/wireless-feature-prune-apply.yaml`. It uses
explicit empty lists for both collections under the existing feature owner.
Retain remains the default. Null/unknown collections, secret-bearing/mixed-section
cleanup, a former mixed owner and CR-deletion Delete policy are not accepted by
this bounded feature lifecycle.

## Verification

The public lifecycle matrix covers successful two-definition pruning and retained
Delete recovery, Retain semantics, preservation of native imports and foreign
owners, referenced/incomplete profiles, externally edited/system/missing/replaced
resources, legacy receipts without state, full/cohort capacity, missing tasks,
lost acknowledgements, failed/foreign results, tombstone loss, simulated schema
pruning, and source/owner changes during proof publication.

Final-dispatch tests change a peer definition, add a profile reference, remove
receipts, replace an owner, revise the source or pause the controller after
preparation. Each case refuses native Delete. Stable and unrelated-profile cases
submit once; repeated calls to the same prepared write never submit again.
Generic Verified bypass attempts are rejected during public Delete verification.

Feature/shared-journal/discovery/IPAM race regression passes 63.715s:
[regression log](wireless-feature-prune-regression.log). The initial public matrix
passes 19.874s and the final expanded safety/example/dispatch selection, including protected
system designs and explicit import/foreign preservation assertions, passes 31.257s
([safety log](wireless-feature-prune-safety.log)).

Actual CRD/status persistence passes ten public pruning cases, including imports,
publication races, projected capacity, missing tasks and pruned/lost tombstones,
in 47.795s ([API persistence log](wireless-feature-prune-envtest.log)). API servers
are private and ephemeral; the native service is a controlled fixture. No
installed kubeconfig or stopped cluster data is used.

The first real-API run caught fixture errors, not successful qualification:
synthetic capacity rows lacked schema-required operation metadata, and a source
race incremented Generation without changing Spec, which a real API correctly
ignored. Rows now carry valid metadata and the race changes the source Spec.
Production verification/ownership gates were not relaxed.

Pinned model, Nexus Dashboard, direct-device software lifecycle, shared upgrade
and migration-tool race checks pass (`/tmp/cvk-feature-prune-foundations.log`);
unchanged packages use cached results. Strict documentation build and
`git diff --check` pass.

## Native complete lifecycle

The [live run](wireless-feature-prune-native.log) passes **eight** combined source
revisions in 38.02s (race package 39.934s). Seven revisions create/update both
independent definitions, verifying fourteen native-state fingerprints. The eighth
sets the same config's source to both explicit empty collections and selects
Delete pruning. Both accepted Deletes are serialized as Unknown before being
recovered and verified by the public reconciler. Total acknowledged-task recoveries
are **16**: two Creates, twelve Updates and two Deletes, with no repeated native
submission. Three later reconciles do not replay cleanup.

| Definition | UUID | Original Create task | Public Delete task |
| --- | --- | --- | --- |
| `CVK_MB_16050735` | `9a66ac99-3cc9-4402-bd12-c688e41c9df3` | `01a1431b-ccff-7261-91dc-34179c3de8b0` | `01a1431c-4975-79e8-83c7-dc98a563c7d6` |
| `CVK_MB_16050735_FRA` | `b63272a7-8b76-42ca-aee7-ee5a4f2eb9b5` | `01a1431b-c76c-7b8d-8f8b-5425c5fd2867` | `01a1431c-4f26-77aa-bc28-557714727850` |

Private artifacts:
`/private/var/folders/w5/1l6hg4ks3wvcdv_bkztxwtyw0000gn/T/cvk-cleanair-manifest-1371477624/`.
`prune-complete-state.json` contains six records: two unchanged generation-2
creators, two unchanged generation-8 applied Updates and two generation-9 Verified
Delete tombstones. `complete.json` records publicPrune=true, tombstoneRecords=2,
knownTaskRecoveries=16, manifestRevisions=8, nativeStatesChecked=14 and cleaned=true.
Each kind has exactly one POST, six PUTs and one DELETE. Both complete summary
baselines are restored. This cleanup uses public source pruning, rather than the
previous qualification-only direct teardown path.

Kubernetes state in the native run is a client fixture; real API persistence is
separately qualified above. The lab has no qualified WLC/AP deployment. Native
association creation/removal, concurrent profile/deployment transactions and
installed worker recovery are not attested. Profile-reference refusal is fixture
qualified; the native run uses generated, unused designs with complete observed
profile metadata.

## Remaining lifecycle

Tombstone-aware recreation/source reintroduction, CR-deletion cleanup, ownership
handoff, explicit legacy adoption/recovery, installed-worker and scale qualification
remain open. Arbitrarily lost post-release history is not reconstructed. Device
unprovisioning and automatic profile-reference removal are outside this standalone
definition-pruning stage. The resources and the full 93-resource pinned model
remain partial; no whole-section or whole-resource parity flag is enabled.


The subsequent [recreation stage](wireless-feature-recreation.md) closes explicit
source reintroduction and preserves previous lifecycle records through bounded
retired cohorts. Remaining-work statements above describe the prune-only stage.
