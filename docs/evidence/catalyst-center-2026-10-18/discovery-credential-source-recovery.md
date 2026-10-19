# Credential discovery: attested original Create recovery

Branch `pr/johalley/catc-support`; pinned NetAsCode model **0.5.0**.
This stage enables completion of acknowledged, pre-attested v3 Creates after
ConfigMap/projection loss. It does not enable config-deletion finalization,
retained Delete/prune recovery, or native credential qualification.

## Authority and execution

Before public mutation, the worker validates the complete resolved credential
source and projected version envelopes, then records non-secret source identity
using the existing discovery scope envelope. Publication reuses the idle-controller
RV guard and ordinary config/source snapshot gate. Planning additionally requires
the loaded source to match that attestation, closing a ConfigMap change between
scope validation and apply loading. Neither race grants native I/O.

The retained operation must be the same v3 Create, config UID/name/generation,
controller UID/generation, source UID/revision and exclusive inventory owner. The
source attestation's spec/model identity and original credential reference bindings
must match. Missing or changed scope cannot be reconstructed from native metadata.

Normal execution retains the full common contract, including projected-file
checks. Acknowledged recovery uses the common metadata/descriptor validation on a
copy without projected references, original config validation and explicit frozen
operation/reference/authorized-source binding checks. This narrowly permits
observation when the original projected bytes no longer exist. It is not new
submission authority and does not weaken dispatch validation.

The existing discovery timeout observer and shared Unknown/Verifying phases
advance the original task. Native task/UUID/public-field verification uses the
retained-scope observer. Both current-source and retained-source completion reuse
one atomic creator/credential receipt writer and the existing finish gate.
Original operation/task/source/version identities remain unchanged. A lost receipt
acknowledgement recovers from the retained pair; missing applied evidence cannot
release the slot. Rejected receipt publication causes no native replay.

Recovered work always stays Waiting rather than reporting current InSync. An
unavailable or changed current source is not executed as part of recovery. Once
valid source/projection returns, ordinary attestation and planning resume; an
unchanged job becomes a no-op without relabelling its old receipt.

## Qualification

Controlled and real Kubernetes API public reconciles exercise ConfigMap deletion,
changed data, replacement UID, missing projection, projection counter lag, timeout
Unknown recovery, lost/rejected/pruned receipt writes, missing/tampered scope,
config/controller changes, revoked owner, taskless/legacy work, source return,
failed/pending tasks, native drift, foreign UUID and cleanup-family conflict. A contextual successor case
loses its projection after replacement Create acceptance and recovers without
resetting predecessor history. The normal original writer remains source-bound.

Direct and public publication races exercise the actual idle-journal guard; a
separate race changes ConfigMap contents between scope validation and planning.
Those tests require zero native I/O, no journal claim, and no substituted scope.
These are controlled native responses with fake-client or real API persistence,
not installed actors or real device credential delivery.

An initial projection-loss case exposed the common gate's intentional file stat.
Recovery now has the narrow metadata contract above while new dispatch still uses
the original gate. The public race fixture also needed its explicit test admin
client for injecting status; the production reader contract was not widened.

## Remaining boundaries

The config must still exist, be non-deleting and retain its original spec and
controller generation. The existing cleanup-family conflict guard remains ahead of recovery.
Older inputs without pre-submission scope, Prepared,
Submitting, taskless outcomes and context changes remain fenced. Delete/prune
source-loss observation, config deletion/manager finalizer release, installed
qualification, actual lab device credentials/native recovery, and wider pinned
resource-model gaps remain open. No source is fabricated and no uncertain request
is replayed. Read-only health/inventory checks do not qualify native recovery.

## Recorded gates

- Final discovery/shared-journal race and real-API regression: PASS,
  260.396 seconds, 1,614 passing test/subtest events.
- Twenty-two recovery scenarios pass with controlled native responses using
  fake-client and real Kubernetes API persistence. Contextual successor recovery
  and three scope/publication/planning guards also pass in both environments.
- Scoped SWIM/XFSU/reload regression and uncached shared contract, ND, upgrade
  coordination and maintenance checks pass.

[Source/gate record](discovery-credential-source-recovery.json),
[discovery regression](discovery-credential-source-recovery-regression.log),
[SWIM regression](discovery-credential-source-recovery-swim.log), and
[foundation regression](discovery-credential-source-recovery-foundation.log).

Complete CVK build and strict documentation validation passed. The current-source
[native read-only probe](discovery-credential-source-recovery-native-read.log)
passed with three devices and two SWIM images. That is connectivity evidence,
not native credential recovery or installed qualification. The tested source
fingerprint is unchanged and all 93 resource rows remain.

## Follow-up

Acknowledged owned Delete/prune source-loss recovery is qualified in the
[retirement follow-up](discovery-credential-retained-retirement.md). The gates
and scope above describe the original Create-recovery snapshot.
