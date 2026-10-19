# Catalyst Center controller adapter

**Alpha: partial NetAsCode parity.** Start with the
[alpha operating contract and merge gates](catalyst-center-alpha.md) and
[remaining roadmap](catalyst-center-roadmap.md). The detailed qualification
history below does not establish complete resource or installed-device support.

The `catalyst-center` adapter connects a `NetworkController` to the Catalyst
Center Intent API. It uses the controller-neutral worker, credential projection,
TLS policy, session rotation, status conditions, and descriptor registry shared
by the other controller adapters. It does not change the Nexus Dashboard
adapter.

The descriptor is aligned to the Catalyst Center Network as Code model:

```text
format:  netascode-catalyst-center
stripe:  catalyst_center
sections: sites, network_settings, network_profiles, fabric, templates,
          inventory, wireless, lan_automation, system_settings,
          authentication_templates, feature_templates
model version: 0.5.0
```

These sections correspond to the upstream Catalyst Center data model. Version
`0.5.0` is the selected [NetAsCode release baseline](https://netascode.cisco.com/docs/changelogs/catalystcenter/changelog/),
not a claim of full schema validation. The shared adapter-local catalog now
includes all 11 top-level sections consumed by that pinned module. Use the
[configuration parity report and implementation sequence](catalyst-center-parity.md)
to distinguish model recognition from executable configuration support. A report-only `NetworkControllerConfig`
reconciler now supports bounded site attributes and site-bound NTP/DHCP/DNS/timezone/banner and telemetry profiles, plus AAA/ISE metadata, IPAM and global authentication templates, with explicit
rejection of unsupported fields and modes. Additional bounded Apply executors
are tracked resource by resource in the [parity ledger](catalyst-center-resource-parity.md);
section recognition does not establish complete schema or execution parity.
Native site and network-setting apply use a shared durable journal and requires matching CRDs, manager and worker; see
[site execution](catalyst-center-parity.md#native-site-apply). See the
[site reporting contract](catalyst-center-parity.md#site-reporting-contract) and
[network settings reporting contract](catalyst-center-parity.md#network-settings-reporting-contract). SWIM is a separate operational workflow and
does not introduce a new Network as Code section.

## Explicit site-default export

The migration tool can resolve the pinned floor geometry defaults and supported
site overrides before normal manifest execution. It preserves planned APs and
accepts explicit same-ConfigMap model/index/image snapshots, checking AP extents
against resolved floor geometry. It validates the resolved model with the current
parsers and keeps singular/bulk area parent semantics separate. Runtime defaulting, ownership and task handling remain unchanged.
See the [site-default export contract](catalyst-center-site-defaults.md).

## Discovery credential ownership and cleanup

The configuration reconciler can automatically transfer inventory ownership to a
new, manager-enrolled pure credential-discovery config with Delete retention.
The previous config must be gone and its complete retirement or no-submission
closure must carry the known history seal. Repeated native/source/projection
observation precedes one atomic metadata-only custody update. Creation uses the
existing journal and one-use dispatch permit on a later reconcile.

A successor's exact initial revision supports creation, original-task recovery,
Delete-policy cleanup, sealed retirement and manager finalizer release. A config
deleted while its first Create is still Prepared can retain that exact witness
and cancel without submitting. Its archived history remains sealed; a cancelled
queued version does not count as delivery. A later owner advances actual
delivered-version floors and can reuse a version that was never delivered.
Once the atomic cancellation proof is committed, completion can recover after
source/projection loss using metadata and repeated native absence. Before that
commit, the original source and projection must remain available. Legacy
unsealed, empty-history cancellation records remain manager-readable but cannot
authorize handoff. Mixed/unowned or unwitnessed empty cleanup stays fenced.

A source change before transfer requires fresh attestation and observation.
After transfer, bounded inline and ConfigMap revisions now reuse the current
source attestation and existing replacement/prune lifecycle, preserving the
immutable custody root and complete receipt history. Rotation, metadata changes,
job addition/removal, Retain omission and prune/recreate are covered. Reintroduced
credential leaves must advance retained high-water marks. An existing delivered
owner may now remove all discovery rows and projection references: explicit empty
inline/ConfigMap sources reuse typed-history validation, current scope and the
existing prune/Retain executor. Deletion and subsequent public handoff preserve
all receipts and delivered-version floors. Existing versioned owners can also
replace a retained job with global-reference or SNMPv3 NoAuthNoPriv metadata,
using an explicit v4 action-only incarnation and the existing journal. No empty
credential receipt or synthetic version is recorded. Reintroducing projected
credentials must advance historical delivery floors. Typed-history ConfigMap
sources retain their existing attestation path.

New SNMPv3 NoAuthNoPriv owners also support the manager-enrolled Delete lifecycle.
Use `examples/configs/catalyst-center/discovery-noauth-apply.yaml`; no Secret refs
or version labels are needed for metadata-only intent. Creation, original-task
recovery, cancellation and cleanup reuse the same compiler, journal and finalizer.
A metadata-only or never-submitted predecessor invents no delivery floor: the first
projected delivery or projected successor can begin at version 1. Both scoped
input formats use strict one-use refusal proofs; ambiguous/attempted requests
remain fenced. Five live read-only cancellation/refusal cases preserved the native
baseline with zero mutation attempts. See [NoAuth qualification and limits](../evidence/catalyst-center-2026-10-18/discovery-initial-noauth-candidate.md).

Initial NoAuth with explicit/default Retain now uses the same typed flow without
a cleanup finalizer. Use `examples/configs/catalyst-center/discovery-noauth-retain-apply.yaml`.
Deleting the config retains its native job and old UID reservation/audit; a new
manifest cannot inherit them. Targeted lifecycle/refusal tests and eleven live
read-only cases pass; the corrected full extension regression passes 2,806 checks. See [Retain qualification](../evidence/catalyst-center-2026-10-18/discovery-noauth-retain.md).
A generated native NoAuth job passed Create, original Unknown recovery,
manifest-driven Delete and manager release, restoring the native baseline.
The response reader preserves explicit versions and derives an omitted v3 only
from a recognised mode and nonempty SNMP user. Corrected full regression passed on the frozen source snapshot.
Ordinary/typed custody migration, native projected credential delivery, device
authentication and installed actors remain unqualified. See
[metadata-incarnation evidence](../evidence/catalyst-center-2026-10-18/discovery-metadata-incarnations.md). See [empty-source evidence](../evidence/catalyst-center-2026-10-18/discovery-credential-empty-source.md)
and [revision evidence](../evidence/catalyst-center-2026-10-18/discovery-credential-revisions.md).
See [public handoff and cancellation evidence](../evidence/catalyst-center-2026-10-18/discovery-credential-public-handoff.md)
and [successor cleanup evidence](../evidence/catalyst-center-2026-10-18/discovery-credential-custody-cleanup.md).

## Dual-stack IPAM reservations

Use `examples/configs/catalyst-center/ipam-dualstack-apply.yaml` as part of the
existing `network_settings` owner. Matching reservation names and types under an
IPv4 and IPv6 global pool form one site-bound dual-stack reservation. The selected
site must already exist; the pools must not overlap other allocations. IPv6 /64
reservations enable SLAAC according to the pinned model.

Native public reconciliation now qualifies creation, IPv6 parent DNS updates,
IPv6 reservation gateway updates, and an IPv4-only reservation revision that
preserves the existing IPv6 family. Accepted operations survive JSON-reloaded
simulated Kubernetes persistence; restarted no-op and exact qualification cleanup
pass. Submission plans are one-use even after failed preflight or missing task
acknowledgements. New public creates retain task-correlated native UUID receipts;
owned updates preserve that identity. Removed/replaced owned resources are
refused. Known acknowledged tasks can recover from Unknown without replay. The final
submission check repeats the durable journal/source/owner/capacity guard; owned
reservations retain the original site UUID.
A separate isolated Kubernetes 1.35 test verifies receipt schema/publisher CAS
and retirement guards; it does not qualify installed workers.
The binding reuses the same IPAM executor in network/credential/fabric plans;
legacy journals and imported resources retain their prior behavior without
retroactive creation ownership. See [owned result evidence](../evidence/catalyst-center-2026-10-16/ipam-owned-results.md). The pure-IPAM source-pruning extension is described below. Resizing and
reparenting remain unsupported; the pinned model requires an IPv4 reservation
with optional IPv6. Installed workers and device services remain
unqualified. See [evidence and limits](../evidence/catalyst-center-2026-10-16/ipam-dualstack-manifest.md).

## Owned IPAM source pruning

After a pure-IPAM config has created verified UUID receipts, apply
`examples/configs/catalyst-center/ipam-prune-apply.yaml` as a revision of that same
config object. With `prunePolicy: Delete`, either `ip_pools: []` or the pinned
module's default-empty `network_settings: {}` removes its owned reservations
first, then unused global pools. Both forms are accepted in Retain Apply and
Report too; an omitted pool key does not imply Retain. `Retain` preserves omitted
allocations. Keep the config UID stable; deleting and recreating the CR does not
transfer ownership.

Release also affects inherited child-site visibility. The controller refuses
release while assigned addresses are in use; CVK additionally checks known
gateway, LAN-session and child-reservation references and the original UUID/site
bindings. Missing or replaced resources, incomplete references and uncertain
outcomes stay fenced. A successful task plus repeated absence produces a
retained deletion receipt. A later explicit source revision can recreate the
allocations with fresh task-correlated creation proof; unchanged input does not
replay deletion or recreate a tombstone.

This is whole-allocation source pruning for pure-IPAM configs. Mixed
credential/settings/fabric pruning, IPv6-family detachment, other field resets,
CR-deletion cleanup and owner handoff remain unsupported. Keep other resource
kinds in their existing owners and retention policies. Historical LAN references
and incomplete gateway-secondary metadata conservatively block cleanup. The
native prune/recreate/prune cycle passes 15 uncertain-task recoveries and complete
fixture cleanup; installed actors remain unqualified. See [pruning evidence and limits](../evidence/catalyst-center-2026-10-16/ipam-owned-pruning.md) and [default-empty qualification](../evidence/catalyst-center-2026-10-16/ipam-default-empty.md).

## Discovery ownership, replacement and source pruning

The discovery exporter can now resolve supported non-secret custom defaults
before the existing site selector/compiler using `--defaults FILE`. The pinned
bundle has no discovery defaults; explicit job values retain precedence, nulls
are refused, and runtime execution remains unchanged. See the
[defaults contract and examples](catalyst-center-discovery-defaults.md).


Discovery-only `inventory` Apply configs using registered global credential names
now retain the created native job ID in an existing action receipt after task
success and original ID/field readback. That receipt proves resource creation;
unchanged desired jobs still wait for Complete/Inactive before InSync. A later
manifest change to the same owned job, including an active job, follows verified
ID deletion, confirmed absence and creation of the revised job. The original
selected native fields must still match their receipt; imported, foreign,
or drifted jobs cannot authorize replacement. Existing matching imported
jobs remain observable without becoming owned.

Use `examples/configs/catalyst-center/discovery-apply.yaml`, then the same-config
revision in `examples/configs/catalyst-center/discovery-replacement-apply.yaml`.
For source removal, apply `examples/configs/catalyst-center/discovery-prune-apply.yaml`
as a revision of the same config: `prunePolicy: Delete` with `discovery: []` or
`inventory: {}` prunes only that config's verified jobs. Retain keeps omitted jobs.
Independent creates and owned removals can progress while an unchanged retained
job stays active. A no-write cohort still waits for every retained job to complete
before InSync. Use `examples/configs/catalyst-center/discovery-cohort-apply.yaml`;
see [cohort qualification](../evidence/catalyst-center-2026-10-16/discovery-cohort-progress.md).
The entire selected/obsolete cohort is checked before the first write; jobs must
still match their original ID and selected fields. The exact-ID delete API stops
and removes an active job too; a failed runtime job does not strand owned cleanup.
A successful task and repeated ID/name absence produce a retained tombstone.
A later bound input revision can reintroduce the job with new task/ID proof;
empty intent never deletes imported jobs. Mixed inventory/template pruning,
whole-CR deletion and owner handoff remain unsupported. Unverified initial
creates require recovery before source changes can proceed. See [pruning qualification](../evidence/catalyst-center-2026-10-16/discovery-owned-pruning.md).

Inventory tags/discovery and combined template/inventory definition/tag paths now
retain discovery ownership and support exact-ID replacement using their existing
group/journal namespaces. Both selected sections must stay owned by the same config.
Adding templates to an existing pure owner preserves the original discovery proof;
legacy acknowledged work keeps its prior recovery path without automatic adoption.
Unqualified nested/private device paths retain prior behavior. See [mixed ownership
qualification](../evidence/catalyst-center-2026-10-16/discovery-mixed-ownership.md). Inline
credentials, per-site discovery scope and installed-worker replacement remain
unqualified. Known-task uncertainty is recovered through the existing journal
without resubmission; unknown outcomes without an acknowledged task remain fenced.
See [replacement qualification](../evidence/catalyst-center-2026-10-16/discovery-owned-replacement.md) and [active lifecycle evidence and boundaries](../evidence/catalyst-center-2026-10-16/discovery-active-lifecycle.md).

## Floor design and planned AP positions

`sites.floors[].planned_access_points` uses native map-model names and antenna
patterns, which can differ from inventory product IDs. CVK validates declared
models, selected bands and antenna patterns against the controller's maps
catalog before site writes and again before placement submission. Numbered
catalog choice labels are normalized during validation only; requests retain the
explicit manifest value.

Planned-position coordinates are in feet, even when floor geometry uses meters.
The lab-qualified example uses `AP9130I`, band 5, antenna
`Internal-9130-5GHz`, and Z=4 feet. CVK checks physical extents using normalized
declared future geometry before site writes and fresh native floor identities
and bounds before placement submission. Native minimum-height rules can be
tighter; the lab requires Z at least 3 feet, which is not generalized to every
release. See [extent preflight](../evidence/catalyst-center-2026-10-15/planned-ap-extent-preflight.md), `examples/configs/catalyst-center/planned-ap-apply.yaml` and
[native qualification](../evidence/catalyst-center-2026-10-15/planned-ap-native-qualification.md).
Create and position update passed through public manifest reconciliation with
simulated Kubernetes persistence and worker restarts. This changes map design;
operational AP provisioning, radio replacement and public prune/delete remain
separate unfinished capabilities.

## Metadata-only SNMPv3 credentials

An explicit `snmp_mode: NOAUTHNOPRIV` definition under
`network_settings.device_credentials.snmpv3_credentials` uses the existing
configuration journal and an action receipt containing its verified native UUID.
No secret reference or version is required. Add the definition to the existing
`network_settings` owner, or use
`examples/configs/catalyst-center/snmpv3-noauth-apply.yaml` when no owner exists.
The example creates a definition without assigning it to a site or device.

Public create, username update and restarted no-op are native-qualified with
simulated Kubernetes persistence. Mixed versioned/metadata-only manifests,
projection lag, drift repair and recovery refusals are fixture-tested. Unowned
definitions and replaced UUIDs are refused. See the original
[metadata evidence](../evidence/catalyst-center-2026-10-15/snmpv3-noauth-manifest.md).

An existing CVK-owned NOAUTH definition can now be promoted to AUTHNOPRIV or
AUTHPRIV by editing that same config and adding the required versioned secret
references. Preserve the config UID and credential name; use
`examples/configs/catalyst-center/snmpv3-promotion-apply.yaml` as the AUTHNOPRIV
revision of the NOAUTH example. Authentication and privacy values use the normal
manager-delivered projections, never inline model fields. Later AUTHPRIV revisions
can add privacy and rotate passwords through the existing version counters.

The original metadata receipt remains as origin proof; the ordinary versioned
receipt records the promotion and later rotations. Initial promotion requires
unchanged original NOAUTH metadata and its exact native UUID. Recovery and receipt
publication recheck that lineage without resubmitting the task. NOAUTH →
AUTHNOPRIV → AUTHPRIV → authentication rotation is native-qualified with four
original-task recoveries, same-UUID readback, no replay and exact fixture cleanup.
A separate native run also qualifies direct NOAUTH → AUTHPRIV followed by
rotation and three original-task recoveries. Both generated definitions were
cleaned up. Real temporary-API persistence is qualified separately from the native runs;
installed promotion and actual device authentication remain unqualified.
Downgrading to NOAUTH, removing a previously versioned destination, adoption,
recreation and destructive credential lifecycle remain unsupported. See
[promotion evidence](../evidence/catalyst-center-2026-10-19/snmpv3-metadata-promotion.md).

## Site-scoped AAA settings

Site AAA settings require compatible registered authentication-policy servers.
For plain AAA, CVK checks all selected primary/secondary addresses, server type
and protocol before planning and repeats the native UUID/capability check before
one-use submission. Missing registrations also block earlier writes in combined
network models. Registrations are managed through the existing `system_settings`
owner; site settings use `network_settings`. Add entries to existing section owners
rather than creating competing configs.

Use `examples/configs/catalyst-center/aaa-prerequisites-apply.yaml` before
`examples/configs/catalyst-center/aaa-apply.yaml`, with administrator-authorized,
versioned secret projections. Credentials stay outside the model. Both explicit
AAA groups remain required. Public null inheritance and empty unset/prune remain disabled. Private reset staging found native validation and readback limits; it retains ownership instead of treating task success or parent-equivalent metadata as cleanup proof. See [reset compatibility evidence](../evidence/catalyst-center-2026-10-15/aaa-owned-reset-staging.md).

Empty-area initial apply, secret rotation with projection-lag refusal, secondary
server addition/removal and restarted no-op are native-qualified through public
reconciliation with simulated Kubernetes persistence. This does not qualify real
authentication traffic, installed workers, ISE PSN/PAN relationships or durable
registration lineage. See [evidence](../evidence/catalyst-center-2026-10-15/aaa-site-settings-native.md).

## Switching network profiles

Switching profile writes retain native template project/UUID dependencies in the
existing journal and recheck the full selected profile set before one-use
submission. Inherited site settings are preserved and compared privately. Only
unique-key attribute envelopes are normalized for API ordering; opaque values
remain unmanaged and never enter status or dependency hashes.

Public create, template addition/removal/restoration and assignment to an empty
area now reach InSync and restarted no-op against the lab API with simulated
Kubernetes persistence. Installed actors and device deployment remain unqualified.
Legacy hashes that cannot prove project lineage stay fenced; exact empty legacy
membership remains recoverable. See
[qualification](../evidence/catalyst-center-2026-10-15/switching-profile-project-binding.md).

New public switching Creates retain task-correlated creator UUID receipts through
the existing atomic action ledger. The complete missing selection must fit the
256-row shared ledger before any new Create. Matching task/UUID/source evidence
is required before releasing the journal; missing, corrupt or pruned publication
remains fenced. Original creator receipts protect the retained UUID across later
Updates and Retain omission/reselection. Existing imports and legacy in-flight
Creates acquire no retroactive ownership. No manifest option or new status schema
is needed. Creator identity alone does not grant adoption, pruning or config-deletion
cleanup; latest-effect baselines and the remaining lifecycle/installed gates are
still pending. See [creator custody qualification](../evidence/catalyst-center-2026-10-19/switching-profile-creator-custody.md).

Populated switching detail reads now preserve a complete private observation,
including unknown native fields, alongside the typed identity from one GET.
Dispatch and repeated recovery observations compare that complete state; only
qualified keyed attribute/membership collections are reordered. Opaque values,
array ordering and numeric precision remain protected. A comparison helper reuses
the approved `/network_profiles` key projection, but latest-effect ledger
publication and cleanup authority are not yet implemented. See
[native observation qualification](../evidence/catalyst-center-2026-10-19/switching-profile-native-observation.md).

Switching and wireless profile/site assignments share one executor. It freezes
the declared selectors, repeats the full selected assignment observation before
one-use submission, and reconstructs only the acknowledged pair for repeated
UUID-bound verification. Both empty-site native flows are qualified, including
no-op and exact test-fixture cleanup. The initial Retain-only qualification leaves installed actors
and broader hierarchy/inheritance behavior open; newer Delete-policy support is
described below. See
[assignment qualification](../evidence/catalyst-center-2026-10-15/profile-site-assignment-guards.md).

Switching and wireless profile/site associations also share pre-dispatch refusal
retirement and acknowledged Unknown-task recovery. The existing metadata journal
retains the profile UUID, site UUID, logical pair and original task. Recovery
checks the full task tree and stable readback of that pair through the common
source/owner/controller/journal bracket. Membership reads for later source pairs
cannot strand the acknowledged association. Profile and site inventory remain
paginated reads through the existing readers.

A guard-certified unsent operation can retire and replan; an API mutation error
or missing acknowledgement cannot. Foreign memberships remain preserved, and
partial ordinary readback stays in Verifying. Recovery alone adds no membership ownership,
unassignment, prune or physical-admission authority. No new manifest option is
required. See [public association recovery evidence and limits](../evidence/catalyst-center-2026-10-17/profile-site-public-recovery.md).

New public association additions also persist latest-applied evidence in the
existing `ActionApplications` ledger, atomically with Verified completion.
Imported matches and legacy acknowledgements are not adopted. The receipt binds
the original task, exact profile/site UUIDs, config owner and source; capacity,
identity or evidence conflicts retain the fence. With Retain, source deselection preserves
the row without removing the native association. Receipts grant no exclusive
creator or owner-handoff authority; explicit association-only Delete support is
described below.
See [application receipt qualification](../evidence/catalyst-center-2026-10-17/profile-site-applications.md).

Explicit `prunePolicy: Delete` now removes only previously applied profile/site
associations omitted from the current source. Update the existing owner config;
new config UIDs cannot adopt receipts. Explicit empty switching/wireless arrays
are supported, while null and unknown fields remain invalid. Keep
`deletionPolicy: Retain`: profile/site/tag definitions and CR deletion do not gain
destructive authority. The example is
`examples/configs/catalyst-center/profile-site-prune.yaml`.

Assigned devices below the selected site, PnP site/location or unresolved active
onboarding references, and wireless tags at related hierarchy scopes block removal with `ProfileSitePruneReferenced`. Clear those
consumers through their appropriate lifecycle first. Unavailable/unknown reference
evidence also refuses removal. The original acknowledged task and stable absence
must verify before a removal tombstone atomically replaces the application row;
missing acknowledgements remain fenced. Removal and absent-before-write
re-addition reuse the same bounded slot. Reappeared associations and missing or
replaced native identities require recovery. No new manifest option, transport
or controller is needed. See [unassignment qualification and limits](../evidence/catalyst-center-2026-10-17/profile-site-prune.md).

An optional existing projected `PrivateComparisonKey` reference at
`section: network_profiles`, `path: /network_profiles` enables task-bound private
baselines for site/policy tags and all-new policy cohorts. One atomic action row
binds the complete producer member set, observed UUIDs, source/task/owner and
keyed native/dependency comparisons. Imported matches and legacy tasks gain no
Create receipt. Key changes, native edits/replacements and logical aliases remain
fenced. Latest Update rows reuse their slots while preserving Create-task and
sibling proofs. No key value belongs in the model or status, and the controller
must approve the alias through its existing worker projection. See
`examples/configs/catalyst-center/wireless-tag-baselines-apply.yaml` and
[private tag evidence and limits](../evidence/catalyst-center-2026-10-17/wireless-tag-private-receipts.md).
These baselines do not grant tag deletion, membership-removal or owner-handoff
authority; association pruning retains its existing guards.

Global authentication-template updates preserve the native pre-auth ACL
description, matching the pinned module's ignored description field. Contract
ordering alone does not trigger a write. Selected identities and values are
rechecked before one-use submission; restarted verification remains bound to the
acknowledged profile UUID and versioned expected-result digest, including preserved
native Voice VLAN/BPDU values. Unknown bindings retain the journal for recovery;
recognized legacy operations retain their narrower verification. See
[result-binding qualification](../evidence/catalyst-center-2026-10-15/global-authentication-result-binding.md).
An unused Low Impact change/restore cycle passed
native task, InSync and no-op checks with simulated Kubernetes persistence.
Fabric overrides and installed actors remain unqualified, and an earlier raw
response discrepancy remains unresolved. See
[global template evidence](../evidence/catalyst-center-2026-10-15/global-authentication-templates.md).

Fabric authentication overrides now accept resolved Low Impact, Open and Closed
template values, including Closed-only `bpdu_guard`, and report observed drift.
They share the global authentication reader/executor and bind site, fabric and
profile UUIDs for recovery. Fabric-only Apply now uses the existing explicit
group runtime for root/descendant-zone member reservations, before/result/physical
binding, one-use dispatch, independent task/readback verification, atomic receipt
and shared settlement. The declared foundation must already converge and every
affected member must have a current managed CiscoDevice/Node identity. Public
cycles for all three profiles are fixture-qualified. The lab currently has no
fabric sites, so native qualification covers report/readiness/refusal only. See
[public lifecycle evidence](../evidence/catalyst-center-2026-10-15/fabric-authentication-public.md)
and the matching `fabric-authentication-report.yaml` / `fabric-authentication-apply.yaml`
examples under `examples/configs/catalyst-center/`.

An isolated native fabric trial remains unqualified: the appliance accepted a
No Authentication creation task but failed application lookup with `NCSP11008`
for `ConnectivityDomain`, including after local wired collection was enabled.
The package inventory lists SDA installed, so an absent package is not proven.
All test resources were removed through exact task/UUID readback. CVK now reports
correlated group task failures as `ControllerTaskFailed`, preserving the journal
and every reservation. Only safe machine codes and fixed guidance reach status.
See [native investigation](../evidence/catalyst-center-2026-10-15/fabric-empty-native-qualification.md).

## Inventory and template tags

Tags can be declared under `inventory.tags` or `templates.tags`. When a config
manages both sections, the inventory declaration takes precedence for the same
name. Keep both sections in the same config to preserve their shared ownership.

Metadata-only tag manifests now support owned source pruning with
`prunePolicy: Delete`. Use `examples/configs/catalyst-center/tags-owned-prune-apply.yaml`
and its `tags-owned-prune-empty-apply.yaml` revision on the same object. Original
creator/latest-state receipts, complete native state and empty device/interface/template
consumers are required. Original Delete task recovery and durable absence receipts
qualify cleanup; those source-pruning examples retain CR state by policy. Older writable-only receipts need an
explicit owned Update before cleanup. Newer inputs can recreate a pruned tag with a fresh UUID and complete predecessor
lineage ([recreation qualification](../evidence/catalyst-center-2026-10-19/tag-recreation.md)). Handoff and installed actors remain unqualified. See [source-prune qualification](../evidence/catalyst-center-2026-10-19/tag-source-pruning.md).

CR deletion is separately enabled by `deletionPolicy: Delete`; use
`examples/configs/catalyst-center/tags-owned-deletion-apply.yaml`, which keeps
source pruning at Retain and revises the same named object as the source-pruning
example to preserve its UID and ownership. Manager enrollment precedes Apply, and the worker
retains the finalizer until owned unchanged tags have no consumers, their
original Delete tasks and absence receipts are verified, and the bound cleanup
proof passes the manager callback. Imports remain retained. Known settled
receipts permit source-independent cleanup, while an uncertain original task or
unavailable original input remains fenced. Inventory preserves its existing
finalizer; template and combined scopes use tag cleanup. Three native static-tag
cycles pass through isolated real API persistence with one POST/PUT/DELETE each,
original-task recovery and restored baselines. Installed actors and finalization-after-recreation qualification remain pending. See
[CR deletion evidence](../evidence/catalyst-center-2026-10-19/tag-config-deletion.md).
Owned Delete-policy cleanup now also persists a spec/deletion/evidence-bound
retirement witness before the manager can release the CR. A lost status
acknowledgement recovers without replay, a late native resurrection blocks the
witness, and Retain invalidates retirement authority. Native qualification proves
that the witness and original claims/receipts survive actual old-CR removal in
all three tag scopes. The durable prerequisite is described in
[retirement evidence](../evidence/catalyst-center-2026-10-19/tag-ownership-retirement.md).
Same-section replacement configs now have an explicit handoff path after the old
UID is absent. It preserves sealed original receipts, repeats native absence,
atomically moves the claim and then uses normal journal preparation for a fresh
native UUID. Use `tags-owner-successor-apply.yaml` after the original Delete-policy
config is gone. Three native original-plus-two-successor cycles pass with
original-task recovery and restored baselines. Legacy migration,
installed handoff and additional combinations remain pending; see
[handoff evidence](../evidence/catalyst-center-2026-10-19/tag-owner-handoff.md).
ConfigMap-backed static tag lifecycle and same-section handoff now also pass
native tests in all three scopes. Three source UIDs and six revisions per scope
retain original operation bindings and reject stale snapshots; settled cleanup
works after source removal, and successors use recreated ConfigMaps. Use
`tags-projected-owner-execution.yaml`. Accepted work with lost exact input remains
fenced; installed projection remains unqualified. See
[projected-source evidence](../evidence/catalyst-center-2026-10-19/tag-configmap-lifecycle.md).
After complete Delete-policy retirement and old-CR removal, successors may now
exchange inventory, templates and combined tag scopes. The same sealed audit and
status CAS replace all old claims with the selected new scope, preserving other
owners. All six directional transitions pass native three-owner sequences with
ConfigMap recreation, original-task recovery and restored catalogs. Active-owner
scope edits and splitting the shared namespace remain refused. See
[section handoff](../evidence/catalyst-center-2026-10-19/tag-section-handoff.md).




Template-to-tag association operations now retain their original native project
binding in the existing journal resource key, alongside the template/tag UUIDs
already recorded in the operation. A restarted verifier checks that binding and
repeats native readback for only the acknowledged association. Legacy submitted
operations without a project binding remain fenced with
`OperationRecoveryRequired`; names alone cannot supply the missing evidence.
Unsubmitted legacy preparation can still be cancelled and replanned. See
[project binding evidence](../evidence/catalyst-center-2026-10-15/template-tag-project-binding.md).

Native definition lifecycle qualification covers networkdevice `AND`/`EQ`,
`OR`/`EQ`, AND-wrapped `LIKE` and `ILIKE`, direct `EQ`, and multi-value `IN` rules.
Unambiguous one-condition AND/OR inputs are lowered to their atomic condition;
unary EQ source forms have passed native qualification. Empty/ambiguous logical
groups and rules selecting both `value` and `values` fail before writes. Positive EQ evaluation, two-condition AND/EQ and unary AND/OR EQ source forms
are native-qualified with first-to-second-to-empty membership transitions. OR/EQ, AND-wrapped LIKE/ILIKE and multi-value IN positive transitions are also
qualified ([operator evaluation](../evidence/catalyst-center-2026-10-19/tag-positive-operators-and-task-binding.md)); broader combinations and their owned lifecycle remain unqualified. See
[operator evidence](../evidence/catalyst-center-2026-10-15/dynamic-tag-operators.md)
and `examples/configs/catalyst-center/dynamic-tag-operators.yaml`.

Explicit device and template tag assignments require a manual association.
A dynamic-rule match alone does not satisfy that intent: CVK reads STATIC and
MIXED association classes so the assignment survives later rule changes. Both
types reuse the same paginated reader, with no unfiltered fallback on failure.
Native device qualification verified manual/dynamic coexistence and removal of
only the manual fixture without suppressing the dynamic match. See
[association provenance evidence](../evidence/catalyst-center-2026-10-15/tag-association-provenance.md).
Native template qualification verified positive STATIC classification, serialized
operation recovery, no-op and exact cleanup; positive template DYNAMIC/MIXED rule
transitions remain fixture-qualified only. See
[template provenance evidence](../evidence/catalyst-center-2026-10-15/template-tag-association-provenance.md).

To clear an existing tag's rule definition, update its existing config object with
`dynamic_rules: []`. Omitting `dynamic_rules` preserves its current rules. The tag
itself remains under Retain policy. Same-UUID clearing and restoration have been
native-qualified on empty fixtures and directly from a matching switch population,
including restoration of the same tag and its dynamic membership. See
[matching-clear evidence](../evidence/catalyst-center-2026-10-15/tag-pagination-and-matching-clear.md). Use `examples/configs/catalyst-center/dynamic-tags-apply.yaml`
and its companion `dynamic-tags-clear.yaml`; see
[clear/restore evidence](../evidence/catalyst-center-2026-10-15/dynamic-tag-rule-clearing.md).

## Wireless profile site and policy tags

Wireless profiles under `network_profiles.wireless` can declare additive `sites`,
`site_tags` and `policy_tags`. Referenced sites and SSIDs must exist. A site tag
must supply `ap_profile_name` referencing an existing AP profile, unless an
existing tag retains a valid observed reference. Missing AP references fail
Apply with `MissingSiteTagAPProfile` before prerequisite writes or journal
preparation. CVK does not invent a default for this optional NaC input because
the native create/update APIs require it.

An omitted or empty policy-tag `ap_zones` selection maps to the native built-in
`default-zone`. Named custom zones must resolve against the parent wireless
profile. Before prerequisite writes and again before submission, CVK refuses
same-site site-tag conflicts and overlapping policy-zone scope on the same or
ancestor/descendant sites, including native tags absent from the source. Public
Apply reports `ConflictingWirelessTags`. Disjoint policy-zone selections are
allowed; partially intersecting selections remain conservatively fenced pending
native qualification. Custom Flex lookup, membership migration/removal and native
ancestor/custom-zone behavior remain unqualified. Use isolated fixtures for
deployment qualification. See the
example manifest at `examples/configs/catalyst-center/wireless-profile-tags-apply.yaml`
and [native qualification evidence](../evidence/catalyst-center-2026-10-15/wireless-tags-native.md).

Profile definitions and site/policy tag Create/Update operations now reuse the
shared one-use preflight guard and acknowledged-task recovery. A fresh guard can
prove that it refused before invoking the mutation client; public reconciliation
then retires only that exact unsubmitted operation, preserves ownership/history
and replans. A REST failure or lost acknowledgement cannot supply that proof.
An Unknown operation with its original task ID can be observed through the full
task tree and stable native/dependency readback, with unchanged source, owner,
controller and journal bindings. Taskless uncertainty remains retained.

The ordinary Accepted-to-Verifying boundary remains unchanged: partial native
readback stays in verification after task completion. Known-task recovery adds
no creator ownership or removal/prune authority. These paths use the existing
Retain manifests without new fields or annotations. Switching-profile definitions
retain their project-binding contract. Profile/site associations use the shared
recovery path described above. See
[recovery qualification and remaining limits](../evidence/catalyst-center-2026-10-17/wireless-profile-public-recovery.md).

Custom AP zones must use unique SSID/RF-profile-name pairs. CVK validates the
merged desired and retained zone definitions before writes and reports
`DuplicateAPZoneCombination` for duplicates. All-new policy tags for a profile
are created together in one bulk request through the existing metadata journal;
sequential single-item creates can replace another tag on the appliance. Updates
retain the individual tag UUIDs. Mixed existing/new declarations and additions
to an existing shared-site cohort are fenced with `PolicyTagCohortRequired`;
partial readback stays retained and never triggers another create. Native qualification rejected existing-name bulk upsert and empty-site policy definitions; see [expansion constraints](../evidence/catalyst-center-2026-10-15/wireless-policy-extension.md). See
`examples/configs/catalyst-center/wireless-policy-cohort-apply.yaml` and the
[custom-zone and cohort evidence](../evidence/catalyst-center-2026-10-15/wireless-custom-zones-cohort.md).

Singleton site/policy tags now pass two native deletion/recreation cycles,
including ordinary same-ID updates and membership expansion/reduction between
cycles. Four complete predecessor cohorts and all 26 original tasks are retained
without replay, and ordered cleanup restores the generated fixture baselines.
The bulk policy-cohort repeat run is still pending; Kubernetes actors were
simulated and installed workers/WLC/AP behavior remain unqualified. See
[repeated native lifecycle evidence](../evidence/catalyst-center-2026-10-18/wireless-tag-repeat-native.md).

Wireless SSID details can carry `vlan_group_name` through the profile metadata
executor. The name must refer to an existing design definition; this field does
not create a VLAN group. CVK binds the selector name and retains the ordinary
source, profile UUID and SSID identity guards. Positive native qualification,
authoritative group-definition lookup and WLC realization remain pending. See
[VLAN selector evidence](../evidence/catalyst-center-2026-10-15/wireless-vlan-selector.md).

## Current capabilities

Wireless profiles reject an effective combination of Fabric and FlexConnect
SSIDs with `MixedWirelessSwitchingModes`. Validation includes retained settings
and runs before journal preparation or configuration writes, including when the
invalid profile appears after another valid profile in the manifest. Explicitly
disabling an incompatible flag resolves model validation; native switching-mode
migration and device realization still require qualification. Separate isolated
FlexConnect create/local-VLAN update and Fabric create/same-mode metadata update
are native-qualified; see [mode evidence](../evidence/catalyst-center-2026-10-15/wireless-profile-modes-native.md).

- Health authentication through `POST /dna/system/api/v1/auth/token`.
- Read-only inventory through `GET /dna/intent/api/v1/network-device`.
- Read-only SWIM image inventory through
  `GET /dna/intent/api/v1/image/importation`.
- Internal SWIM distribution and activation request contracts, including
  asynchronous task IDs and task polling through `/dna/intent/api/v1/task/{id}`.
  Managed rollouts now delegate these operations through a durable
  `CatalystCenterSWIMHandoff`; final success requires native XE verification.

The adapter uses the existing CVK REST transport for TLS, rate limiting,
request execution, and error redaction. Credentials are read from the worker's
projected `username` and `password` files. Tokens are held only in the worker
process and are not written to Kubernetes status or logs.

Co-locating SWIM with managed XE devices requires credential Secret references
for every CiscoDevice in that namespace. The namespace RBAC audit accepts only
the canonical, live Catalyst Center worker binding and its exact compiled SWIM
role; inline device passwords, stale ownership, extra subjects, and broader
permissions remain blocking.

Device and image inventories use bounded, one-based pagination. Invalid,
missing, null or repeated pages fail the refresh without publishing a partial
inventory as successful. Image fields follow the
[Intent API image schema](https://developer.cisco.com/docs/catalyst-center/2-3-7-5/get-software-image-details/),
including `imageName`, `isTaggedGolden` and `imageIntegrityStatus`.
An image UUID lookup establishes presence only; it does not establish image
integrity, device compatibility or authorization to activate.

The adapter client also supports the modern device-image details and readiness
APIs. Product association binds the device UUID and management address to the
controller's exact product identifier, including supervisor where applicable,
then matches that identifier to the imported image's `mdfId`. The human-readable
family name and the catalogue's sometimes incomplete PID list are insufficient
on their own. Readiness results reuse the bounded inventory pagination helper
and are filtered by device and operation.

Readiness qualification requires matching task/device identities, fresh completed
validation evidence and explicit success for every returned check. Missing,
conflicting, warning, skipped or partial results fail qualification. A successful
parent task with no validation records is not a pass. The decoder accepts both
the documented top-level status and the 3.2.3 `resultDetails` STATUS entry, while
discarding free-form device output. The managed SWIM handoff runs these checks
before distribution and again before activation.

A completed but unsuccessful readiness run can be replaced after an explicit
parent reauthorization, such as pause/resume after verified remediation. The
new device-worker grant must differ from the previous one, at least one minute
must have elapsed, and each stage permits at most three submissions. Prior task,
claim, stage and submission-time receipts are retained in `readinessHistory`.
The new check uses the same write-ahead dispatch and fresh admission checks.
In-flight checks and uncertain submissions are never replayed. A repeated task
ID is rejected; retries do not waive any readiness check or activation gate.
This supports supervised recovery. Opt-in manifest-driven cleanup and inventory
synchronization are described in [SWIM remediation](catalyst-center-remediation.md).

Redirects are rejected for login, reads and writes. Only read requests with a
401 refresh the session once; 403 responses do not trigger another login, and
mutating POST requests are never retried automatically. Remote error bodies
are omitted while HTTP status classification is preserved. Status publication
uses direct Kubernetes reads and rejects stale UID/generation bindings, paused
controllers and terminating controllers. Enabled device adoption or an API
version other than `v1` is rejected rather than silently ignored.

## Managed SWIM execution

### Execution selection and task engine

The internal `softwarelifecycle.UpgradeExecution` contract distinguishes
`Direct` from `CatalystCenter`. Omission resolves to `Direct`, preserving the
existing gNOI choice. A controller execution must pin its namespace, name and
Kubernetes UID. Switching methods or controller incarnations requires a new,
separately authorized operation; an outage never causes automatic fallback.
Managed rollout image sources select `catalystCenter` explicitly; the manager
copies that binding into the generated `IOSXESoftwareUpgrade` leaf.

The adapter now has an internal SWIM task executor with this sequence:

```text
Pending -> distribution claim persisted -> Distributing -> Distributed
        -> activation claim persisted -> Activating -> Verifying -> Succeeded
```

Each POST requires a durable mutation-authority claim, an atomic write-ahead
dispatch marker and a fresh authorization check. Task IDs are recorded before
polling. Restarting with a dispatch marker but no receipt enters
`OutcomeUnknown`, retaining the device fence and never resubmitting the POST.
Missing tasks and failed polls also cannot authorize replay. Activation needs
a completed distribution; task completion then requires fresh, matching device
identity/version evidence before success is persisted and the fence released.
Unsupported or incomplete operation records fail closed.

### Managed handoff

The XE software-upgrade reconciler owns the existing maintenance session,
rollout reservation, canonical device mutation Lease, and stage claims. It
creates a same-namespace `CatalystCenterSWIMHandoff` journal and publishes a
short-lived stage grant in the parent upgrade's `status.controllerHandoff`.
The Catalyst Center worker consumes that grant and updates only journal status.
It cannot write the parent admission, acquire another Lease, or read device
credentials. The dedicated worker role permits read-only Lease access in its
namespace; the first supported deployment therefore places the controller,
device and canonical Lease in the same namespace.

The controller checks uncached parent/controller/device identities, maintenance
acknowledgement, exact live Lease UID/holder, grant expiry, policy/control
revision, pause/cancel state, maintenance window, and network-evidence deadline.
Every distribution or activation also requires the parent's matching durable
mutation claim. Inventory checks bind the serial, management IP, controller
device UUID, image UUID/version and product association immediately before
submission. The requested image must be the device's sole golden SYSTEM image,
since that is the target of the qualified readiness endpoint. A second live
grant check follows those inventory calls.

The journal uses Kubernetes resourceVersion compare-and-swap. A persisted
submission marker without its receipt becomes `OutcomeUnknown` after restart;
it never authorizes another POST. Unresolved mutations remain in canonical
quarantine, including across Lease expiry and deletion requests. Cancellation
blocks new stages but allows observations of accepted work. Controller UID or
configuration-generation changes fence the journal for investigation.

After Catalyst Center reports activation success, the XE worker uses the
existing gNOI Verify and native lifecycle inventory paths to prove the pinned
version is running and committed. It writes identity-bound native evidence to
the parent; the controller then marks the journal successful. Only then does
the XE worker settle the parent and allow existing maintenance recovery to
release the fence. Successful journals remain as audit records, with their
retention finalizer removed.

Install the generated CRDs, chart RBAC/admission policies, and matching manager
and device/controller worker binaries together before using the new source.
Manager startup verifies the new admission policy's compiled contract. The
`rollout-controller-swim-v1` handshake and `CatalystCenterV1` execution marker
prevent old workers from interpreting controller work as Direct/gNOI work.
Existing URL/device-file/preinstalled sources keep their existing paths.

Use the controller rollout example at
`examples/configs/catalyst-center/swim-rollout.yaml` in the repository
through the existing plan/approve/execute workflow. Each controller source pins
one device UUID: target one device, or provide separately selected sources for
different device UUIDs. This first execution profile requires single-supervisor
IOS XE, `Reload`, explicit `rollbackOnFailure: false`, and no byte-pacing policy.
It does not silently substitute direct rollback, ISSU, cleanup or transfer
pacing. Campaign SHA-256 remains the administrator's imported-image audit pin;
controller UUID/version and product/readiness evidence do not independently
prove a vendor signature or the imported file checksum.

The default live test reads health and inventories. An additional opt-in test,
`TestLiveCatalystCenterSWIMReadiness`, consumes persisted readiness receipts from
`CATC_READINESS_RECEIPTS` and optionally checks product associations for the
comma-separated `CATC_TEST_IMAGE_IDS`. It never starts or replays a POST.
The receipt file maps management addresses to `device_id`, `started_at` (Unix
seconds), and `receipt.response.taskId`. Receipts expire after 15 minutes for
qualification. An expired or unsuccessful run requires a new, deliberately
submitted readiness check; rerunning the test does not submit one.

[The 8 October lab record](../evidence/catalyst-center-2026-10-08/README.md)
records the deployed `.101` upgrade from 17.18.03 to committed 17.18.04 through
Catalyst Center, including native verification, health soak and fence release.
Cleanup and controller inventory synchronization were supervised in that run.
The subsequent [automatic remediation qualification](../evidence/catalyst-center-2026-10-08/automatic-remediation/README.md)
records manifest-triggered archive deletion, independently observed free space,
and a correlated successful inventory synchronization on `.101`. That separate
same-version qualification stopped at an xFSU readiness warning; it is not
evidence of another completed upgrade or a version-changing automatic cycle.

### Modern API execution profile

The managed executor supports the pinned `networkDeviceImages-v1` profile.
It persists a readiness dispatch marker and receipt before distribution and
again before activation, requires the six qualified XE checks, and observes
the exact per-device workflow (task, device, operation, controller image version
and time range). Empty workflow results remain pending. Final success still
requires fresh device-side version verification. Contract selection is immutable;
the legacy route remains separate and is never used as a retry fallback.

An optional `transferFallbackAddress` in `imageSource.catalystCenter` qualifies only the
exact 3.2.3 warning detail pair reporting positive HTTPS/SCP reachability and
failed NETCONF transfer for that literal appliance IP. It does not suppress
flash, startup, image, xFSU or unknown warnings. Omission blocks all warnings.
This narrow manifest option is not proof that a live transfer works.

These transitions have race-tested simulated coverage, including lost responses,
missing receipts, missing workflows and partial readiness. Kubernetes storage
and the manager/worker handoff are implemented. The supervised 8 October live
run qualified distribution, activation, native committed-image verification and
maintenance release on `.101`. Automatic retired-archive cleanup and inventory
synchronization are implemented through the opt-in preparation policy; their
qualification limits are described in [SWIM remediation](catalyst-center-remediation.md).
The adapter advertises managed SWIM when image inventory is available;
that capability alone does not certify other releases, platforms or remediation
policies.

### Qualified API profile and current limits

The opt-in `standardReloadProfile: CatalystCenter323` pins Catalyst Center build
`3.2.3-75346.100` and uses the modern device-image APIs. A live 17.18.04 to
26.02.01 campaign completed through distribution, activation, reboot, commit,
native verification and maintenance release. See the
[qualification report](../evidence/catalyst-center-2026-10-09/standard-reload/README.md).
This qualifies normal reload on that build/path, not xFSU or other versions.
The legacy internal execution contract remains separate; an ambiguous modern
submission never retries through the legacy API.

Unknown readiness warnings remain blocking. The explicit reload profile may
classify only the observed, target-bound cross-version xFSU warning as
non-applicable. The separately selected `transferFallbackAddress` qualifies only
the exact positive HTTPS/SCP warning described above. Neither option suppresses
flash, startup, image compatibility or device eligibility failures.

## Manifest-driven preparation and recovery

Use [SWIM preparation and remediation](catalyst-center-remediation.md) and the
examples in `examples/configs/catalyst-center/` for the implemented contract.
The Catalyst Center source optionally pins an immutable administrator-owned
ConfigMap by name, UID and SHA-256 under `preparation`. The supported policy is
`RetiredCVKImageArchivesV1`; this is CVK operational policy, not a new NetAsCode
configuration section. No target manifest accepts arbitrary cleanup commands.

The device worker observes native state and can remove only exact retired CVK
image archives backed by invalidated preparation receipts. It verifies size,
digest, boot/install references, current/target image protection, empty application
inventory and cumulative file/byte limits before issuing a durable one-shot
removal. Catalyst Center then refreshes inventory, verifies the exact device's
sync completion and runs fresh readiness checks before distribution and again
before activation. A lost mutation response is reconciled from observations;
unknown outcomes retain the fence and are never blindly replayed.

Reserve both incoming archive and extracted-package space before distribution,
in addition to the pre-activation floor and headroom. If eligible archives cannot
satisfy that budget, the workflow stops. Application archives, installed packages,
route/VRF repairs and arbitrary file cleanup are outside this policy. Required
network configuration must converge separately through the existing drivers.

The [9 October uninterrupted qualification](../evidence/catalyst-center-2026-10-09/merge-cycle/README.md)
completed 17.18.04 → 26.02.01 on the empty single-RP `.101` switch, including
actual retired-archive cleanup, both inventory synchronizations, SWIM activation,
native committed-image verification and maintenance release. Control revision
remained zero after approval. The target archive/packages were already cached;
this does not qualify a fresh-transfer cycle or xFSU. Earlier supervised cleanup
and recovery runs remain separately identified in their evidence records.

### Cleanup ownership and protection

Catalyst Center documents
[automatic flash cleanup during distribution](https://www.cisco.com/c/en/us/td/docs/cloud-systems-management/network-automation-and-management/catalyst-center/3-1-x/user_guide/b_cisco_catalyst_center_user_guide_3_1_x/b_cisco_dna_center_ug_3_1_x_chapter_0100.html).
Its native behavior may remove unused image/package/configuration files until
space is available. This does **not** establish that its API supports CVK's
per-file allowlists, byte limits or retention exclusions. Capability validation
must establish those guarantees before a `CatalystCenter` cleanup action can
be admitted. A policy the selected executor cannot enforce is unsupported,
not permission to weaken the policy or fall back to a different executor.

If native cleanup cannot enforce the policy, use an explicitly selected,
separately coordinated device preparation action through the existing device
worker. Controller credentials remain in the controller worker. The upgrade
still executes through Catalyst Center; choosing a direct preparation action
must not silently change the upgrade executor or introduce another device
credential/session stack in the CC adapter.

Mandatory exclusions cannot be disabled by target manifests: active/committed
images and boot files, the selected target image, rollback images retained by
policy, files referenced by unresolved operations or valid preparation receipts,
and app-hosting packages/storage. An "inactive" install package is not
automatically safe to delete: it can be the rollback or a prepared target.
Do not translate `RemoveUnreferencedImageFiles` into blanket `install remove
inactive`, wildcard deletion or deletion of `.conf` files.

Bind each candidate to the device UID/serial, filesystem/member, exact path,
size/digest and inventory revision. Recheck references immediately before
dispatch while holding canonical device mutation admission. If files or
references changed, invalidate the plan instead of selecting replacement files
under the old authorization. Post-upgrade cleanup is a separate action after
successful verification and the required rollback/soak retention period.

### Integration with the existing upgrade flow

The logical sequence is:

```text
Observe -> Classify checks -> Plan authorized remediation
        -> Claim existing device mutation admission -> Execute
        -> Verify remediation -> Fresh readiness
        -> Admit distribution/activation -> Verify device and workloads
```

Remediation reuses existing canonical device coordination, maintenance policy,
topology reservations, manager/device identity binding and durable mutation
claims. It must not acquire an independent "cleanup lease" or use the controller
namespace as the device lock key. Apply drain requirements appropriate to the
action; do not assume that controller-owned distribution is only a byte copy.
The manager records the action plan before dispatch; the executor records its
task receipt and outcome. Status should expose the finding, action, executor,
candidate files, reclaimed bytes, attempts and fresh verification timestamps.

A lost response or expired timeout is an uncertain outcome: preserve the fence
and reconcile evidence, rather than replaying deletion or distribution. Known
remaining insufficient space stops the upgrade after the bounded action;
there is no unbounded cleanup/retry loop. A readiness retry is a new observation
job after a settled run, not permission to replay an ambiguous mutation.

Implement this first in the CC operational integration and use the existing
coordination hooks. ND and default Direct/gNOI behavior remain unchanged.
Qualify protected-file exclusion, insufficient-space-after-cleanup, changed
inventory, stale policy/controller/device bindings, duplicate submissions,
lost task receipts, restart recovery and concurrent Direct versus SWIM claims
before enabling mutation from these proposed manifest fields.

### Live test preparation

After replacing workers, wait for the manager to accept a fresh complete network
observation before submitting a rollout with network health enabled. A failed
planning attempt is retained; submit a new plan once its prerequisite is met.
Use `BlockIfRunning` for an empty target, or the existing policy-bounded `Drain`
workflow for workload migration. A replacement that cannot activate must block
PDB-aware drain; it is not evidence of a SWIM failure or permission to bypass
that gate. Cancellation must settle before another upgrade targets the device.

If cancellation leaves an unowned CVK maintenance taint, an infrastructure
administrator can request its removal by annotating the CiscoDevice with
`operations.cisco.vk/cancelled-drain-taint-release=<leaf-UID>/<session-token>`.
Use the exact retained operation UID and session token from
`status.maintenanceSession`. This is an explicit recovery action, not automatic
SWIM remediation. The manager requires a validated, cancelled, recovering
workload-drain session, an unexpired recovery deadline, all selected pods closed,
and a wholly idle, request-free canonical lease. Software mutation claims,
controller handoffs, pre-existing taints, and desired CiscoDevice taints prevent
this action. Only the exact `cisco.vk/device-maintenance=gnoi:NoSchedule` taint is
removed; unrelated taints and all lease/admission state are preserved. The
annotation remains as audit evidence and cannot match a different session.


### Provisioning before day-N templates

Scoped inventory Apply waits ten seconds after provisioning dependencies before
regular or composite day-N template submission. All declared wired provisioning
targets participate, including targets without a template. Verified provisioning
receipts provide the persisted start time; already-provisioned devices without
a current receipt use the exact template operation's Prepared timestamp. A
one-second serialization margin prevents early release after Kubernetes stores
whole-second timestamps. Worker restarts preserve the interval. No process-local
sleep or new CRD field is required. Existing reservation, source/owner and native
identity checks remain in force. Full wireless/bulk composition and actual
provisioning-to-template lab qualification remain pending; see
[pacing evidence](../evidence/catalyst-center-2026-10-15/template-provision-pacing.md).


### Explicit-scope extranet policies

Fabric-only Apply can create policies and expand owned subscriber/fabric lists
through the existing `controller-configuration-group-v1` runtime. Every affected
fabric/zone-parent/VN member requires a matching managed CiscoDevice/Node identity
and the normal all-member reservation authority. Native metadata prerequisites
must already converge. The lifecycle uses persisted preparation, one-shot dispatch,
full task-tree and readback verification, atomic receipts and shared settlement.

See `examples/configs/catalyst-center/extranet-apply.yaml`. The model
`fabric_sites` list bounds native scope; `spec.scope: global` remains the existing
configuration ownership scope. Omitted native scope and destructive transitions
are gated. Public inline/ConfigMap cycles are fixture-qualified; installed actors
and accepted native fabric mutations remain unqualified because the current lab
has no fabric fixture. See [extranet lifecycle evidence](../evidence/catalyst-center-2026-10-15/extranet-public-recovery.md).


PnP site claiming remains gated for public execution. Its private owned recovery
observer now compares requested workflow image/template references and detects
changes inside task work items through the shared safe PnP inventory reader.
Missing references remain pending; matching IDs alone do not prove completion.
The lab currently has no PnP records, so positive native claim qualification is
still pending. See [PnP recovery evidence](../evidence/catalyst-center-2026-10-15/pnp-workflow-reference-recovery.md).


PnP-only inventory Apply now processes database registrations through the public
reconciler using the existing journal and atomic UUID ownership path. It checks
all declarations before choosing one write and reuses shared inventory reads.
After registration converges, status remains Waiting/Ready=False with
`PnPClaimAdmissionUnavailable`; it does not mark onboarding InSync. The bounded
capability is `pnp-registration-stage`. Private and public creation share the same
whole-cohort admission and dispatch factory, pinned model/retained-Apply scope
checks and frozen source revision. A private-created UUID resumes through the
ordinary public journal reconciler; see
[shared admission qualification](../evidence/catalyst-center-2026-10-18/pnp-shared-admission.md). The public stage budgets the complete
remaining cohort against shared ownership-receipt row and byte capacity before
registration. `PnPRegistrationCapacityUnavailable` preserves existing ownership
and any acknowledged UUID. Larger valid cohorts can exceed this controller-wide
budget; no eviction or sharding is provided. See
[capacity scope](../evidence/catalyst-center-2026-10-18/pnp-capacity.md). Final whole-cohort preflight refusals can now retire
through the existing one-use no-dispatch proof and replan while retaining section
ownership. `PnPRegistrationReplanRequired` distinguishes this outcome from an
uncertain submission. The final native absence check shares this proof mechanism; errors after POST entry and lost acknowledgements remain fenced. See [final-check qualification](../evidence/catalyst-center-2026-10-18/pnp-primitive-no-dispatch.md);
see [recovery scope](../evidence/catalyst-center-2026-10-18/pnp-no-dispatch.md). See
[public registration qualification](../evidence/catalyst-center-2026-10-15/pnp-public-registration.md)
and `examples/configs/catalyst-center/pnp-registration-execution.yaml` for the
native cycle, tested example and remaining admission/completion work.
A private removal primitive now binds the original verified registration UUID and
payload, mandatory authority, uncontacted native state and complete managed-serial
absence through the existing one-use guard. Native qualification passes one
fresh generated registration/removal cycle, including deletion-added audit
metadata and repeated absence. Earlier refusal/uncertain acknowledgement evidence
is retained; uncertain requests are not replayed. This does not enable public
prune/Delete or physical onboarding. See
[uncontacted removal scope](../evidence/catalyst-center-2026-10-19/pnp-uncontacted-removal.md).
A private source-bound capture stage can now enrich an owned registration with
canonical cleanup identity and a safe native digest, preserving the original
operation/time in the same receipt slot. It avoids guessing serial case after
source removal. API persistence/CAS/lost-ack tests and a native serialized-restart
cycle pass; legacy registration remains valid without automatic cleanup
adoption. This remains a prerequisite for durable public deletion, not a Delete
permit. See [canonical baseline](../evidence/catalyst-center-2026-10-19/pnp-removal-baseline.md).
The private source-prune stage now binds that creator into the existing synchronous
journal, recovers Verifying after restart, publishes an independent absence
receipt atomically and requires it plus the original creator/owner before release.
A native cycle passes after the old device declaration is removed, with one exact
Delete, no raw-serial reconstruction and restored baselines. Unknown responses
remain fenced even on absence. Public routing, complete consumer/private-content
protection and CR finalization remain pending. See
[journaled removal](../evidence/catalyst-center-2026-10-19/pnp-journal-delete.md).
Public reconciliation now recovers an already acknowledged PnP Delete before
empty/edited intent can fall through to another family. Unknown or changed
source/owner states remain fenced, and no new public dispatch/full convergence is
advertised. Cleanup also refuses listed attached configuration, credentials,
headers and onboarding references using a safe refusal bit rather than private
values. Native generated-record public recovery passes; full consumer census,
public dispatch and CR/installed finalization remain pending. See
[recovery and content guards](../evidence/catalyst-center-2026-10-19/pnp-public-recovery-content.md).
Synchronous PnP removal now also preserves the existing guard-bound no-dispatch
proof through its native/journal wrappers. Read/content/source refusal before
HTTP entry can retire only the exact unchanged owned preparation and requires a
fresh plan. Changed owners and post-submit uncertainty remain fenced. Native
refusal/replan/one-Delete/restart cleanup passes; this does not enable new public
dispatch. See [pre-dispatch recovery](../evidence/catalyst-center-2026-10-19/pnp-removal-no-dispatch.md).


The private PnP prune planner now validates the entire pending owned cohort,
budgets all remaining absence receipts and enforces deterministic UUID order.
All pending native baselines/eligibility are rechecked inside the existing final
authority guards; a changed sibling prevents the first Delete. Each completed
record still needs its atomic absence receipt and a fresh plan for the next.
Two generated native registrations and ordered cleanup restored both baselines.
Public admission, full consumer census and CR/installed finalization remain
pending. See [whole-cohort qualification](../evidence/catalyst-center-2026-10-19/pnp-removal-cohort.md).

A manager-only metadata scope prerequisite now reuses the bootstrap identity
census for the whole canonical serial set and checks retained reservations and
existing bootstrap fence keys. Source/owner/controller brackets and real API
Node/reservation/Lease/source-change tests pass. It returns no grant, creates no
lease and is not yet wired into production actor admission. Public new cleanup
dispatch remains disabled. See [manager scope evidence](../evidence/catalyst-center-2026-10-19/pnp-manager-scope.md).

The manager preparation route now consumes an optional pure adapter scope
projection. Catalyst Center binds the exact first pending PnP Delete, creator
digest, source revision and complete remaining serial cohort before shared
scope observations. Inline/ConfigMap, provider real-API and registry/manager tests
pass. The operation remains Prepared: actor/version binding, serialized worker
handoff and new public dispatch are still incomplete. See
[manager preparation binding](../evidence/catalyst-center-2026-10-19/pnp-manager-binding.md).

Manager scope observations now also bind the sole current worker's
Deployment/ReplicaSet/Pod identities using the existing bootstrap validators,
with source checks around repeated worker observations. Real API object
persistence and controlled replacement/overlap/template cases pass; readiness
is simulated. No fence or grant is created. Protected actor/version authorization,
serialized fence handoff and durable no-dispatch release remain pending. See
[worker binding evidence](../evidence/catalyst-center-2026-10-19/pnp-manager-worker.md).

The staged metadata handoff now retains certified pre-HTTP refusal evidence
atomically with retiring its exact journal slot. Creator/success receipts stay
unchanged, unreleased custody cannot be dropped by field or status replacement,
and retained custody holds new work. The scope digest excludes its own handoff.
Real API persistence/recovery guards pass. Manager sealing, authenticated actor
admission and fence claim/release remain unwired, so public new dispatch stays
disabled. See [durable refusal](../evidence/catalyst-center-2026-10-19/pnp-durable-refusal.md).

Managed metadata custody now has authenticated manager/Pod-bound worker
admission and a full live-policy/binding/namespace verifier, reusing the shared
bootstrap protection reader. Refusal, verified-absence, uncertainty and actor
separation pass real API admission tests; legacy no-custody behavior and the
managed chart contract pass. These are staged fixtures, not an installed/native
handoff qualification. Manager sealing, compatible bootstrap settlement,
worker claim/release and public dispatch remain unwired. See
[metadata actors](../evidence/catalyst-center-2026-10-19/pnp-metadata-actors.md).

Protected manager seal creation now uses the existing status CAS with exact
source/actor/policy observations and required runtime compatibility checks.
Lost acknowledgement and stale CAS recover from fresh snapshots without
overwriting custody. Real manager admission/readback passes. The preparation
route exposes an optional compatibility hook, but the composition root leaves
it nil: fence claim/settlement and native dispatch remain disabled. See
[manager seal](../evidence/catalyst-center-2026-10-19/pnp-manager-seal.md).

PnP claim preparation and restart resumption now persist and recheck a v2 native
baseline before requesting a fresh permit. Native reads finish before final
permit consumption and the single POST. Public claiming remains gated because
unprovisioned PnP records lack the managed identities required by current
reservations; bootstrap admission and workflow completion are still needed. See
[Prepared claim handoff qualification](../evidence/catalyst-center-2026-10-15/pnp-prepared-claim-handoff.md).


A shared manager-side bootstrap identity observer now checks source ownership and
known cross-namespace device/Node/operation conflicts using repeated global reads.
It grants no authority and does not change connector RBAC or existing device
leases. Production bootstrap serialization/admission and completion still need
implementation. See [bootstrap identity evidence](../evidence/catalyst-center-2026-10-15/bootstrap-identity-observation.md).


A manager-side serial fence kernel now uses retained Kubernetes Leases in one
configured shared namespace. It binds the source and Prepared identity, refuses
foreign/expired evidence and preserves uncertain reservations. A runtime check
is mandatory; production admission, enrollment exclusion, grant and settlement
are not wired. The Lease is not a dispatch permit. See
[serial fence evidence](../evidence/catalyst-center-2026-10-15/bootstrap-serial-fence.md).


The bootstrap fence now also retains the exact current connector incarnation.
It reuses the managed controller-worker validator and checks Deployment,
ReplicaSet and Pod UIDs, readiness, account and controller argument/template
binding. A replacement worker cannot adopt an old fence. These local actor facts
do not replace installed admission or native dispatch authority. The retained
lab fixture still requires cleanup once the tunnel is restored. See
[connector binding qualification](../evidence/catalyst-center-2026-10-15/bootstrap-serial-fence.md#connector-incarnation-binding).


The chart now includes bootstrap Lease protection when managed topology is
enabled. The shared namespace is `config.leaseNamespace` or the Helm release
namespace; the reserved name prefix is `cvk-bootstrap-`. The policy permits only
manager creation of retained evidence and denies modifications/deletion. It does
not enable claiming or provide a settlement path. Isolated API-server tests and
native Prepared/runtime-denial requalification pass; both native generated records
were cleaned up. Production manager/admission wiring remains pending. See
[bootstrap admission evidence](../evidence/catalyst-center-2026-10-15/bootstrap-fence-admission.md).


Bootstrap reservation now directly verifies the complete live compiled protection
policy and Deny binding and binds their UIDs/generations plus the shared namespace
UID into retained evidence. A valid replacement installation cannot inherit an
old fence. Production actor/enrollment compatibility, installation and execution
still remain gated; the policy check grants no device authority. See
[runtime protection qualification](../evidence/catalyst-center-2026-10-15/bootstrap-protection-runtime-binding.md),
including the explicitly simulated background controller status in envtest.


CiscoDevice manager reconciliation now waits before finalizer/teardown/projection
or worker/config writes when its declared or retained serial has any bootstrap
Lease evidence in the shared namespace. Malformed, expired or deleting evidence
also holds it. This manager guard does not stop existing workers or independent
aggregators and does not enable public claiming. See
[enrollment guard qualification and limits](../evidence/catalyst-center-2026-10-15/bootstrap-device-enrollment.md).


The independent aggregator now checks retained bootstrap evidence before worker
startup and each IOS-XE/NX-OS engine write or cleanup transaction. Guard refusal
preserves config cleanup finalizers/owned keys. Report-only behavior is unchanged.
This does not prove earlier I/O drained or unknown aliases are contained, and
public claiming remains gated. See
[aggregator guard evidence](../evidence/catalyst-center-2026-10-15/bootstrap-aggregator-guard.md).


Aggregator worker replacement now waits for observed local loop and transport
cleanup completion. Unknown or failed cleanup stays retained; Device reincarnation
or Node-binding changes require a controlled rebind. These in-process records are
not durable quiescence or native outcome proof and do not enable claiming. See
[retirement qualification](../evidence/catalyst-center-2026-10-15/bootstrap-aggregator-retirement.md).


Local aggregator registration also blocks a new Device name/namespace sharing
a known canonical serial with an active or unresolved retiring worker. Captured
declared/retained scope survives source pruning; unrelated serials stay independent.
Rejected candidates remain tracked through cleanup. This does not cover unknown
hardware or other processes and does not enable claiming. See
[physical alias qualification](../evidence/catalyst-center-2026-10-15/bootstrap-aggregator-physical-aliases.md).


The disabled WPA2_PERSONAL SSID credential example is native-qualified through
the public reconciler with projected versioned values, accepted-task restart,
stale projection refusal, same-ID rotation, no replay and exact cleanup. Native
task/version receipts do not prove client authentication; Kubernetes persistence
was simulated and installed worker/schema qualification remains pending. See
[SSID credential manifest evidence](../evidence/catalyst-center-2026-10-15/ssid-manifest-credentials-native.md).


The disabled Multi-PSK example now has native qualification for main plus two
ASCII keys: priority reorder causes no HTTP write, one-key rotation uses the
same native ID, and receipt paths/aliases follow the current source indices.
Installed Kubernetes projection and actual client authentication remain pending.
See [Multi-PSK qualification](../evidence/catalyst-center-2026-10-15/ssid-multipsk-native.md)
and `examples/configs/catalyst-center/wireless-ssid-multipsk-credentials-apply.yaml`.

#### Removing and returning Multi-PSK priorities

Update the same owned config/SSID with
`examples/configs/catalyst-center/wireless-ssid-retire-priority4-apply.yaml` after
the Multi-PSK example. Retain unchanged keys' version/value bindings and move their
SecretRef paths with their manifest indices. Removing a priority requires explicit
`multi_psk_settings` ownership and removing its SecretRef; the retired password
need not remain projected. CVK retains its version floor in `ssidCredentialHistory`.
Reintroduction requires a strictly greater counter and matching atomic Secret.
Explicit `multi_psk_settings: []` clears the set while the main credential remains
projected; omission does not request removal. Deploy the updated controller CRD
before the new worker. An older schema that drops history is refused by the
resource marker. This is qualified on disabled, unassociated definitions; client
authentication and installed-worker rollout remain unqualified. See the
[retirement evidence](../evidence/catalyst-center-2026-10-15/ssid-credential-retirement.md).

For global SSID credential tasks with an original native task ID and unchanged
owner/source/history bindings, an Unknown journal can now observe the same task
and reach Verifying without resubmission. Pending, failed or uncorrelated tasks
retain their operation; a missing task cannot be recovered from write-only
metadata. Credential receipts still require successful task and native UUID/
metadata verification. See [recovery qualification](../evidence/catalyst-center-2026-10-15/ssid-credential-task-recovery.md).

### CleanAir definition band updates

A selected global CleanAir definition can change `radio_band` among `2.4GHz`,
`5GHz` and `6GHz` using the same native UUID. The ordinary configuration journal
tracks its PUT task and desired readback. The executor checks all selected
CleanAir definitions again before dispatch, including already-converged siblings,
and preserves omitted optional fields. A stale prepared write is refused and its
in-memory submission permit cannot be reused.

The `examples/configs/catalyst-center/wireless-cleanair-apply.yaml` example
uses Retain; this support does not add source pruning or wireless deployment.
[Qualification](../evidence/catalyst-center-2026-10-16/cleanair-band-updates.md)
covers all six directed band transitions, live public manifest revisions and
separate real-API status persistence. Installed actor recovery, durable definition
ownership, all interferer/band combinations and WLC/AP behavior remain unqualified.

An acknowledged CleanAir Update retained as Unknown can resume observing its
original task when owner/source bindings and its original target UUID remain
valid. This uses the existing journal and does not resubmit the PUT. A missing
task, retained legacy Create, changed source/owner or unverified target cannot be
resolved solely because the definition currently matches the manifest.
[Recovery qualification](../evidence/catalyst-center-2026-10-16/cleanair-update-recovery.md)
covers serialized restart and native mutation counts; installed actors remain
unqualified.

### RRM FRA definition updates

RRM FRA definitions support `2.4GHz_5GHz` and `5GHz_6GHz` band-pair updates on
one UUID, freeze/status changes and intervals from 1 to 24 hours. Sensitivity is
emitted only for `2.4GHz_5GHz`; omit it for the 5/6 GHz pair. All six pinned
sensitivity values are qualified at the definition API. FRA and CleanAir reuse
selected-state rechecks and one-use prepared dispatch.

The Retain example is `examples/configs/catalyst-center/wireless-fra-apply.yaml`.
[Qualification](../evidence/catalyst-center-2026-10-16/fra-dispatch-and-fields.md)
separates native task/readback from public-manifest real-API persistence. FRA
pruning, installed actors and actual WLC/AP behavior remain
unqualified; global definition acceptance does not prove device firmware support.

Acknowledged FRA and CleanAir Update tasks retained as Unknown can resume native
task observation in pure or combined feature-template manifests. The compiled
resource namespace, original target UUID, task receipt and common unchanged
owner/source bindings remain mandatory; this grants no new submission permit.
[Combined qualification](../evidence/catalyst-center-2026-10-16/wireless-feature-update-recovery.md)
recovers twelve updates across seven native manifest revisions without duplicate
PUTs. Taskless/legacy Create outcomes, full owned-state pruning and installed-worker
recovery remain unqualified.

Combined feature-template writes also recheck the companion collection's original
planning snapshot, including already-converged definitions, before the selected
child's guarded submit. A late companion change consumes and refuses that prepared
write. This reuses the existing planning reads; steady-state/no-op reconciliation
does not add a second inventory pass. [Cohort qualification](../evidence/catalyst-center-2026-10-16/wireless-feature-cohort-preflight.md)
covers both native refusal directions and the complete acknowledged-task cycle.
Automatic retirement of an already-started journal after a no-dispatch refusal
remains unqualified; matching metadata does not grant taskless replay authority.

### Feature creator ownership

New FRA/CleanAir Creates retain an atomic creator receipt with the original
manifest/source/controller operation, native task and verified UUID. Capacity for
the declared missing definitions is checked before effects. A receipt must remain
present before that creation journal can be released; missing, duplicate or
schema-pruned records do not authorize replay or name-based adoption.

Acknowledged Creates retained as Unknown can recover through their original
task and UUID proof. Retained legacy Creates keep their prior protocol, and
missing-task outcomes remain fenced. Existing imported definitions can still be
updated without acquiring creator ownership. Creator identity survives subsequent
attribute updates. Owned Updates now atomically publish a separate applied-state
record bound to the desired effect, original UUID and native Update task. Each
successive update replaces that record and leaves the creator unchanged. Missing
or invalid records fence release; imports do not acquire either ownership record.
These receipts prove the last CVK mutation, not continual absence of native drift
or source changes that need no write. [Update qualification](../evidence/catalyst-center-2026-10-16/wireless-feature-applied-state.md)
includes retained-task recovery and exact native cleanup. Source pruning and
tombstone-aware recreation are qualified below; owner handoff and installed actor
qualification remain pending. [Qualification](../evidence/catalyst-center-2026-10-16/wireless-feature-creator-ownership.md)
records fourteen live task recoveries and the remaining lifecycle limits.


### Native feature state evidence for pruning

New CleanAir/FRA creator and applied-state receipts include the fingerprint of
the complete native detail object, including attributes omitted by the source.
The fingerprint comes from the same response used for typed desired-state
verification, with duplicate keys refused and numeric precision preserved.
Legacy UUID-only receipts remain valid for their original ownership/recovery
protocol; they do not acquire a native-state fingerprint by matching current
metadata. [Qualification](../evidence/catalyst-center-2026-10-16/wireless-feature-native-state.md)
checks fourteen stored/native comparisons across seven live manifest revisions.
This state evidence now supports the bounded source-pruning lifecycle below.
Legacy records without fingerprints remain ineligible for automatic pruning.


### Feature source pruning

A feature-only Apply config may opt into `prunePolicy: Delete` while keeping
`deletionPolicy: Retain`. Use the same config UID that created the definitions;
`examples/configs/catalyst-center/wireless-feature-prune-apply.yaml` shows explicit
empty CleanAir/FRA collections for removing that owner's omitted definitions.
Retain remains the default, and imports or foreign owners are not adopted.

Pruning requires an original creator receipt, the last verified complete native
fingerprint, and unchanged kind/name/UUID/system identity. It reads the complete
wireless-profile inventory, refusing references and incomplete or unmapped
metadata. Before each Delete it repeats the omitted-cohort state/reference checks,
then revalidates the durable source/owner/controller permit and unchanged receipts.
All candidate deletion records must fit the existing receipt capacity before the
first Delete. The prepared submission can be consumed only once.

Native NCNC Delete task/UUID proof and two independent absence observations are
required before Verified and a deletion tombstone are published atomically.
Generic metadata verification cannot bypass this step; an absent/pruned/invalid
tombstone cannot release the journal. Acknowledged Unknown Deletes resume task
observation without another submission. Missing tasks, lost acknowledgements,
failed outcomes, replaced resources or changed source/ownership remain fenced.
Later reconciles retain the tombstone and do not replay a successful Delete.

[Qualification](../evidence/catalyst-center-2026-10-16/wireless-feature-pruning.md)
covers eight live source revisions, sixteen acknowledged-task recoveries and two
public deletion tombstones with original creator/applied rows unchanged. It also
covers actual CRD/status persistence and negative dispatch/publication cases.
The lab has no qualified WLC/AP deployment: this deletes unused controller-side
definitions, does not detach profile references, and does not deconfigure devices.
Tombstone-aware recreation and feature-config deletion are qualified below.
Ownership handoff and installed worker qualification remain pending. The feature resources
remain partial.


### Feature recreation after source reintroduction

A later desired-source revision may reintroduce a name that the same config UID
pruned successfully. No new public flag is required. Reintroducing with Retain
restores declared definitions without requesting deletion of omissions; the
recreation example is
`examples/configs/catalyst-center/wireless-feature-recreate-apply.yaml`.

The Create operation explicitly binds the preceding Delete operation ID and a
digest of its complete creator/applied/tombstone cohort. CVK requires a newer
config generation or changed source identity/version, unchanged ownership/history,
and absence of both the selected name and all retired UUIDs for that logical
name. The final submission guard repeats the absence and durable checks. An
existing same-name object is not adopted, and an old UUID cannot become the
new Create result.

On success, CVK atomically archives the previous lifecycle and publishes the new
creator receipt with Verified. Old task/source/native-state metadata is preserved;
new Updates replace only active applied state, and subsequent pruning writes a
new tombstone. Current creators link every archived predecessor by exact digest.
Missing, malformed, duplicate, detached or cyclic history blocks further mutation
and release. Archives consume the existing bounded receipt capacity and are not
silently truncated. Lost acknowledgements and missing tasks retain their fences.

[Qualification](../evidence/catalyst-center-2026-10-16/wireless-feature-recreation.md)
passes two native recreation/update/prune cycles after the initial lifecycle:
fourteen source revisions, twenty-eight task recoveries, six distinct native
UUIDs, four retired cohorts and eighteen retained records, with all generated
definitions removed and both native inventories restored. Kubernetes API
persistence is separately qualified. No installed actor, WLC/AP deployment,
cross-owner handoff or arbitrary lost-history reconstruction is claimed by this
recreation lifecycle. Feature-config deletion is qualified below.


### Feature-config deletion cleanup

Feature-only Apply may select `deletionPolicy: Delete`. The central manager
installs a protection finalizer before the isolated worker may apply that policy.
Both manager and worker must include this implementation and the chart's manager
RBAC update. Controller-worker RBAC remains config-read/status-write only; no
worker gains config spec, metadata, finalizer or Secret mutation permissions.
The example is
`examples/configs/catalyst-center/wireless-feature-delete-on-config-removal.yaml`.

Deleting the config triggers verified cleanup of its owned CleanAir/FRA designs,
using the existing pruning executor and task journal. The stored desired source
is not rewritten. Cleanup operations bind the deletion timestamp and full spec;
only those Deletes may be newly submitted while the config is deleting. Existing
accepted feature tasks are observed to completion under their original source
and generation. A deletion-related generation transition grants no new Create or
Update authority. Changed or missing required task sources remain fenced.

When no task is pending, cleanup can proceed from retained receipts even if its
source ConfigMap was removed. References, native drift, missing receipts/tasks,
failed/unknown outcomes and unsupported ownership keep the finalizer. Completion
requires verified tombstones, fresh native absence and no pending operation for
that config. The worker reports a proof bound to config/controller identity,
generation, spec, deletion timestamp and exact retained evidence. The manager
uses uncached reads and an optimistic metadata-only patch to remove its finalizer;
other finalizers survive. Unrelated owners' journals are not erased or required to
settle once this config's own cleanup is already verified.

Retain remains the default and does not delete remote definitions. Original
ownership and lifecycle history remain on the NetworkController after CR removal;
a new config UID cannot implicitly take over that history. The parent controller
must remain active through cleanup. Parent-controller deletion, cross-owner
handoff and installed-worker/WLC qualification remain pending.
[Qualification](../evidence/catalyst-center-2026-10-16/wireless-feature-config-deletion.md)
separates manager/worker wiring, actual Kubernetes persistence, native cleanup and
remaining rollout limits.


## Template creator and latest-state receipts

New project and regular/composite draft Creates bind the original complete NCTP
Create task tree and its canonical UUID result to native identity, project, kind
and declared content. Creator receipts remain immutable through updates and
commits; imported objects and legacy operations gain no creator adoption.

For template-only configurations, an optional `PrivateComparisonKey` reference
at `/projects` uses the existing controller allow-list and manager projection.
`examples/configs/catalyst-center/template-private-baseline-execution.yaml` shows
the manifest shape. The operator supplies the random 64-hex-character key through
its authorized Secret projection, separately from model data.

Privately baselined owned updates retain one separate latest-state receipt per
object. It binds the immutable creator cohort and records project, draft or
commit stage, current source and a keyed fingerprint of native state. Template
fingerprints include complete version metadata and committed bodies, preserving
unknown fields and exact numeric values. Native content and key material are not
published in status. Atomic receipt readback, complete task-tree observation and
existing guards handle acknowledged recovery and proven preflight refusal.
Typed regular commit receipts preserve restart-stable composite settling.

These per-mutation snapshots are not continuous drift monitoring or prune
permission. Later child/version changes can stale an earlier snapshot. Complete
external reference/version provenance, ordered source pruning, deletion and
recreation remain pending, along with installed manager/worker qualification.
See [creator evidence](../evidence/catalyst-center-2026-10-16/template-creator-ownership.md),
[private creation snapshots](../evidence/catalyst-center-2026-10-16/template-private-baselines.md)
and [latest-state evidence](../evidence/catalyst-center-2026-10-16/template-applied-state.md).


### Template version ownership

New privately baselined commits append the original shared operation ID to the
existing CVK commit comment. Complete task-tree success, a unique native version
UUID carrying that nonce, native parent/body/version checks and repeated private
fingerprints precede atomic receipt publication. Later writes archive the version
receipt while replacing latest state. Body/metadata changes, external versions,
missing historical rows and duplicate claims cannot acquire complete version
ownership. Old commits and imported versions are not adopted.

Archives use the existing journal capacity, reserved before further effects.
No native version Delete is introduced; the pinned provider's version removal is
state-only. Template pruning/deletion and complete reference qualification remain
pending. See [version ownership evidence](../evidence/catalyst-center-2026-10-16/template-version-ownership.md).


### Template profile references

The reference reader requires matching before/list/after counts from the native
reverse profile API, then cross-checks legacy switching-profile details for
Day-N and onboarding bindings. It repeats the combined observation to catch
identity changes even when counts stay equal. Unknown/incomplete views refuse
collection; returned data contains reference IDs/kinds only. Opaque inherited
site settings stay outside the snapshot.

This is a prerequisite reader, not a public prune path or an atomic native
snapshot. Composite/history, PnP and active deployment references, source/owner
permits and destructive lifecycle verification remain pending. See
[profile reference qualification](../evidence/catalyst-center-2026-10-16/template-profile-references.md).


### Composite template references

The reference reader scans drafts and every committed version across native
projects, matching both base and version UUIDs. This preserves references from
older committed consumers even after a current draft changes. Unknown schemas,
identity changes and incomplete history cannot produce a reference-free result;
only structural member IDs are examined, with private content left outside the
returned snapshot.

This adds a consumer-class reader only. PnP/deployment dependencies, final
mutation permits, public pruning/deletion/recreation and installed actor
qualification remain pending. See [composite reference evidence](../evidence/catalyst-center-2026-10-16/template-composite-references.md).


### PnP template references

The PnP reference reader inventories standalone workflow definitions and device
main/system/reset instances, including root/task configuration UUIDs and
workflow-parameter configuration lists. Both template base and committed-version
UUIDs participate. Native/legacy identity aliases normalize; conflicting IDs,
incomplete counted pages and changed repeated observations refuse collection.
Only reference identities leave the reader; CLI, URLs and parameter values are
excluded. Private claim recovery now also checks task-level template consistency.

An isolated native unassigned workflow lifecycle and exact cleanup are qualified.
No device was claimed or operated. Device-instance qualification, active deployment
consumers and final public destructive lifecycle remain pending. See
[PnP reference qualification](../evidence/catalyst-center-2026-10-16/template-pnp-references.md).


### Retained template deployment references

The retained-reference observer reuses the existing regular/composite version
bindings and immutable operation contract. It includes the current operation and
historical action receipts, keeping references after config or owner removal and
across reader restarts. It repeats native aliases and uncached journal reads;
changed or malformed evidence cannot produce a reference-free result. Private
input fingerprints, parameter values and device serials are excluded.

This observes known references for one NetworkController, not appliance-wide
activity. Native status by a known deployment UUID does not enumerate unknown UI,
API or scheduled deployments. Historical receipts do not prove activity or
completion. Real API persistence and native template-history reads are qualified;
physical deployment, complete operational coverage and public prune/delete/
recreation remain pending. See [qualification and coverage](../evidence/catalyst-center-2026-10-16/template-journal-references.md).


### Template software-version inheritance

`catalyst_center.templates.software_version` supplies the definition fallback from
the pinned NetAsCode model. A definition's own `software_version` overrides it.
An explicit null suppresses inheritance and leaves the optional native constraint
unmanaged; use an explicit empty string to clear it. Quote version strings.
Native empty/null/omitted clear readbacks converge without repeated commits.

This field controls template applicability. It does not select or execute a SWIM
upgrade. Other defaults remain resolved upstream. Use
`examples/configs/catalyst-center/template-software-version-execution.yaml` for
inherited and overridden definition examples. Public fixtures and eight regular
native metadata transitions are qualified; version patterns, software variants,
complete destructive lifecycle and installed actors remain pending. See
[qualification and precedence](../evidence/catalyst-center-2026-10-16/template-software-version-fallback.md).


### Nullable template variable metadata

Optional variable binding/default/display/hint fields, flags and selection
metadata can be null. They are omitted from the native payload. Primitive
numeric/boolean defaults and choice values become strings through the existing
parameter conversion path. Declare a supported `data_type` explicitly and use
SINGLE_SELECT or MULTI_SELECT when choosing a selection type. Invalid enums,
nested values and unknown fields fail before prerequisite writes. Quote values
whose exact numeric text must be preserved.

Set `default_value: ""` to clear a STRING default and `data_values: {}` to clear
its choices. A nonempty choice map is also authoritative: removing a label must
remove it from the native draft and committed version. Null/omitted optional
fields do not express that reset. The regular-template lab cycle verifies
clearing, restoration, individual choice removal and repeated no-op convergence;
see [reset qualification](../evidence/catalyst-center-2026-10-16/template-variable-resets.md).

An empty choice map keeps its selection mode and permits no selectable values.
Deployment validation accepts an omitted optional variable after clearing;
supplying an undeclared choice still fails. SINGLE_SELECT/MULTI_SELECT changes
and parameter checks against actual native draft/committed declarations are
qualified separately from device deployment; see
[selection compatibility](../evidence/catalyst-center-2026-10-16/template-selection-compatibility.md).

The template reader handles parameter-list permutations only when the appliance
explicitly disables custom ordering. Every field/value and parameter ID remains
part of the observation; meaningful edits still refuse dispatch. Private snapshot
comparison uses the same canonical view and exact native JSON numbers. Older
mismatching receipts remain fenced.

Use `examples/configs/catalyst-center/template-variable-metadata-execution.yaml`.
Public/API cases and native regular-template default updates/commits/no-ops pass
with generated fixture cleanup. Device deployment, full field/reset combinations
and installed-worker qualification remain pending. See
[variable metadata evidence](../evidence/catalyst-center-2026-10-16/template-variable-metadata.md).


### Typed template defaults and display metadata

Use `examples/configs/catalyst-center/template-parameter-types-execution.yaml`
for inert INTEGER, IPADDRESS, MACADDRESS and STRING definitions. The existing
compiler maps display descriptions/names/hints, explicit empty bindings and
required/array/constant flags. Metadata qualification is separate from actual
deployment and rendered device-state verification.

SECTIONDIVIDER is a pinned schema enum but remains unqualified on the lab: it
was omitted without a content reference and retyped to STRING with one. A
custom-order probe did not fix it. CVK continues to refuse mismatching native
readback; see [parameter types and remaining gaps](../evidence/catalyst-center-2026-10-16/template-parameter-types.md).

### Controller-data bindings

Variable `binding` is an opaque string as in the pinned provider. CVK passes it
through the template compiler and leaves runtime resolution with Catalyst Center.
Use `examples/configs/catalyst-center/template-binding-execution.yaml` for an
inert CommonSettings declaration with runtime site/device placeholders.

Nonempty binding storage, filter update, clearing, restoration and no-op behavior
are live-qualified. Explicit values still need to satisfy native choice and type
rules; a binding does not make an empty dropdown unrestricted. Live runtime
resolution and physical deployment are separate remaining gates. See
[binding execution and recovery boundaries](../evidence/catalyst-center-2026-10-16/template-bindings.md).


Private site/policy tag baselines can now authorize same-UUID membership removal
under explicit `prunePolicy: Delete`, while keeping the tag nonempty. The existing
comparison-key projection and action ledger are reused; affected empty-site
scope survives restart and is checked before dispatch and receipt publication.
See [membership qualification and limits](../evidence/catalyst-center-2026-10-17/wireless-tag-membership.md)
and `examples/configs/catalyst-center/wireless-tag-membership-apply.yaml`.
Whole-tag omission and empty-membership deletion are covered by the later
whole-tag qualification below; larger removal envelopes remain pending.


A complete source can now reduce qualified private tag memberships and remove
their parent site assignment in one revision. Tag Updates converge first;
association DELETE still requires actual native references to be gone. The
in-memory projection only validates planning and never verifies deletion. See
[dependency qualification](../evidence/catalyst-center-2026-10-17/profile-site-tag-dependencies.md)
and `examples/configs/catalyst-center/profile-site-tag-dependencies.yaml`.


Explicit Apply/Delete now prunes privately recorded omitted site/policy tags,
including the pinned empty-sites resource omission case. Original UUID/content
and complete affected-scope guards gate DELETE; verified absence and original
full task trees publish an atomic latest-slot tombstone. Owned tag deletion can
precede strict parent unassignment in the same source. See
[whole-tag qualification and remaining lifecycle limits](../evidence/catalyst-center-2026-10-17/wireless-tag-prune.md)
and `examples/configs/catalyst-center/wireless-tag-prune-apply.yaml`.
Two passing native workflows verify 26 distinct original tasks, five tag
deletions before four site unassignments, final no-op and restored inventories.
Installed actors and physical wireless behavior remain unqualified.


Metadata-marked bootstrap fences now have manager-only settlement admission.
Release proofs in these tests are staged; production acquisition, worker claim,
outcome verification and release recovery remain pending. Automatic activation
stays disabled. See [settlement admission evidence](../evidence/catalyst-center-2026-10-19/pnp-metadata-fence-settlement.md).


Metadata handoffs now retain immutable original Lease references incrementally
within the sealed serial cohort. Only the manager may append them while Prepared,
and worker submission requires the complete set. A shared reader rejects absent/replaced/released evidence and binding
or worker drift. Acquisition/publication writers and verified release/recovery
remain pending; this contract does not enable automatic execution. See
[reference qualification](../evidence/catalyst-center-2026-10-19/pnp-metadata-fence-references.md).


The optional manager handoff route now acquires canonical metadata fences and
retains each original UID through the existing status CAS. It recovers exact
next pending bindings and committed partial references after lost responses,
while refusing published UID replacements. Nil compatibility keeps deployment
observation-only. Worker execution and verified settlement/recovery remain
pending; full pinned-model parity is not complete. See
[acquisition qualification](../evidence/catalyst-center-2026-10-19/pnp-metadata-fence-acquisition.md).


Metadata acquisition now publishes a separate immutable manager readiness
acknowledgement after full original-fence and scope verification. A shared
worker claim primitive uses the original journal CAS and produces a one-shot
process permit only after successful readback. Lost or uncertain claims retain
custody and produce no permit. Typed PnP submission and settlement integration
remain pending; the normal runtime remains disabled. See
[worker-claim qualification](../evidence/catalyst-center-2026-10-19/pnp-metadata-worker-claim.md).


An explicitly configured private worker path now resumes the original Prepared
PnP metadata removal through the shared journal claim and existing guarded
transport. It preserves creator-bound verification, pre-HTTP refusal and
uncertain-outcome fencing; configured execution cannot fall back to an unsealed
legacy submission. Actual API tests use a controlled appliance endpoint. Manager
settlement/release and a complete installed/native lab cycle remain pending,
and default/public new pruning stays disabled. See
[typed submission qualification](../evidence/catalyst-center-2026-10-19/pnp-metadata-prepared-submission.md).


The optional manager now settles exact verified-absence or certified no-dispatch
metadata outcomes through a durable intent, original UID/RV Lease deletion and
separate release/retirement. Original creator/tombstone history remains intact.
Private Catalyst Center lab qualification passed both saved-state recovery and
a fresh two-record cycle, with empty PnP and unchanged network baselines restored.
Kubernetes/runtime authority in native runs is simulated; real API actor/schema
tests are separate. Production/public activation and full-model parity remain
pending. See [settlement and lab evidence](../evidence/catalyst-center-2026-10-19/pnp-metadata-manager-settlement.md).


The public configuration reconciler now prepares owned PnP source pruning and
resumes only the original manager-ready operation when a metadata runtime is
explicitly configured. Public verification, manager settlement and public finish
passed a fresh two-record Catalyst Center cycle; real API tests separately cover
verified completion, lost responses and pre-HTTP refusal. Default installed
activation remains disabled, and installed runtime authority, full consumer
protection, deletion lifecycles and full-model parity remain pending. See
[public pruning qualification](../evidence/catalyst-center-2026-10-19/pnp-public-metadata-pruning.md).


Metadata cleanup now requires known declared/retained identities for existing
CiscoDevice consumers. Legacy enrollment and aggregator writes also honor the
shared retained fence namespace: address-only consumers wait while any reserved
serial key exists. Real API tests cover enrollment/deletion barriers and valid
cross-namespace identity transitions, and a fresh native public PnP cycle passed.
Installed runtime composition and the remaining retained-consumer/lifecycle
proofs are still pending. See
[consumer enrollment qualification](../evidence/catalyst-center-2026-10-19/pnp-metadata-consumer-enrollment.md).


The shared identity census now retains other controllers' sealed metadata serial
claims even when their active operation is cleared or release is acknowledged.
Recognizable CVK Nodes must also have consistent physical observations before
metadata admission; ordinary compute and unrelated virtual-provider behavior is
preserved. Controlled, actual API and a fresh native public PnP cycle pass.
Installed compatibility/composition, active-operation drain and the remaining
full-model lifecycle/hardware work are still pending. See
[retained custody and Node census qualification](../evidence/catalyst-center-2026-10-19/pnp-retained-handoff-node-census.md).


### Experimental installed metadata handoff

The manager and isolated Catalyst Center worker now have an opt-in runtime path
for owned PnP metadata cleanup. This setting remains off by default and has not
yet passed installed manager/worker/native qualification. Source, controlled
runtime and real API authentication/admission checks are recorded in the
[runtime wiring evidence](../evidence/catalyst-center-2026-10-19/pnp-installed-runtime-wiring.md).

A supported managed installation must already use leader election, isolated
workers, current admission contracts and an explicit shared Lease namespace:

```yaml
topology:
  enabled: true
  controllerMetadataHandoff: true
controller:
  leaderElect: true
rbac:
  profile: strict
config:
  leaseNamespace: existing-shared-namespace
```

Use the namespace already bound in the retained topology policy. Changing an
existing installation from the implicit namespace default is a separate
retirement/migration, not an override for outstanding work. Metadata cleanup
does not require a read-write physical configuration profile. Actual matching
chart/binary role contracts, a single ready manager, Pod-bound caller identity,
a current compatible connector and disjoint known consumers are still checked
before preparation and dispatch. The connector and manager must resolve to the
same image ID; sidecar/init-container and heterogeneous-image configurations
are outside this initial runtime gate.

Deploy matching chart and binary contracts together. The shared observation
role now needs `get namespaces` to corroborate fence namespace identity; no
mutation or Secret read was added. Old or broader role shapes are refused.
Before changing controller specs or manager/connector images, complete and
settle retained metadata work. Its original worker/bootstrap/access objects
remain protected; generation/domain changes, cancellation and deletion/source
loss finalization still need their remaining lifecycle implementation.

Docker's local API currently prevents the separate qualification cluster from
being inspected or upgraded. This does not establish installed or full-model
parity; existing private native regression evidence remains a separate layer.

Verified PnP removal journals now finish from original creator-bound absence
receipts after manager custody retires, even if the config/source disappeared or
controller execution was paused. This historical recovery makes no native request
and does not mark current intent converged. Unready/uncertain custody and full
claim/provisioning convergence remain pending. See the
[terminal recovery evidence](../evidence/catalyst-center-2026-10-19/pnp-terminal-recovery.md).

New terminal metadata settlement intents also survive ordinary controller
specification/generation edits, while preserving the original operation and exact
fence identities. The metadata admission chart and binary fingerprint must match
for the guarded non-status UPDATE fix. Existing unverifiable legacy settlement
intents remain held; controller type/endpoint stay immutable. See
[terminal epoch qualification](../evidence/catalyst-center-2026-10-19/pnp-terminal-epoch-settlement.md).

Ready Prepared PnP cleanup now cancels automatically when its controller/config or
source epoch is invalidated. The manager records cancellation atomically before
releasing any original fence; a competing worker claim or uncertain native outcome
remains held. Matching CRDs, admission chart and binary are required. This is
available through the existing experimental metadata runtime; default activation
remains off. See [Prepared cancellation qualification](../evidence/catalyst-center-2026-10-19/pnp-prepared-cancellation.md).

Credential-free AP profiles and disabled OPEN SSIDs now use canonical empty
planner identities, matching Kubernetes journal serialization. Metadata-only
public create/update, task restart and no-op execution are qualified separately
against real CRDs and the Catalyst Center API. Native AP calendar/settings and
OPEN SSID fixtures were cleaned up exactly; installed and device execution
remain separate gates. See [wireless metadata evidence](../evidence/catalyst-center-2026-10-19/wireless-metadata-identities.md).

Managed-site execution selection can now be exported with full source retention
and existing ownership/recovery fences; see [site defaults and selection](catalyst-center-site-defaults.md).

Offline [multi-file source construction](catalyst-center-model-merge.md) now
precedes existing section defaults and bound selection without changing runtime
ownership or operation recovery. Full input-language and model parity remain partial.
