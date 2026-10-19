# Current worker binding for PnP manager observations

The manager now follows the pure PnP preparation/cohort projection with
`ReadMetadataScopeWorker`. It reuses the existing bootstrap/controller-worker
validators to identify the sole current connector through its retained
Deployment → ReplicaSet → Pod UID chain. The source/owner binding brackets two
worker observations, rejecting replacements, overlap, unready workers, obsolete
templates, wrong controller arguments and late source/controller changes.

The returned read-only observation binds controller/config, Prepared operation,
complete existing non-Secret creator/owner/journal status, sorted canonical serial
cohort, worker identity, UID chain and current template contract hash. No raw model
content or credentials enter the digest. Sorting and copying prevent caller order
or mutable aliases from changing the returned binding. The physical reservation
digest and worker grant contracts remain unchanged.

This value is not a permission, a Lease, a retained attestation or a native
submission permit. The manager creates no fences and publishes no grant. The
current template hash proves agreement among observed worker objects, not a
separately authorized metadata protocol version or installed binary capability.

## Qualification

Eleven controlled worker scenarios cover valid binding, missing/overlapping or
unready workers, conflicting arguments, zero observation time and late
source/Deployment/ReplicaSet/Pod/controller changes. Repeated canonical scope
produces the same binding; changed creator history changes it. Caller snapshots
and serial slices remain unchanged.

The actual isolated Kubernetes API qualification now persists the source,
controller, Deployment, ReplicaSet and Pod chain and exercises this observation.
Worker readiness is simulated; no container executes and no installed actor is
qualified. Existing real-API Node/reservation/Lease/source conflict cases remain
part of that run. Manager tests now also refuse missing workers, overlapping
workers and old templates. [Results and hashes](pnp-manager-worker.json) contain
the final regression gates. Six foundation packages pass 3158 test/package
events, and selected PnP/journal/upgrade regressions pass 1046 events. The main
binary, formatting and source-fingerprint checks pass.

## Remaining serialized handoff

Public new PnP dispatch stays disabled. Reusing retained bootstrap fences still
requires protected actor/version authorization, manager-to-worker serialization
and recovery/release for successful and proven-not-dispatched outcomes. In
particular, a certified pre-HTTP refusal must leave durable handoff evidence
before the journal is cleared, so the manager can safely release its retained
fences after a restart. The current private no-dispatch path is unchanged and
has no newly created fences. An uncertain native outcome must never acquire
no-dispatch or replay authority.

Native consumer protection, missing-source/CR deletion context, manager
finalization and installed/native public lifecycle qualification remain pending.
The full pinned 93-resource NetAsCode model remains incomplete.
