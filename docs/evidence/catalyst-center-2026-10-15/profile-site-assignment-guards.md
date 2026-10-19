# Shared profile/site assignment execution and recovery

NetAsCode 0.5.0 declares `catalystcenter_network_profile_for_sites_assignments`
for both switching and wireless profiles. They continue using one CVK executor,
the existing paginated assignment reader and transport, and the existing journal.
No schema, resource key, receipt format or provider foundation was added.

## Change

The executor freezes profile names, site paths and profile namespace when it
plans a request. Before its one permitted POST attempt, it repeats the complete
selected assignment observation twice. Selected or later profile/site replacement,
membership changes, missing sites and observation failures refuse submission.
Changes to unrelated memberships remain allowed. Both successful requests and
denied or uncertain attempts consume the plan; a lost acknowledgement cannot
reuse its closure.

Duplicate logical profile/site declarations are refused. Verification reconstructs
only the acknowledged pair from the source and persisted profile/site UUIDs,
then repeats that pair's native observation twice. It does not reread unrelated
profile memberships or block an already acknowledged task on a missing future
site. The next planning pass still preflights the complete source before another
mutation. Both native UUIDs and the existing logical resource key remain required.

## Race and recovery fixtures

The switching/wireless matrix covers first and later profile/site replacements,
a site replacement during observation collection, newly present selected/later
memberships, missing later sites, rejected reads, caller mutation, unrelated
membership changes, lost acknowledgement and repeated use of consumed plans.
Foreign membership is preserved. Serialized recovery verifies the original pair
with exactly two membership reads, ignores unrelated future dependencies and
refuses a replacement between those reads. Duplicate selection is also refused.

The focused suite passed in **5.184 seconds**. Affected profile, journal,
template/tag, credential, SWIM and apply-example race tests passed in
**12.788 seconds**.

## Native switching qualification

The public switching-profile reconciler flow passed in **72.21 seconds** (package
74.320 seconds) with simulated Kubernetes persistence. It completed profile
creation, template addition, assignment to a newly created empty area, template
removal/restoration, current-generation InSync, rebuilt-reconciler no-op and
report verification. Exact profile/assignment/area/template/project cleanup
completed through terminal tasks and native readback.

- Profile: `f0b3668f-cd29-45de-9d14-cd45470822e7`.
- Area: `0a0fc354-1a64-4a79-9578-81629cf996ec`.
- Template: `6f85e56f-c39f-42b8-8346-303802776bf6`.
- Project: `ec59bf6e-1a70-4d28-83f9-9c8e9bed3f4b`.

See `profile-site-switching-native.log` for correlated operation and cleanup IDs.

## Native wireless qualification

The isolated wireless compiled-executor flow passed in **89.01 seconds** (package
91.065 seconds). It created a disabled SSID and profile, assigned the profile to
two newly created empty areas, created and additively updated site/policy tags,
checked fresh-compiler convergence/report and retained accepted identity without
replaying requests. Overlapping tag declarations were refused. Captured scope:

- Profile: `da011103-742a-4587-b0b5-d9aed2a65a6c`.
- First area: `f3746eb6-a7c0-4b3a-b861-81f45768fdd8`.
- Second area: `ab8111b3-b4be-4249-ad37-23de3ba3e285`.

Tag/assignment/profile/SSID/area deletion tasks completed and the original
baselines were restored. No device assignment, provisioning or device operation
was submitted. See `profile-site-wireless-native.log`.

## Remaining scope

Switching public reconciliation uses simulated Kubernetes persistence; wireless
qualification uses the compiled executor. Installed actor/API-server end-to-end
qualification, broader hierarchy/inheritance behavior, resource-level ownership
for removal/pruning and larger cross-stage/scale qualification remain pending.
Test-fixture DELETE calls are not evidence of public manifest-driven removal.
Full pinned NetAsCode parity remains incomplete.
