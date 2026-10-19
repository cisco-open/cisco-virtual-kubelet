# Task-bound project and template creator receipts

## Public execution and ownership

New project and regular/composite draft Creates now use an explicit creator
operation namespace. The compiler still supplies native payloads and reads;
CVK's existing shared journal, configuration plan hooks, dispatch guard and atomic
action-receipt persistence provide lifecycle coordination. No transport, CRD,
provider interface, worker protocol or device-admission structure was added.

Before dispatch, fresh native snapshots are followed by the existing exact
Submitting owner/source/controller permit check. Whole missing creator sets and
their future commits reserve the shared 256-receipt/256-KiB limits before effects;
the final guard also repeats current creator history and per-operation capacity.
A preflight refusal retains the original one-use no-dispatch proof and can safely
retire/replan through the existing protocol, including prepared composites.

Ownership requires all of the following:

- Success of the complete original rooted task tree.
- A successful original root with serviceType NCTP and a canonical, nonzero UUID
  in data. Prose, embedded JSON, an unrelated service or foreign task ID cannot
  supply identity; free-form progress is not interpreted or stored.
- The task UUID matches the native project or draft UUID, declared name,
  regular/composite kind, original project UUID and declared attributes/content.
- Original task UUID and complete tree success are rechecked around native
  verification; owner/source/controller and exact journal state remain bound.
- One atomic status update publishes the Verified operation and creator receipt,
  followed by exact API readback. Finishing requires that original valid receipt.

The creator operation records only a digest of public project/template identity.
Template bodies, parameter values and private-content hashes never enter the new
receipt or operation identity. A source-mode discriminator distinguishes inline
from ConfigMap provenance, so schema loss of both ConfigMap UID/version fields
cannot masquerade as inline state.

Creator receipts remain immutable through later source revisions, ordinary
attribute/draft updates, commits and no-ops. Declared owned objects must retain
their original UUID, project and kind. Missing or same-name replacement objects
are fenced rather than adopted, recreated or updated. Existing unowned objects
retain their prior Retain update semantics and gain no creator receipt. An active
legacy Create retains its original observation contract and cannot retroactively
acquire ownership even when its native task happens to expose a UUID.

This is **Retain-only creator identity**, not a pruning protocol. Receipt loss
while completing a creator fences the Verified operation. A lost historical
receipt is never reconstructed from matching metadata; existing unowned Retain
semantics do not grant any deletion authority.

## Qualification

- [Focused creator race tests](template-ownership-tests.log): 42 task-result cases,
  30 receipt-envelope cases and 50 public flows cover project, regular and
  composite creation; malformed/foreign results; native replacements/kind changes;
  owner/source changes during proof; conflicts, acknowledgement loss and schema
  pruning. Removed creator receipts or source fields retain the original fence.
  No-op and update attempts against a replaced owned UUID are refused.
- [Inline and whole-set capacity tests](template-ownership-capacity-inline.log)
  qualify inline-source ownership and count/byte reservation before a native
  effect. The byte case begins below the limit with valid retained commit shapes.
- [Real private API cases](template-ownership-api.log): 149 cases pass against
  Kubernetes v1.35.0: 50 creator cases, one inline case, 77 original-task/legacy
  recovery cases and 21 preflight retirement cases. These use real CRDs, generated
  UIDs/resource versions and persisted status; the native HTTPS service is
  controlled. Composite fixture floors are advanced explicitly, so this does not
  establish installed-worker or real elapsed-time qualification.
- [Native public cycle](template-ownership-native.log): actual generated project,
  regular/composite Creates and commits followed by a source-driven project/draft
  update and member-version recommit. All nine original tasks recover from
  Unknown without replay; nine preflight refusals, including three prepared
  composites, retire before fresh dispatch. Exactly nine native mutations succeed.
  Three original task/source/UUID-bound creator receipts are verified and remain
  identical across both source revisions. Both revisions reach InSync; generated
  templates and project are removed by exact captured UUIDs. Native APIs, task
  trees and elapsed settling are real; Kubernetes, restarts and injected
  preflight refusals are simulated.
- [Native duplicate Create qualification](template-ownership-duplicates-native.log):
  a separate generated project and regular/composite fixtures first complete
  their real Create tasks. Repeating each exact successful Create payload returns
  HTTP 400 without a task; original UUIDs and declared bodies remain unchanged.
  No successful duplicate result can be mistaken for creator evidence in these
  checked paths. Exact template/project cleanup passes. This test does not claim
  a general guarantee for unqualified appliance releases or API variants.
- [Composite settling regressions](template-ownership-pacing.log) use canonical
  UUID/task results while retaining fresh-floor, restart, source/member-race and
  lost-receipt assertions.
- [Configuration/template/wireless race regressions](template-ownership-regression.log)
  and [foundation regressions](template-ownership-foundations.log) pass. The latter
  cover shared configuration, ND and direct/SWIM software lifecycle packages.
- [Strict documentation build](template-ownership-docs.log) passes.
- [Pinned resource inventory](template-ownership-ledger.log) retains all 93 resource
  rows from NetAsCode 0.5.0, with no missing or extra types.

The [Create Project API](https://developer.cisco.com/docs/catalyst-center/2-3-7-6/create-project/)
describes creation of a new project. The checked provider 0.6.1 template Create
implementation also treats duplicate POST failure separately from its optional
explicit adoption path. CVK's creator protocol has no name-based adoption fallback.

## Remaining gaps

Template/project source pruning, deletion-bound cleanup, recreation/tombstones,
private native-state baselines for destructive removal and full upstream field/
default/reference coverage remain pending. Composite draft Update has fixture
coverage but is not claimed as native-qualified by the source cycle above.
Installed cluster/manager/worker execution remains unqualified; separate private
API and native harnesses do not establish a combined installed end-to-end flow.
Taskless outcomes still require evidence-based recovery. No template was deployed
to a switch by these tests. The pinned template-version Delete is state-only;
native version deletion is not a missing parity feature to invent.

The subsequent [private creation baseline implementation](template-private-baselines.md)
adds optional historical keyed snapshots. Latest-applied state and destructive
removal qualification remain pending.
