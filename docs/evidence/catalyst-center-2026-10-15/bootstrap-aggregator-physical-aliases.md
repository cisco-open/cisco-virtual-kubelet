# Aggregator physical alias retirement fence

## Implemented behavior

Aggregator startup and registration now consider the canonical physical serials
captured by every active or unresolved retiring worker in this manager. Changing
Device name, namespace or incarnation cannot evade a worker for the same known
physical serial. Both spec-declared and retained Node physical identities are
captured; later caller/source pruning does not erase the original worker scope.
Canonical case aliases collide. Unrelated serials remain independent.

The source projection is shared with the existing enrollment guard through
`DevicePhysicalIdentities`, which reuses CVK's canonical identity validator,
deduplicates the spec/retained sources and returns a stable sorted set. It does
not discover hardware or convert an IP address into identity. Invalid nonempty
identity is refused; absent identities retain the existing legacy behavior and
are not proof of disjoint hardware.

The check before driver construction and the check during worker registration
share the same mutex-protected comparison. Registration allows only one active
worker per known footprint. Completed successful retirements can be pruned;
pending/missing completion and failed Close remain held across Device keys.
This uses the existing active/retiring maps with a linear scan on startup, not
another independently maintained identity index or database. Both IOS-XE and
NX-OS constructors use the shared lifecycle registration.

A candidate losing registration can still own a newly opened transport. Its
footprint is retained under the registration mutex before releasing the lock and
starting cleanup. Slow or failed candidate Close therefore cannot disappear
when the original owner finishes. The candidate loop never starts; its cleanup
failure is returned with the registration error and its record remains held.

## Verification

Focused race-enabled aggregator, shared and controller tests passed in 3.117,
5.318 and 3.039 seconds. Tests cover active and retiring aliases in different
namespaces/names, case aliases, both declared and retained identities, captured
scope after source pruning, independent unrelated serials, failed/missing cleanup
and a rejected candidate whose Close remains pending or fails after the original
owner exits. Eight concurrent alias registrations admit exactly one worker.

Full race-enabled regression passed: aggregator 3.244 seconds, controller
33.566, manager 11.023, shared configuration 33.828, provider reservation 3.621,
and CC configuration/inventory/PnP/SWIM 69.514. Provider, ND and pinned model
tests passed from cache. Existing worker cancellation/cleanup/rebind and
bootstrap identity/source/guard fixtures remain covered. All tests use simulated Device/transport state; no live
aggregator deployment, PnP fixture or physical action was submitted in this stage.
No CRD, native API, transport protocol, device lease family, worker RBAC or public
execution capability was changed.

## Remaining qualification

This is local-manager exclusion for known declared/retained serials. It does not
prove identity for missing or incorrect declarations, unknown legacy hardware,
address-only aliases, other manager processes or other mutation consumers.
Records are not durable across restart. A closed loop/transport is not native
transaction completion, and a restarted process is not quiescence evidence.

The production bootstrap authorizer still needs installed manager/actor/RBAC
rollout verification, durable unknown-outcome/quiescence evidence, participation
of other direct-worker consumers, one-use dispatch, cancellation/settlement/
release and correlated native claim/template completion. Public claiming and
full pinned NetAsCode execution parity remain incomplete. Native hardware
qualification cannot be inferred from these local concurrency tests.
