# Atomic discovery credential completion

Follow-up: the subsequent [public Retain-only execution stage](discovery-credential-public-execution.md)
now connects guarded Create and original-task recovery. This record retains its
original private-stage source and qualification boundary.

The private credential-bearing discovery path now has a task-bound completion
writer using CVK's existing controller status journal. This closes the private
atomic-publication gap from the [input/verifier stage](discovery-credential-contract.md).
Public credential-bearing discovery submission remains disabled; no live inline
credential mutation or installed rollout is claimed.

## Persisted result and shared journal integration

After the original successful task is correlated with a complete native job and
its exact UUID/public metadata, one status CAS retains all three:

- `CredentialApplications`: the observed job UUID and exact projected credential
  version/source bindings for the original operation.
- `ActionApplications`: a discovery creator receipt containing that same
  operation, UUID, owner and verification time.
- The unchanged operation advanced to `Verified`.

The existing action-receipt publisher persists the update and reads it back.
The discovery writer additionally checks the paired credential receipt, catching
schemas that retain the creator receipt while pruning credential evidence.
No new status type, CRD, Secret API reader, transport or journal is introduced.
The private receipt does not grant ordinary discovery deletion or replacement
authority; those paths still require their own native-effect and lifecycle proof.

Shared journal preparation checks both receipt capacities and creator conflicts.
The existing submission-permit transition repeats that check, so a changed
capacity/creator state between preparation and dispatch cannot issue a permit.
The shared generic Verified transition and generic credential-only completion
cannot complete this family. Journal release requires exactly one matching
creator/version pair and its exclusive current section owner. Unknown input
versions also remain fenced instead of falling back to generic completion.

Same-name existing creators, including ordinary discovery receipts, cannot be
adopted or replaced by this Create. The input fingerprint is checked against
canonical version/source bindings and one source job's known credential leaves.
Secret material is absent from both receipts and all status fingerprints.

## Failure and recovery checks

Controlled publication scenarios cover success, lost acknowledgement, status
conflict, pruned credential receipt, pruned creator receipt, pruned operation or
creator credential bindings, wrong result UUID, failed/running task and unrelated
native job. The successful cases use the existing plan completion callback and
shared prepare/start/Accepted/Verifying/finish transitions.

A simulated lost acknowledgement leaves the pair durably present. A reconstructed
worker/projection recognizes it without another native read, mutation or status
rewrite, then releases the operation slot while preserving both receipts. A
conflict leaves the original Verifying operation with no partial receipt. Missing
or edited evidence retains the operation fence. This is completion recovery;
public submission and taskless/Unknown/source-loss recovery remain separate work.

Eleven preparation cases reject exhausted/oversized receipt storage, same-name
creators, existing version evidence, missing/changed bindings, invalid actions and
malformed/unknown input versions without changing journal ownership. Three
pre-permit cases repeat capacity, foreign-creator and pruned-binding refusal.
Twenty pair-validation cases reject missing/edited/duplicate receipts, old/future
times, unknown/cross-job credential paths, counter edits and missing/foreign/
overlapping/duplicate ownership.

Seven API-server publication scenarios exercise real CRD persistence and shared
plan completion: success, lost acknowledgement, conflict and four credential/
creator/schema-pruning cases. Native task/readback responses are controlled in
these tests. They do not prove that a lab appliance has applied inline credentials.

## Remaining work

The full pinned model remains incomplete. This family still needs its public
compiler/executor and guarded one-use submission connected to the shared loop,
restart/Unknown/taskless and source-loss handling, no-op recognition, owned
replacement, credential rotation/removal history, deletion/pruning, composition
and live qualification with authorized device credentials. Metadata-only
NoAuthNoPriv input remains parseable but is not granted a credential receipt with
an empty version set. Other discovery and NetAsCode parity gaps remain recorded
in the [93-resource ledger](../../controllers/catalyst-center-resource-parity.md).

See the [validation record](discovery-credential-atomic-completion.json) for exact
source hashes and qualification boundaries. The prior complete optional-null
package audit predates both private credential stages and is not relabeled as a
full-suite pass for this source.
