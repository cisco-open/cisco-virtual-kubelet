# Manifest-driven profile/site unassignment

`network_profiles` Apply now honors explicit `prunePolicy: Delete` for associations
with valid CVK latest-applied receipts. The shared switching/wireless association
reader, existing `ActionApplications`, one controller journal, one-use dispatch,
source/owner/controller/CAS brackets and original task-tree verification remain
the foundation. No CRD field, store, controller or credential channel is added.
The pinned NetAsCode 0.5.0 mapping remains
`catalystcenter_network_profile_for_sites_assignments` in `cc_network_profiles.tf`
and `cc_wireless.tf`.

## Selection and authority

Retain remains the default. Delete selects only recorded associations missing
from the complete current section source. Matching imports and legacy tasks
without application receipts remain unowned and are never inferred as deletion
candidates. Profile definitions, templates, sites and tag definitions gain no
creator or deletion authority from an association receipt. Config deletion is
not supported by this change; `deletionPolicy` must remain Retain.

Explicit empty switching/wireless arrays can remove all eligible owned
associations while retaining definitions and foreign memberships. Null, missing
collection shapes and unknown fields remain invalid. The example
`examples/configs/catalyst-center/profile-site-prune.yaml` must update the existing
owner config; a newly created config cannot adopt another UID's receipts.

Planning reconstructs each retained logical identity from the current native
profile name/UUID and site hierarchy/UUID and compares the existing logical
pair digest. Missing, replaced or renamed objects and an association that vanished
without its original removal acknowledgement require recovery. A shared reader
caches site/profile inventories, memberships, tags and device-empty checks within
one cohort observation. Every removal candidate is checked before the first
removal. Ordinary desired work is also fully planned; invalid later intent cannot
be hidden by a deletion candidate.

## Reference and dispatch guards

An association cannot be removed while any assigned device consumes its site or
a descendant site. PnP site/location references at related hierarchy scopes also
block removal before devices reach assigned inventory. Unbound workflow references or active/unknown
onboarding states and unavailable or unresolved PnP evidence refuse removal; a
proven unclaimed, uncontacted registration without a site or workflow is preserved
without blocking. PnP inventory is read once per cohort observation through the
existing safe typed/paginated reader. Wireless site/policy tag references at the same, ancestor or
descendant scope also block removal. Unavailable device/tag reads, unknown tag
fields and unresolved tag site IDs refuse removal. The public reason is
`ProfileSitePruneReferenced`. These are metadata-only, currently unused-site
operations; this does not supply physical-device admission or unprovisioning.

The exact selected profile/site DELETE is protected by two fresh complete cohort
observations plus the public bookkeeping predecessor and common source/owner
checks. One-use dispatch and private proof-certified unsent-operation retirement
remain unchanged. A mutation error or missing task acknowledgement cannot issue
that proof or authorize a replay. Unrelated associations are never deleted.

## Completion and bounded history

Accepted completion checks its original task root/full tree and then preserves
the normal Verifying boundary. Unknown recovery additionally requires stable
absence of the original pair, current identity/reference evidence and the common
source/owner/journal bracket around that task. Both paths publish only after
independent absence verification. New tag/device references after acceptance can
retain the operation; clearing a tag reference allows observation of the same
original task to continue without replay.

Verified removal atomically replaces the latest application row with a versioned
removal tombstone in the same slot. It retains owner/source/generations, UUIDs,
original task, times and the existing public row/active-cohort consistency seals.
Verified release requires exact persisted receipt readback. The existing 256-row
limit is unchanged: removal and later absent-before-write re-addition reuse the
slot even at capacity. A reappeared external association is fenced rather than
silently adopted or repeatedly deleted. Tombstones are retained on no-op;
missing native identities still require recovery, not automatic retirement.

## Qualification

The [final combined race/API gate](profile-site-prune-qualification.log) covers
82 removal cases locally and the same 82 through actual temporary Kubernetes
API storage, plus the shipped example's scope boundaries. Tests cover normal
and Unknown completion, imports/Retain, empty selections, at-capacity replacement,
re-addition, original UUID/name/path drift, disappearance, preflight refusal,
replacement during preflight, lost acknowledgement, failed/missing task trees,
source/owner/history changes, changes during tree observation, receipt edits,
reappearance, device and descendant-device consumers, PnP site/location/descendant/unbound
workflow/unknown-site/read-error and unclaimed-registration cases, tag/descendant-tag and
unknown/unavailable references. A newly appeared post-task tag is cleared and the
original task completes without another DELETE. Additional drift-injection cases
were corrected to actually change the fixture before asserting the fence.

The [final adjacent race/API regression](profile-site-prune-regression.log) repeats
170 prior association application/recovery and network-setting application cases
alongside the broader configuration paths. The command passed in 325.988 seconds.
Its old switching validation expectation now accepts an explicit empty array and continues rejecting null. Shared [controller, ND,
lifecycle and GNOI checks](profile-site-prune-foundation.log) remain passing.

[Native qualification](profile-site-prune-native.log) reuses generated switching
profile/template/project and disabled-SSID/empty-area wireless single/cohort
fixtures. Additions first retain their verified application evidence. Each native
unassignment uses public Delete intent, retires a proven local unsent preflight
refusal, recovers its original acknowledged task as Unknown, retains an atomic
tombstone and reaches final InSync with repeated no-op. The wireless workflows
first prove real tag-reference refusal with no mutation. The harness then removes
only its exact generated tag IDs before CVK automatically proceeds with
unassignment. This is not manifest-driven tag deletion or ownership adoption.

The final native command, including the PnP guard, passed in 818.038 seconds.
It recovered 23 distinct original public metadata tasks with 23 proven unsent
refusals retired: five association additions and five manifest-driven removals
are included. All three workflows reached final InSync/no-op with five latest
removal tombstones. Both real tag-reference refusals passed, both wireless
baselines were restored, and switching profile/site/template/project cleanup
completed. The lab PnP catalog was empty; nonempty PnP reference shapes are
qualified by the local and actual Kubernetes API fixtures, not native PnP claims.

The native APIs/tasks/readback/cleanup are real; Kubernetes actors/storage,
interruption and preflight faults in native qualification are simulated. The
final combined gate, including the PnP reference guard, passed in 101.328 seconds. [All 47 existing SWIM unit contracts](profile-site-prune-swim.log) passed
in 4.150 seconds. The API gates independently use temporary Kubernetes persistence. [Source fingerprints,
commands and terminal counts](profile-site-prune-sources.json) are distinct from
installed-binary qualification. [Strict documentation verification](profile-site-prune-docs.log)
covers these links.

## Full-goal gaps

Tag/profile creator ownership and destructive lifecycle, dependent-tag automatic
pruning, config deletion/finalizers, owner handoff, tombstone retirement and larger
hierarchy/scale qualification remain pending. Sites with device consumers require
an independently admitted lifecycle rather than bypassing this metadata fence.
Installed workers and WLC/AP/device effects are unqualified. This milestone does
not establish full 93-resource model/schema/default/lifecycle parity or merge
readiness.
