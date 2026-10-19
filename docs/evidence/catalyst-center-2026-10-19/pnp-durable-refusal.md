# Durable certified refusal in the existing controller journal

An optional MetadataHandoff field now retains metadata preparation custody in
the existing NetworkController configuration journal. It preserves the original
Prepared operation, scope binding, worker Pod identity, configured lease
namespace and canonical cohort. RefusedSubmissionAt and RefusedAt represent a
certified pre-HTTP refusal. They are separate from successful ActionApplications.

When consuming the original guard-bound one-use pre-submit proof, the PnP
retirement path checks the exact preparation, owner, worker, protocol and
identities. It then records both refusal timestamps and clears only that exact
journal slot in one status CAS. It verifies API-normalized retained evidence
after the update. Mismatched worker/operation evidence retains the journal.
Native or acknowledgement errors cannot mint this proof or infer refusal from
absence.

Retained custody holds new plans, preparations, starts and finishes. This staged
increment does not treat ReleasedAt alone as dispatch permission. The legacy
absent-field path keeps its existing behavior. The worker scope digest excludes
the handoff itself, preventing a retained seal from changing its own binding.

## Schema and qualification

The API type, deepcopy methods and both CRDs include the optional field. The new
property and custody transition rules match repository-pinned controller-gen
output. Prepared binding and certified refusal evidence are immutable. The root
and configuration-level guards reject removal of unreleased custody, including
whole-status/configuration replacement by a writer that omits the new field.
CRD transition validation is not authenticated actor admission.

Controlled and real Kubernetes API tests exercise the staged certified-refusal
CAS, reconstructed durable evidence, unchanged creator/success receipts, wrong
worker/operation refusal, new-work barriers, binding rewrite denial and field or
configuration removal denial. The handoff fixture models pre-existing custody
in a submitting operation solely to test composition with the original guard;
it is not an installed manager seal/claim execution.

The complete provider/API matrix passes 129 test/package events. Shared API,
controller, ND and upgrade checks pass 3289 events across seven packages;
selected PnP/journal/upgrade checks pass 1049 events. The main binary passes.
The existing schema-preservation test was extended to the fourth typed operation
copy. Early schema placement, formatter-altered CEL literals and timestamp
normalization issues were corrected before final qualification.
[Results and fingerprints](pnp-durable-refusal.json) record the gates.

## Remaining runtime handoff

No manager seal writer, authenticated metadata actor/version admission, native
fence claim/release or new public dispatch is enabled. New handoffs must remain
staged until those pieces are implemented. In particular, manager release must
consume either this original certified no-dispatch evidence or the original
acknowledged and independently verified absence receipt; uncertainty cannot be
converted into replay authority. Fence acquisition, success/refusal release,
lost-response recovery and installed actor/native qualification remain pending.

CR deletion and missing-source context, full native consumer protection and all
other remaining pinned 93-resource execution/hardware gaps remain open. No
Catalyst Center mutation or installed-cluster rollout occurred in this increment.
The full pinned NetAsCode model remains incomplete.
