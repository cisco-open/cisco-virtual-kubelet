# Shared PnP registration creation admission

The private registration planner and the public registration stage now share the
same stable whole-cohort observer and guarded write factory. New private
creation can no longer validate only one selected device while bypassing another
declaration's ownership/absence checks or the complete remaining cohort's receipt
capacity. The public stage reuses the extracted implementation rather than
retaining a parallel guard. Its native catalog read budget is unchanged.

A shared scope predicate requires inventory-only retained Apply, no projected
secrets, and resolved pinned NetAsCode format/version. Both public routing and
private creation use it. The generic Apply-shape helper deliberately does not
validate model provenance; the PnP gate now explicitly checks the pinned format,
version and resolved flag. Report, deletion/prune Delete, mixed sections,
projected secrets and wrong/unresolved model metadata are refused before private
native access. Existing exact owned-UUID resolution remains read-only and cannot
grant claim/reset/provision permission.

The shared factory owns a copy of its configuration and raw source snapshot.
After a plan is returned, later updates through the caller's retained object
cannot silently move its dispatch guard to the new revision while retaining the
old payload. The frozen-source unit/race case updates that same borrowed object
and verifies zero POSTs, exact no-dispatch retirement and fresh-plan requirement.

| Gate | Result / scope |
| --- | --- |
| Race regression | 36 families / 624 subcases; 49.088s; PnP/proof/receipt paths, eight scope refusals and frozen-source refusal |
| Real private Kubernetes API | Sixteen public/private capacity/handoff cases; 108.222s; native service simulated |
| Native private-to-public handoff | 9.303s; real registration/cleanup; Kubernetes persistence simulated |
| ND | 4.698s |
| Software-upgrade provider | 14.621s |
| Controller GNOI/SWIM | 6.756s |
| Production build | Passed; Darwin binary, not installed |

Each private API capacity case initially plans/submits through the private entry,
then resumes through the ordinary public reconciler. Both entry points preserve
full/byte/cohort refusals, exact-limit owned no-op, late-capacity no-dispatch
retirement and acknowledged UUID retention on publication exhaustion. No second
journal/recovery engine or receipt store is introduced. Existing lost/async and
foreign-ownership cases remain fenced. Initial scope tests exposed the missing
model-provenance check; the final shared predicate fixes it rather than weakening
the test.

Native qualification created one generated nonexistent-serial, unclaimed record
through the private planner, serialized/rebuilt the journal between transitions,
and finalized ownership through public reconciliation. A subsequent private plan
returned that exact owned UUID with no writer and no new POST. Ordinary no-op and
supervised exact-UUID cleanup passed; repeated PnP/network reads restored the
original baselines and missing owned records were not recreated. The removed
UUID was `6ad419600bc0186771caf16f`. No physical claim/reset/device endpoint was
submitted, and no installed ledger was accessed or modified.

The shared 256-row/256-KiB ledger remains a scale constraint; this consolidation
does not add sharding or eviction. Installed actor qualification, physical
claim/workflow/preview completion, mixed-state composition, public destruction
and full pinned-model parity remain pending. The isolated installed worker still
runs the earlier credential-recovery image.

See [source/task/build record](pnp-shared-admission-validation.json),
[regression](pnp-shared-admission-regression.log),
[API](pnp-shared-admission-api.log), [native](pnp-shared-admission-native.log),
[ND](pnp-shared-admission-nd.log),
[provider upgrades](pnp-shared-admission-provider-upgrades.log),
[controller upgrades](pnp-shared-admission-controller-upgrades.log),
[build](pnp-shared-admission-build.log) and
[capacity contract](pnp-capacity.md).
