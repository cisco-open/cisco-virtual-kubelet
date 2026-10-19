# Acknowledged template metadata task recovery

## Implemented execution path

Projects, regular/composite drafts and commits now use the existing optional
retained-task observation hooks. The selector is built from the actual compiled
project/template executors and exact group namespace. Tags, memberships,
deployments, unselected resources and unrelated providers gain no recovery path.
The common reconciler still requires the original owner, controller generation,
config generation, source UID and source resource version.

An Unknown operation must retain its valid original task ID and nonzero submission
time. It must have no physical group/serial, created-object receipt, cancellation,
credential/history or dependency envelope. Projects remain parentless; drafts and
commits require a retained valid project UUID. A prepared composite may retain
its exact valid floor, no later than submission; ordinary regular templates and
projects cannot acquire a composite preparation floor. Commit recovery requires
Update with the retained base template UUID.

Recovery observes the complete rooted task tree before using the existing
Unknown-to-Verifying transition. A pending/failed/foreign child or unavailable tree
cannot fall back to a successful root. Existing native verification still checks
project/target identities and declared draft/committed content. Missing native
state retains Verifying; missing tasks, changed owners/sources and unqualified
child-task evidence remain fenced. No submission permit or mutation closure is
issued during recovery. Normal Accepted metadata and unrelated task observers
retain their previous completion contract.

Successful Create recovery retains the ordinary **unowned Retain-only** convergence
contract. A same-name replacement can satisfy that declared-state contract, but
cannot become proof that CVK created its UUID. No per-object creator receipt or
prune right is fabricated. Existing commit action receipts remain commit/pacing
evidence rather than version-object ownership. No schema, transport, physical
admission or provider interface was added.

## Verification

- [Public recovery and selection race tests](template-task-recovery-tests.log):
  70 public cycles cover seven operation boundaries and original-task success,
  task loss, source/owner/parent changes, missing native state and failed/pending/
  foreign/unavailable child trees. They assert original operation/task identity,
  exact retained Unknown/Verifying phases, no replay and commit-only receipts.
  Another 190 selector cases cover exact grouped/ungrouped namespaces and invalid
  phase/identity/action/physical/parent/preparation envelopes.
- [Private real API tests](template-task-recovery-api.log): all 70 public cases
  pass with Kubernetes v1.35.0, real CRDs, UIDs/resource versions and persisted
  operation/receipt state. Native HTTPS is controlled. Unsubmitted composite
  preparation timestamps are advanced by the fixture; this is not elapsed-time
  or installed-worker qualification.
- [Native public cycle](template-task-recovery-native.log): isolated project and
  regular/composite drafts/commits, followed by source-driven project/draft
  updates and member-version recommit. The test driver retains each of nine
  actual acknowledged tasks as Unknown with every other operation field intact.
  The public reconciler observes each original native task tree, verifies state,
  reaches InSync across both revisions and preserves template UUIDs. Exactly
  nine native mutations complete; no acknowledged task is resubmitted. The same
  flow also rechecks the prior nine preflight refusals and three prepared
  composite refusals. Both templates and their project are removed using exact
  captured identities. Kubernetes/restarts and preflight faults are simulated;
  task trees, native state, successful mutations and elapsed settling are real.
- [Configuration/template/wireless race regressions](template-task-recovery-regression.log)
  pass with the existing Accepted task paths and preflight retirement contracts.
- [Strict documentation build](template-task-recovery-docs.log) passes.
- [Foundation regressions](template-task-recovery-foundations.log) pass for the
  shared configuration, ND, software lifecycle and upgrade packages.
- [Resource ledger](template-task-recovery-ledger.log) retains all 93 distinct
  resource types from pinned NetAsCode 0.5.0, with no missing or extra rows.

## Ownership evidence and remaining work

The [structural task inspection](template-task-identity-inspection.json) checked
five original native tasks through legacy task and modern detail endpoints.
Project Create and regular/composite draft Create expose canonical UUID data;
the data values match independently read generated project/template UUIDs in both
views. Legacy serviceType is NCTP. The two checked commit tasks return noncanonical
data with no exact generated UUID match. Only field names, type/shape and exact
comparison booleans are retained; no arbitrary task body, progress, username or
credential value is recorded.

These observations provide a concrete next path for task-bound project/template
creator receipts. They are not an implemented ownership protocol, do not prove
version-object ownership, and do not imply that all API variants were inspected.
A future owned path must bind the original task result to native UUID/name/kind,
project and source, persist the receipt atomically and qualify retention, updates,
pruning, deletion and restart recovery. Complete upstream field/schema semantics,
lost/taskless-outcome reconciliation and installed cluster/worker qualification
also remain pending. Separate API/native harnesses are not a combined installed
end-to-end qualification. No template was deployed to a switch in these tests.

The [pinned version lifecycle inspection](template-version-lifecycle-inspection.json)
also confirms that provider 0.6.1 TemplateVersion Delete removes Terraform state
without making a native API request. Its Create posts a commit and selects the
version by comment; the pinned module derives comments from content/parameter or
composite-member semantics. CVK must qualify equivalent commit/version-reference
semantics, but native version deletion is not a parity requirement to invent.
Project/template ownership and destructive removal remain separate work.

A subsequent [creator ownership implementation](template-creator-ownership.md) now
uses the native NCTP UUID evidence for explicitly new creator operations. This
does not retroactively promote the legacy unowned recovery qualified above.
