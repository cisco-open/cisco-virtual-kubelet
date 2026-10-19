# Discovery config cleanup: deletion-bound preparation

This adds a private preparation step after the ownership observer. It reuses the
existing configuration journal CAS, capacity checks and owned discovery identity.
No public reconciler/finalizer is enrolled, and the existing submission gate still
refuses discovery mutations on deleting configs.

The Prepared operation binds the exact config deletion and spec digests, retained
evidence digest, source UID/version, config/controller generations and target UUID.
Preparation requires unchanged API revisions and owner evidence. It verifies the
persisted operation after the status CAS. Empty candidate sets are not treated as
completion. An occupied journal remains occupied; this stage neither drains nor
cancels its operations.

Controlled tests refuse altered deletion/spec/UID/generation/version, section,
action, resource, target and additional identity; a controller revision race is
also refused. Existing ownership-observation failure cases remain covered.
Real Kubernetes API tests verify the terminating config and Prepared record under
the CRD schema. Submission fails both with the private context and after that
process-local context is removed. No ownership receipt is changed.

Live qualification created owned discovery job **158**, verified original-task
ownership/restart/no-op, and prepared cleanup against its native fingerprint in a
**separate simulated Kubernetes API**. Preparation made no native mutation and
submission was refused. The pre-existing supervised cleanup then deleted exactly
158 (task `01a14cb3-1f36-70a1-b1ca-24a9584b6fe5`) and restored the discovery baseline.
This is not installed-worker or automatic CR-deletion qualification.

Remaining: reconstructable deletion-bound dispatch, original-task recovery,
receipt publication with full finalization-proof capacity, exact absence receipts,
manager enrollment/completion/release and installed qualification. The 93-resource
ledger continues to distinguish implementation and qualification from full parity.

The added metadata-update race initially hit the worker-generation fence in the
real API fixture. The race now changes/restores only status ownership; production
checks were not relaxed. The [initial refusal](discovery-cleanup-preparation-api-initial.log)
is retained separately.

Evidence: [source hashes and native records](discovery-cleanup-preparation-validation.json),
[native run](discovery-cleanup-preparation-native.log),
[real API tests](discovery-cleanup-preparation-api.log),
[regressions](discovery-cleanup-preparation-regression.log),
[ND and software upgrades](discovery-cleanup-preparation-nd-upgrades.log),
[all 47 non-live SWIM functions](discovery-cleanup-preparation-swim.log) and
[production build](discovery-cleanup-preparation-build.log).
