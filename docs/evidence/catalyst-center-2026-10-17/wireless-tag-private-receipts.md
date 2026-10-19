# Private wireless tag task baselines

Site tags, single policy tags and all-new policy-tag cohorts can now retain
private native baselines through the existing `ActionApplications` writer. A
single asynchronous operation publishes one atomic row; its existing bounded
`EffectReceipts` set records the complete logical member set, observed UUIDs and
keyed comparisons. The pinned NetAsCode 0.5.0 tag model is unchanged. There is no
new CRD field, controller, transport, Kubernetes Secret read permission or private
state store.

## Existing operational key channel

The existing `PrivateComparisonKey` reference/projected-file resolver now accepts
one network_profiles-only reference at `/network_profiles`. The complete model
is parsed before the operational key is resolved. The key remains an independent
32-byte hex value in the controller-approved worker projection. It is never
injected into the model, sent to Catalyst Center or included in public receipts.
The example is `examples/configs/catalyst-center/wireless-tag-baselines-apply.yaml`.

Key identities are scoped to the controller/config UIDs and endpoint through the
existing comparison mechanism. Tag native MACs have a separate domain and bind
namespace, parent UUID, logical member, native UUID, complete known tag fields,
site hierarchy/UUIDs, AP-profile UUID and selected native zone metadata. Native
values and unkeyed fingerprints of their contents do not enter status. Unknown
native fields or unresolved baseline dependencies refuse the private path.
Custom flex inventory remains unqualified; the existing built-in default is
preserved. Key removal/rotation and changed custody remain fenced; no automatic
key migration is introduced. Legacy workflows without this operational reference
keep their existing behavior.

## Producer/result contract

A versioned tag operation preserves the existing selector and additionally binds
the logical member-set digest, comparison-key identity and complete public
bookkeeping predecessor. This works for one member and a bulk cohort; the
cohort is not split into invented operations. Existing one-use dispatch and
proof-certified preflight refusal retirement still own submission. Planning
checks the full declared tag cohort's receipt capacity before its first tag
mutation, including both tag kinds.

Readback reconstructs the original public selector. A raw read-only compiler can
verify attributes without a comparison key, while private receipt publication,
retained custody and release remain key-gated. Accepted and original acknowledged
Unknown tasks keep the existing full-tree/source/owner/controller/journal checks.
Missing acknowledgements cannot infer a task or acquire a receipt.

Publication requires complete stable member readback around the original task
root/tree. The exact producer member set and Update UUID must match before an
atomic Verified operation/receipt is stored. A pre-publication validation also
checks the proposed entire receipt state. Public row and active-cohort seals
refuse missing, edited or inconsistent rows; they are consistency checks rather
than authentication against a privileged status writer. Native comparisons
remain keyed. Verification time and original source/task/owner evidence stay in
the same existing row.

Create-task baselines are retained separately from latest Update baselines.
An update replaces its existing logical-member slot, including at the 256-row
limit. It preserves the prior Create-task row and sibling cohort proofs. Native
UUID replacement, private field/dependency changes, logical/native aliases and
inconsistent creator/member history refuse further writes or no-op convergence.
Matching imports mint no baseline. Existing-object Updates may record application
evidence without inventing a Create-task receipt. Legacy acknowledgements are
observed under their original contract and are not adopted.

## Qualification

The [final race/API gate](wireless-tag-private-receipts-qualification.log) covers
44 cases locally and the same 44 against temporary Kubernetes API storage,
plus 10 producer-set protocol cases. They cover single/cohort creation and additive
update, imported defaults, legacy recovery,
complete-cohort capacity, latest replacement at capacity, receipt/member edits,
history races, changed producer sets before publication, key removal/rotation,
external field/UUID/alias changes, failed/missing task trees, lost acknowledgements
and source/owner drift. Protocol cases recompute public seals after altering
member sets, UUIDs or creator identity, independently checking the producer
binding rather than relying on a stale checksum. Standalone retained-selector
readback is explicitly exercised without weakening key-gated release.

[Broader race/API regression](wireless-tag-private-receipts-regression.log) covers
existing wireless/profile, association prune/recovery and network-application
contracts. [Shared controller, ND and lifecycle/GNOI checks](wireless-tag-private-receipts-foundation.log)
remain separate from product mutation qualification. [All 47 SWIM unit contracts](wireless-tag-private-receipts-swim.log)
passed in 4.041 seconds. The broader race/API command
passed in 591.841 seconds with 324 API cases, including the initial 40 private
receipt cases. The final focused/API gate additionally covers the read-only
selector correction and subsequent alias/pre-publication defensive cases.

The first [native attempt](wireless-tag-private-receipts-native-initial.log)
verified private tag Create receipts but stopped at standalone retained readback,
whose raw verifier had not reconstructed the private selector. The verifier was
corrected; it only reads attributes and does not publish or release receipts.
An [exact retained-fixture cleanup](wireless-tag-private-receipts-cleanup.log)
removed generated UUIDs after independent identity, empty-site, onboarding and
unrelated-baseline checks. The cleanup helper initially used the read collection
for deleting areas; the remaining two areas were retained, then removed through
the existing `/areas` mutation endpoint. No creation was replayed during cleanup.

[Fresh native qualification](wireless-tag-private-receipts-native.log) uses the
same generated disabled-SSID/empty-area single/cohort fixtures with ephemeral
local projected comparison keys and the public reconciler. Association pruning
continues through its existing source/owner/task/tombstone protocol with the
operational key reference present. Tag fixture cleanup is exact-ID supervised
cleanup; this does not claim manifest-driven tag deletion. Kubernetes actors,
storage, interruptions and preflight faults are simulated in the native harness;
the temporary API gate independently qualifies actual Kubernetes persistence.
No WLC/AP provisioning, software upgrade or physical device action is performed.
The fresh single-tag workflow passed in 379.96 seconds and the policy-cohort
workflow in 522.75 seconds (904.904 seconds for the full command). They recovered
21 original public metadata tasks, including nine private tag tasks. Nine retained
Create/Update rows contain ten member proofs. Both workflows reached final
InSync/no-op, completed four association unassignments and restored their prior
metadata baselines.

[Source fingerprints and terminal counts](wireless-tag-private-receipts-sources.json)
identify the implementation and qualification scope. [Strict documentation build](wireless-tag-private-receipts-docs.log)
verifies the evidence/operator links.

## Remaining full-goal scope

These are task-bound verified Create/Update baselines, not an assertion of
exclusive native creator attribution. A [read-only task projection](wireless-tag-task-attribution.json)
found no observed tag UUID in this generated task's root or tree. No raw task
progress, payload or credentials are retained in that projection. The native
attribution/ownership policy and destructive lifecycle still need qualification;
this milestone grants no tag deletion, membership removal, adoption or handoff.
Dependent tag pruning, larger/mixed-cohort migrations, baseline retirement,
installed workers and WLC/AP behavior remain pending. The 93-resource full
model/schema/default/lifecycle goal remains active and incomplete.
