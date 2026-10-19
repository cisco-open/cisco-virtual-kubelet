# Private wireless tag membership removal

This increment adds same-UUID updates that remove members from a **nonempty**
site/policy tag. It uses the pinned NetAsCode 0.5.0 `network_profiles.wireless`
model. Tag deletion, omission-driven tag pruning, creator exclusivity, adoption,
controller handoff and physical WLC/AP qualification remain separate gaps.

## Admission and architecture

Update the existing `NetworkControllerConfig` owner with `prunePolicy: Delete`
and the same existing `PrivateComparisonKey` reference at `/network_profiles`.
`Retain` and a missing private baseline preserve the prior removal refusal.
The selected tag must retain its native UUID, parent UUID, key and full keyed
native/dependency baseline. A matching import or legacy acknowledgement does not
qualify on its own; a recorded private Update can establish a managed baseline
without inventing a Create record or permission to delete the tag.

No CRD, key channel, store, controller, task transport or ND/GNOI upgrade path is
added. The existing tag executor, one-use journal, source/owner/controller/CAS
checks and atomic action-application writer remain responsible for execution.
The existing association-pruning device/PnP guard is factored into one shared
reader helper rather than reproduced as a second implementation.

A `tag-proof:v2` selector retains the canonical tag selector, a canonical encoded
set of affected site UUIDs, producer member-set digest, key identity and public
bookkeeping predecessor. Site membership removal checks removed site subtrees;
zone removal checks every prior site member. The selector must fit the existing
512-character journal limit, including its namespace. Oversized removals are
refused during tag preflight before prerequisite/tag dispatch. Stage smaller
source revisions when required; arbitrary-size destructive cohorts are not
qualified.

Affected subtrees must have no assigned devices or PnP consumers. Failed reads,
unknown site identities and unresolved active onboarding scope refuse execution.
The checks run in initial source preflight and again inside the original native
submission guard. The durable affected-site selector also rechecks those scopes
when observing original acknowledged tasks and before publishing the private
result. A consumer appearing after acceptance leaves the original task fenced;
it does not trigger replay, release a new receipt or claim that an earlier
mutation was rolled back.

The original complete task tree and stable independent native/dependency
readback gate receipt publication. Each verified Update replaces the existing
latest Update slot while retaining Create-task and sibling cohort proofs.
Uncertain acknowledgements, task trees, source and ownership retain the original
operation. Empty tag membership and whole-tag deletion remain refused.

## Qualification

All gates below have terminal passing results. Native qualification uses
generated disabled SSIDs, unused wireless
profiles and empty areas; it grants no device or WLC/AP deployment authority.
The native API is real. Kubernetes actor restarts/storage and injected faults in
that harness are simulated; a separate temporary Kubernetes API exercises real
status serialization and atomic writes.

Terminal local/API gates:

- [Thirty-four lifecycle cases per local/API boundary](wireless-tag-membership-qualification.log),
  completed by [removal-specific uncertainty cases](wireless-tag-membership-uncertainty.log)
  and [zone membership cases](wireless-tag-membership-zone.log).
  Retain, absent baselines, device/descendant/PnP consumers, missing read evidence,
  a consumer appearing at submission or after acknowledgement, lost acknowledgement,
  failed/missing task trees, changed source and changed owner remain fenced.
- [Protocol/example/capacity gate](wireless-tag-membership-protocol.log): ten scope
  envelope subcases, four affected-scope cases, one whole-source capacity refusal
  and the shipped example. Create/cohort/future/duplicate/noncanonical/oversized
  v2 selectors do not gain recovery authority.
- [Preflight injection reachability](wireless-tag-membership-preflight.log): four
  local/API cases assert that the submission-time consumer injection was reached.
- [Earlier integration regression gate](wireless-tag-membership-regression.log):
  82 local + 82 API association-prune cases, 44 + 44 retained-receipt cases and
  initial 20 + 20 member-removal cases passed. [Final shared-reader requalification](wireless-tag-membership-shared-reader.log)
  passed in 303.684s with all 82 + 82 prune and 30 + 30 removal cases, plus
  protocol/example/capacity tests. Zone execution adds four + four cases in its
  separate final-source gate; native zone changes are not claimed.
- [Foundation/ND/direct GNOI](wireless-tag-membership-foundation.log) and
  [SWIM](wireless-tag-membership-swim.log) passed. The SWIM selection ran 24 top-level
  tests with 28 subcases.

Initial fixture failures were corrected rather than treated as product failures:
old payload assertions assumed memberships could only grow, and source-fault
injection replaced WLAN1 after the model had advanced to WLAN2. The API gate now
checks a real source mutation with actual generation handling.

[Native single/cohort qualification](wireless-tag-membership-native.log) passed in
1260.696s. The single-tag workflow took 506.42s and the bulk-policy workflow
751.87s. Both created disabled SSIDs, unused profiles and empty areas, performed
additive updates followed by same-UUID membership reduction, recovered original
tasks from Unknown, reached InSync, verified repeated no-op reconciliation and
restored their unrelated inventories. Across both workflows:

- 26 distinct original public metadata tasks recovered with full task trees and
  26 proven preflight refusals retired without replay.
- Five v2 membership Updates completed: two single-workflow tags and three tags
  in the bulk-policy workflow.
- Latest private rows stayed bounded at four and five respectively, preserving
  Create/sibling proofs and ten final member proofs across nine retained rows.
- Four manifest-driven association removals completed after supervised exact-ID
  tag cleanup, with atomic removal tombstones and final no-op checks.
- Both baseline inventories were restored. No device assignment, physical
  provisioning or device operation was submitted.

Whole-tag, profile, SSID and area fixture cleanup remains supervised test tooling;
it is not manifest-driven tag deletion authority. AP-zone membership execution is
qualified by the temporary API fixtures, not by a native zone-change or WLC/AP run.
The final scope-capacity preflight and shared-reader extraction were completed
while the native workflow was executing and independently requalified by the
final protocol/API gates. [Source fingerprints](wireless-tag-membership-sources.json)
identify current implementation/tests; they are not installed-binary attestations.

See the runnable model in
`examples/configs/catalyst-center/wireless-tag-membership-apply.yaml`.

## Remaining full-goal scope

This increment is not full 93-resource parity or merge readiness. Remaining work
includes full tag/profile destructive lifecycle and attribution policy,
empty-membership semantics, dependent-tag pruning and association-removal
ordering, larger removal envelopes, adoption/handoff/key migration, remaining
resource/schema/default/lifecycle parity, installed workers, public physical SDA
execution, and hardware-dependent wireless qualification.
