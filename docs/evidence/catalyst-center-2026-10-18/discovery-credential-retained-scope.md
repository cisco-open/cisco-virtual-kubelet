# Credential discovery: retained pre-submission public scope

Branch `pr/johalley/catc-support`; NetAsCode model pin **0.5.0**.
This stage is a prerequisite for safe config-deletion/source-loss recovery. It
adds retained expected public fields and a read-only observation candidate. It
**does not enable public source-loss completion, config deletion, finalizer
release, or native credential delivery**.

## Operation and receipt contract

New credential-bearing Create inputs use an internal v3 identity: the existing
semantic name/public/version-label hashes, a closed public-field mask and canonical
expected native fingerprint, plus the existing predecessor context (or an explicit
initial marker). The public NetAsCode model and shared projected Secret envelope
are unchanged. Neither secret bytes nor hashes of secret bytes enter this proof.
The canonicalizer and field vocabulary are the existing discovery helpers.

The proof is retained in the shared write-ahead operation before the sole native
submission permit. It therefore exists before a successful creator receipt.
Its canonical field mask/digest must match the independent v2 creator receipt
when completion atomically publishes the creator/version pair. A legacy receipt
cannot silently discard a scoped operation's expected fields. The replacement
capacity guard budgets the largest scoped successor before deleting its parent.

The existing v1 semantic input remains the comparison basis for queued intent.
Contextual successors retain the same predecessor retirement seal. Unchanged
completed legacy inputs retain their original resource key and do not replay
credentials merely to acquire a new format. Acknowledged legacy input operations
can still complete using their unchanged original source. Legacy idle public-proof
promotion continues without querying an expired original task. Missing source does
not manufacture the new proof for an older operation.

## Read-only recovery prerequisite

The private observer requires the exact current Verifying operation, exclusive
inventory owner, current controller identity/generation, a successful correlated
original task, the exact returned UUID, matching retained canonical public fields,
complete/inactive job state, repeated native reads and an unchanged journal/RV.
It reads neither the config/model nor projected credentials. It returns an
observation candidate, never a status receipt or submission/replay capability.

Controlled and real Kubernetes API cases delete the config and projected files
before observing the original task. Drift, foreign UUIDs, failed/pending tasks,
active jobs, journal races, revoked ownership and unknown scope versions remain
fenced. These cases also assert that ordinary credential publication stays denied
after source loss and that the observer writes neither native nor Kubernetes state.
Identity tests cover malformed masks/digests/context, canonical native equivalents,
legacy unchanged-input compatibility, and matching versus mismatched pre/post proof.

The previous synthetic pair test now uses matching scoped input and public-effect
receipts; its refusal cases still cover incomplete, edited, foreign and downgraded
proof. Legacy fixtures explicitly carry legacy input identities rather than
stripping a new scoped creator's public proof.

## Still required

The existing manager/worker finalization flow must bind a validated retained
credential source attestation before mutation and admit its exact scope for
deletion. Only then can its drain consume this observation and atomically publish
completion, clean up owned jobs and release the manager finalizer. Config/controller
changes and uncertain submissions must preserve their existing fences. No accepted
operation is upgraded in place and no missing scope is inferred from a job name.

Actual device CLI/SNMP credentials, native credential delivery/recovery, installed
actors, finalizer release, wider model gaps and full 93-resource parity remain
unqualified. Read-only lab connectivity checks do not prove those mutations.

## Recorded gates

- Final discovery/shared-journal race and real-API regression: PASS,
  215.927 seconds, 1,560 passing test/subtest events.
- Ten retained-observation scenarios pass with controlled native responses in
  both fake-client and real Kubernetes API persistence.
- Scoped SWIM/XFSU/reload regression: PASS, 2.873 seconds.
- Uncached shared contracts, Nexus Dashboard, upgrade coordination and
  maintenance regression: PASS.

[Source/gate record](discovery-credential-retained-scope.json),
[discovery regression](discovery-credential-retained-scope-regression.log),
[SWIM regression](discovery-credential-retained-scope-swim.log), and
[foundation regression](discovery-credential-retained-scope-foundation.log).

The complete CVK build and strict documentation validation passed. The
[current-source native read-only check](discovery-credential-retained-scope-native-read.log)
passed with three devices and two SWIM images. This establishes connectivity only;
public source-loss completion, config-deletion finalization, native credential
recovery and installed actor qualification remain disabled or unqualified.
The tested source fingerprint is unchanged and all 93 resource rows remain.

## Follow-up

[Attested original Create recovery](discovery-credential-source-recovery.md) now
consumes this prerequisite for acknowledged, unchanged-config operations after
ConfigMap/projection loss. The results above remain scoped to this earlier source
and do not qualify that later writer, config-deletion finalization or native
credential recovery.
