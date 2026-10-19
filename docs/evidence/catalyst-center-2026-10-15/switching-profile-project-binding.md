# Switching profile project binding and preserving updates

The pinned `network_profiles.switching` executor now retains native template
project identity and guards preserving writes through the existing configuration
journal, client and transport. No CRD, credential store, ND implementation or
physical dependency graph was added.

## Implementation

- Existing `ParentID` dependency digests now use the explicit `sp2-` format and
  bind selected template membership plus native project/template UUIDs and roles.
  Ordering and equivalent bare/qualified references do not change identity.
- Missing template projects, native replacement and changed dependency bindings
  refuse planning or verification. Reconstructed verification repeats readback
  and requires stable profile identity.
- Planned writes freeze the complete declared switching selection. Two fresh
  observations immediately before one-use submission refuse selected/later
  profile changes, dependency changes and inherited-setting changes. Unrelated
  profiles do not invalidate the plan. Lost acknowledgement cannot replay the
  same closure.
- Inherited `site.settings` remains private in memory and is preserved in the
  outgoing update. It never enters dependency hashes, status, logs or evidence.
  Comparison preserves exact numeric values and treats only unique-key `attribs`
  envelopes as unordered. Opaque value arrays and duplicate-key sequences remain
  order-sensitive; the original API payload bytes are not rewritten.
- Unknown legacy dependency hashes remain fenced with actionable recovery
  status, including generic Verified release. The exact legacy empty-membership
  digest remains recoverable because it proves no template parents existed.

## Native finding and correction

The first public run completed create, template addition and empty-area
assignment, then retained an Unknown operation during the next definition update.
Repeated read-only observations showed stable selected payload/dependency and
known template groups, but changing inherited attribute order. Privately
normalizing unique keyed envelopes made all six consecutive comparisons match;
no inherited values were emitted.

A complete bounded task-window audit found only successful site work and the
acknowledged assignment, with no additional update task. The captured profile
still had its original template. The exact area had no assigned devices or
children. The authoritative template inventory confirmed the one owned template;
the project summary's empty `templates` field was not used as emptiness proof.

Supervised cleanup removed only the captured assignment/profile/template/area/
project UUIDs after terminal task evidence and scope checks. Each deletion task
and final absence was verified. No original update was replayed and no cluster
journal was modified. This was isolated test-fixture cleanup, not an automatic
production recovery capability. See the adjacent audit and cleanup JSON records.

## Public native qualification

After that verified cleanup, a fresh isolated run passed in **73.84 seconds**
(package **75.740 seconds**). Profile operations used the public configuration
reconciler with simulated Kubernetes persistence and rebuilt reconciler instances.
Project/template and empty-area prerequisites used the existing isolated helpers.

| Source generation | Effect |
| --- | --- |
| 2 | Create empty switching profile |
| 3 | Add the existing isolated template |
| 4 | Assign the profile to the new empty area |
| 5 | Remove template membership while preserving inherited settings |
| 6 | Restore template membership while preserving inherited settings |

Each phase reached current-generation InSync, completed exactly one journaled
operation and retained no active operation. Two additional rebuilt reconciliations
per phase stayed a no-op. Final report matched native state. The exact profile,
assignment, area, template and project were removed with verified task/readback.

Captured final-run UUIDs:

- Profile: `c31d48b2-b0ea-46eb-9dd9-ca54eecae061`.
- Area: `03a2e33b-b76a-4792-932f-8f15032d4f28`.
- Template: `5c226b12-d268-4707-bcd5-901041192fb0`.
- Project: `5865b6b5-0a18-4833-a9d0-42674179cc55`.

## Local validation and limits

The affected switching/wireless profile, tag/template membership, journal,
credential, SWIM and example race suite passed in **12.530 seconds**. Final focused
switching-profile tests, including legacy empty-boundary release, passed in
**3.111 seconds**. Coverage includes late project replacement, profile/template
replacement, later declaration drift, caller mutation, inherited value and
high-precision numeric drift, semantic key ordering, opaque-array ordering and
one-use lost-acknowledgement refusal.

Installed manager/worker/API-server lifecycle, real device deployment, broader
onboarding/composite/multi-site combinations, resource-level adoption/pruning and
automatic evidence-based repair of legacy or uncertain operations remain pending.
No device provisioning or template deployment was performed. Full pinned
NetAsCode parity remains incomplete.
