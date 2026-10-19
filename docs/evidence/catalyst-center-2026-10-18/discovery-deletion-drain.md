# Discovery deletion: drain original accepted work

The private deletion lifecycle can now drain an exact original inventory discovery
operation before preparing cleanup. It reuses original task observation, the owned
discovery verifier, atomic action receipts and journal settlement. No new native
create/replacement is authorized, and no public manager/finalizer hook is enabled.

A private observation context binds config UID, complete current spec/deletion,
original operation ID/generation and unchanged source. It accepts only the same
config generation or the next deletion generation. Strong native fingerprints,
pure-discovery source, current owner, controller and original source UID/version
remain required. The submission gates do not accept this context. Finalization
preparation explicitly refuses a combined drain/dispatch context.

An untouched Prepared operation can be retired without native submission. Accepted
and supported known-task Unknown work observes only its original task. Verifying
publishes the original generation's native UUID receipt through the existing owned
callback. Verified settlement requires that exact receipt. Submitted operations
later than the API deletion timestamp's precision window cannot enter this bridge.
Missing task acknowledgements, in-flight Submitting records, failed/mismatched
tasks, source/generation/ownership changes and native drift retain their fence.

## Validation

Sixteen controlled and real-API scenarios cover original creates, retained-task
Unknown, unsubmitted cancellation, original delete/replacement and prune, missing
task, Submitting, failed/wrong task, generation/owner/source changes, foreign
operation, native drift, later submission and missing completion receipt. Real
Kubernetes deletion generation and receipt persistence are exercised. Prepared
observation contexts cannot transfer to another operation or authorize prepare/start.
The completed Create receipt is handed to the existing deletion-bound preparation.
Discovery/shared journal and feature-finalizer regressions pass separately.

The native run created generated discovery **173** with original create task
`01a14cc6-8990-774a-be29-8c4fce0d1dc5`. In a separate simulated Kubernetes API, its
acknowledged original create was reconstructed as outstanding, with deletion
generation 2 versus original generation 1 and an injected Unknown state. Draining
observed the real original task/native UUID with **zero native mutations**, retained
the original generation's Create receipt, then handed off to private cleanup.
Exactly one delete (`01a14cc6-ef88-7547-bf21-ef4ff1c8fb5d`) completed, its absence
receipt was retained and the journal settled. The discovery baseline and device
inventory identities were preserved. This is not installed manager/worker or
natural fault qualification.

## Remaining

Public manager enrollment, metadata completion/finalizer release, missing-source
and empty/unowned enrollment are still pending. Mixed/grouped and legacy records
without field masks are refused by this bridge. Paused/changed-controller policy,
installed qualification and full pinned-model execution parity remain open.

Evidence: [source and native records](discovery-deletion-drain-validation.json),
[initial local cases](discovery-deletion-drain-tests.log),
[combined API gate](discovery-deletion-drain-api.log),
[final drain/API assertions](discovery-deletion-drain-api-final.log),
[regressions](discovery-deletion-drain-regression.log),
[native lifecycle](discovery-deletion-drain-native.log),
[ND/software upgrades](discovery-deletion-drain-nd-upgrades.log),
[47 non-live SWIM functions](discovery-deletion-drain-swim.log),
[controller/direct GNOI gates](discovery-deletion-drain-gnoi.log) and
[production build](discovery-deletion-drain-build.log).
