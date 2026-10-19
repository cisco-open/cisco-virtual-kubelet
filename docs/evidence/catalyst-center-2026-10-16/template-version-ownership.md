# Native template version ownership and immutable history

New privately baselined, task-owned regular/composite commits now correlate the
native version with the original journal operation. An explicit version-v1
operation marker requires a matching version UUID and private version fingerprint
in the applied-state receipt. Existing legacy commits retain their original
contract and never acquire ownership from a matching comment or body.

The commit keeps its existing CVK content/dependency comment and appends the
cryptographically random shared operation ID in a bounded execution suffix. This
is computed inside the existing one-use preflight guard, following fresh durable
owner/source/journal/capacity checks. Refusal retains the same guard-bound proof;
no separate submission or task journal is added. Native content and user values
never enter this public identifier.

Completion requires the complete original task tree and declared native state,
exactly one canonical version UUID with that operation nonce in the native
version metadata, and independent committed body/project/version validation.
The selected version must be the current latest version. If the body response
also supplies a version comment, it must agree with authoritative version
metadata; omission is supported. Two captures bracket the existing full
applied-state snapshot and repeat task/native/source checks before one atomic
Verified receipt publication. Nonces in prose or foreign comments cannot supply
version authority. Taskless outcomes remain fenced.

The result retains a separate HMAC of that version's complete committed body and
its native metadata entry, including unknown fields and exact numeric values.
No raw content, key material, or unkeyed content hash is placed in status.
Subsequent draft/commit writes archive the prior strict version receipt unchanged
apart from its archive namespace. The shared completion protocol still sees one
receipt per original operation, and the immutable template creator stays intact.
Historical rows remain bound to their original creator/source/UUID/projection key.
Duplicate version UUIDs and operation IDs are refused.

The complete-version-set checker requires every native committed version to have
its original strict commit receipt and unchanged private body/metadata proof.
External versions, missing history, changed old bodies and foreign uncommitted
UUIDs cannot be adopted by a later latest-state snapshot. Empty history does not
grant authority without a privately baselined creator. This checker is a removal
prerequisite, not a Delete permit or a public pruning implementation.

Archives consume the existing 256-receipt/256-KiB shared limits. Capacity checks
include the extra immutable record before a version-bearing latest row can be
replaced, and reserve the larger strict commit envelopes for new creator sets.
A full journal fences further mutation rather than dropping history. Replacing a
non-version latest row does not require an extra archive slot.

## Qualification

- [Focused public/structural tests](template-version-owned-tests.log) cover nonce
  correlation for regular/composite commits, absent/foreign/duplicate nonces,
  noncanonical UUIDs, pruned version fields, body-comment omission and absent
  metadata comments. Cohort cases cover old private/unknown-number/metadata edits,
  external or missing versions, receipt/history/key loss, duplicate ownership and
  legacy non-adoption. Archive/capacity and comment grammar cases also pass.
- [Real private API suite](template-version-owned-api.log) passes 110 cases:
  18 version-specific cases, 65 applied-state regressions and 27 creator-baseline
  regressions against Kubernetes v1.35.0. These use real CRDs, generated identities
  and persisted status; native HTTPS responses and elapsed composite floors are
  controlled. This does not establish installed manager/worker execution.
- [Real API archive round trips](template-version-owned-archive-api.log) preserve
  two original version receipts across a later draft update without content
  disclosure. Earlier retained operations are seeded and normalized through the
  API before completion; the new draft passes its real Verifying-to-Verified
  persistence boundary. Native bodies/tasks in this structural case are synthetic.
- [Final configuration/template/wireless race regressions](template-version-owned-regression.log)
  and [shared ND/direct/SWIM foundations](template-version-owned-foundations.log)
  pass. The final malformed foreign-draft refusal is covered by the final race
  suite; the API suite uses valid native-version shapes plus its stated faults.

- [Native public source cycle](template-version-owned-native.log) passes in
  568.41 seconds: four nonce-bound regular/composite version UUIDs and unchanged
  private body/metadata proofs are independently checked against the complete
  native version sets. Two original strict commit receipts survive source-driven
  updates unchanged in the archive; three template/project creator receipts also
  remain immutable. Six latest-state snapshots, nine acknowledged-task recoveries
  without replay and nine guard-bound refusals (three prepared composites) pass.
  Both source revisions reach InSync and exact generated template/project cleanup
  is verified. Native APIs/task trees and elapsed settling are real; Kubernetes
  persistence, worker restarts and injected refusal/Unknown events are simulated.
  No template is deployed to a device. Composite draft Update remains separately
  fixture-qualified; this cycle covers composite creation and member recommit.
- [Strict documentation build](template-version-owned-docs.log), whitespace checks
  and the [93-resource scope audit](template-version-owned-ledger.log) pass. The
  scope audit confirms no missing/extra resource rows, not full implementation.

## Next destructive lifecycle gates

Complete profile/composite/onboarding reference observation, ordered child
removal, final native equality, Delete-task/absence receipts, source pruning,
recreation/tombstones and installed actor qualification remain pending. Existing
unowned versions intentionally block complete cohort ownership. The pinned
provider's version Delete is state-only; this implementation adds no native
version deletion API or permission. Full schema/default/import parity remains
unfinished.

A read-only [reference API capability probe](template-reference-api-capabilities.json)
confirmed HTTP 200 and structural list/count envelopes on the lab's generated
template. That generated fixture returned zero associations. This proves endpoint
availability only, not complete references or permission to remove it. The
[official API changelog](https://developer.cisco.com/docs/catalyst-center/2-3-7-9/api-changelog/)
lists the reverse template-to-network-profile endpoints. Their complete paging,
count reconciliation and nonempty association behavior still need qualification.
