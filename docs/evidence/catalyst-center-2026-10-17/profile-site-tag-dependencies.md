# One-source tag membership and association removal

This increment composes the existing private nonempty wireless tag membership
Updates with profile/site association pruning. A single complete NetAsCode 0.5.0
source revision can remove a site from its owned tags and from the parent
assignment. Previously the association reader saw the old tag references first,
so the manifest could not progress through the necessary tag Updates.

## Execution and authority

Use the existing config UID, approved `/network_profiles` comparison-key
projection, private tag baselines and association application receipts, with
`prunePolicy: Delete`. No new manifest field, CRD, transport, store, controller,
physical admission path or persisted dependency graph is added.

The normal strict association snapshot remains first. Only when reference checks
refuse it may the planner consider an already-qualified v2 private membership
Update. It reconstructs every pending membership reduction, checks each retained
native UUID/parent/keyed baseline and unused affected subtree, and projects those
memberships solely to validate the complete association candidate cohort. The
native tag record must exactly match that projection's old observation; foreign,
unselected, unknown or replaced tags cannot borrow it. Every projected tag must
remove the relevant exact/ancestor/descendant site reference before it helps the
candidate pass.

Only that original tag Update can be dispatched. Ordinary additions and parent
changes cannot use this exception. Its original key/source/native guards remain,
with additional repeated complete projection, candidate, ownership and history
checks inside the same one-use preflight/refusal mechanism. The journal, original
task trees, independent native readback and atomic latest tag receipt remain the
completion path.

The next reconcile rebuilds the plan from current native observations and
persisted receipts. Association DELETE is eligible only after a **strict** fresh
snapshot sees the actual tag references gone. Its submission guard and original
acknowledged-task completion use strict observations too. The in-memory projection
is never attached to deletion verification or persisted as evidence of deletion.
Confirmed unassignment atomically replaces its existing association slot with the
normal removal tombstone.

See `examples/configs/catalyst-center/profile-site-tag-dependencies.yaml`. Its
precondition is previously recorded two-site baselines and assignment receipts;
apply it as a revision of the existing config owner.

## Qualification

Terminal fixture/API gates have passed. Native qualification uses only
generated disabled SSIDs, unused wireless profiles and empty areas. Kubernetes
actor/storage faults are simulated in the native harness; the separate temporary
Kubernetes API validates actual serialization, status writes and generations.
No device assignment, provisioning or software operation is intended by this
metadata workflow.

Passing gates:

- [26 local lifecycle cases](profile-site-tag-dependencies-local.log) and
  [26 actual API cases](profile-site-tag-dependencies-api.log), across single-tag
  and bulk-policy-cohort origins. Qualifies ordered Updates/DELETE, Retain,
  foreign/omitted/late tags, device/read refusals, submission-time consumers,
  post-task reference changes, lost acknowledgement, failed task trees, changed
  source and changed owner. The fixture transport refuses DELETE until actual
  native tag memberships have stopped referencing its original UUID.
- [Nine projection-scope cases](profile-site-tag-dependencies-projection.log)
  include a two-association candidate cohort. A device/PnP consumer or failed
  read on the other candidate, native field/UUID changes, foreign tags, ancestor
  references and malformed projections refuse planning. A successful prospective
  snapshot still fails strict observation while the actual tag reference remains.
- [Ordinary parent-change refusal](profile-site-tag-dependencies-parent.log):
  two additional local and two API cases assert zero parent/tag/association writes
  when a parent update would otherwise precede the reference-blocked tag cohort.
  Combined lifecycle coverage is 28 local and 28 API cases.
- [Explicit late-refusal retirement](profile-site-tag-dependencies-refusal.log):
  four local and four API cases require both injection reachability and absence
  of a retained operation after proven no-dispatch, with zero unassignments.
- [Shipped example](profile-site-tag-dependencies-example.log),
  [foundation/ND/direct GNOI](profile-site-tag-dependencies-foundation.log), and
  [SWIM](profile-site-tag-dependencies-swim.log) gates passed.

The [compatibility regression gate](profile-site-tag-dependencies-regression.log)
passed in 503.378s: 82 local + 82 API strict association-prune cases,
34 + 34 private membership cases, and 44 + 44 retained-receipt cases,
plus producer/scope/capacity/example checks. Its controller-runtime uninitialized
logger notice prints a diagnostic stack during API startup; it is not a failed
test or task. All selected cases have terminal passing results.

[Native single/cohort qualification](profile-site-tag-dependencies-native.log)
passed in 1463.711s: 585.33s for the single-policy workflow and 876.00s for the
bulk-policy workflow. The evidence records 26 distinct original public metadata
tasks recovered with full trees and 26 proven no-dispatch refusals retired, with
exact mutation counts and no replay.

Each workflow used **one complete source revision** for its reduction phase:
two or three same-UUID private tag Updates verified before the first site
unassignment. The actor then verified that original association task, retained
its atomic tombstone, reached InSync and passed repeated no-op reconciliations.
The log order was independently checked against the task IDs recorded in
[source/order evidence](profile-site-tag-dependencies-sources.json).

Five membership Updates and two dependent unassignments completed across those
source revisions. Later supervised exact-ID tag cleanup permitted two remaining
manifest-driven unassignments. Latest private tag rows remained at four and five,
with ten retained member proofs across nine rows. Both unrelated inventory
baselines were restored; no device assignment, provisioning or device operation
was submitted. Whole-tag, profile, SSID and area fixture cleanup is still
supervised test tooling and grants no manifest-driven whole-tag deletion.

The native API is real; the native harness simulates Kubernetes actor/storage
faults, separately qualified by the temporary API gates. Current implementation,
test and example hashes are recorded as source fingerprints, not installed-binary
attestations. Installed workers and physical WLC/AP effects remain unqualified.

## Remaining full-goal scope

This closes dependency ordering for qualified **nonempty tag-member** reductions,
not whole-tag/profile/site deletion, tag omission-driven pruning, imported-resource
adoption, larger scope envelopes, arbitrary prerequisite graphs, key migration or
handoff. Whole-tag cleanup in the native harness remains supervised exact-ID test
tooling. Installed actors, WLC/AP effects, other remaining resource lifecycles and
full 93-resource model parity are still open.
