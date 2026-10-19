# Template preflight refusal retirement and fresh replanning

## Execution change

The existing guard-bound, one-use no-dispatch capability now covers template
project Create/Update, regular/composite draft Create/Update and commit Update.
Template guards issue this capability only when their private preflight callback
fails before invoking the original native mutation closure. The shared journal
claims it once, binds it to the original Submitting operation and performs the
existing owner-preserving compare-and-swap retirement. Reconciliation reports
`NativePreflightRefused`, waits and replans current desired/native state.

The original operation must retain the same config/owner/source/controller
identity and complete operation envelope, no task/created UUID, no physical
member/serial, no credential/dependency receipt and no cancellation. A prepared
composite may retain its exact nonzero preparation floor, no later than the
original submission time. Resumed Prepared metadata writes now claim the same
capability through the shared task-receipt helper; they do not fabricate a task
or bypass the existing preparation/settling path. A fresh composite operation
retains/revalidates its own durable floor after retirement.

No schema, transport, provider interface, physical admission or common worker
protocol was added. Wireless/feature-template guards delegate to the same private
preflight wrapper and retain their original parentless/immediate envelope.
Template bodies and values remain private in memory, outside journal identities,
errors and the new evidence.

A lost response, ordinary mutation error, foreign/reused proof, changed journal or
owner, recorded task/receipt, or proof lost across a process boundary is not
permission to clear an uncertain operation. Existing Submitting/Unknown records
cannot be retired by reconstructing a proof from an empty task ID. Deployments,
pruning, creator ownership and old taskless recovery retain their separate gates.

## Verification

- [Final race tests](template-no-dispatch-tests.log): 137 capability/journal cases
  cover wireless and seven template boundaries, owner/generation/operation/parent/
  preparation changes, recorded receipts, lost/foreign/reused proof, cancellation
  and conflict retry/exhaustion. Another 21 public manifest cycles cover native
  edits, preflight read errors and lost acknowledgements; 21 envelope cases check
  resource/action/parent/time/physical exclusions. The prior 50 snapshot/replay/
  concurrent template cases also pass in the same run.
- [Private real API tests](template-no-dispatch-api.log): all 21 public manifest
  cases pass against isolated Kubernetes v1.35.0 with real CRDs and generated
  UIDs/resource versions. The native service is a controlled HTTPS fixture.
  Unsubmitted composite preparation timestamps are advanced by the fixture;
  this does not prove elapsed-time behavior or installed-worker operation.
- [Native create/commit recovery](template-no-dispatch-native.log): the public
  reconciler reaches InSync for a generated project, regular draft/commit and
  composite draft/commit. An instrumented HTTP transport returns one synthetic
  preflight read failure at each original Submitting boundary; all successful
  reads and mutations use the real appliance. Exactly five refusals retire,
  including two prepared composites, with unchanged owners/history and no native
  effect before fresh replanning. Exactly five native mutations then complete.
  Native settling uses actual elapsed time. Both templates and the project are
  verified removed by their captured exact identities. Kubernetes is simulated;
  no installed cluster or device deployment is claimed.
- [Native create/update source cycle](template-no-dispatch-native-updates.log): a
  second isolated public flow first completes the five creation/commit actions,
  then edits the original ConfigMap source to change the project description and
  regular template body. Project Update, regular draft Update, a new regular
  commit and composite member-version recommit complete without replacing either
  template UUID. Across both revisions exactly nine original preflight refusals
  retire, including three prepared composite refusals, before exactly nine native
  mutations. Both revisions reach InSync with restarted no-replay checks, and
  exact generated template/project cleanup passes. Preflight faults are synthetic;
  successful APIs, native tasks, versions and elapsed settling are real. Kubernetes
  is simulated. Composite draft Update is not claimed as native-qualified here.
- [Member-version race regression](template-no-dispatch-member-race.log): the
  existing public settling fixture now requires proven refusal retirement,
  a different prepared operation ID, a new durable settling period preserved
  across restarts, then exact-once convergence. Its lost-receipt case still
  requires the original Unknown operation and no replay.
- [Strict documentation build](template-no-dispatch-docs.log) passes.
- [Configuration/template/wireless race regressions](template-no-dispatch-regression.log)
  pass after updating the member-version race contract. The earlier run caught
  its obsolete Unknown expectation; the replacement explicitly checks fresh
  operation identity and renewed settling before native dispatch.
- [Foundation regressions](template-no-dispatch-foundations.log) pass for shared
  configuration, ND, software lifecycle and software upgrades.
- [Pinned resource inventory](template-no-dispatch-ledger.log) retains all 93
  upstream 0.5.0 resource types with no missing/extra rows.

## Remaining gates

This closes automatic recovery of a proven pre-submit refusal, not uncertain
native effects. Task-bound template creator ownership, pruning, lost/taskless
outcome reconciliation, complete upstream schema/default/reference semantics and
installed cluster/worker qualification remain pending. Real-API and native tests
above are separate; they are not an installed end-to-end qualification. No native
template was deployed to a switch by these tests.

A follow-up adds [acknowledged metadata task recovery](template-task-recovery.md)
when the exact original task remains retained. Lost/taskless acknowledgements and
creator ownership still require separate implementation and qualification.
