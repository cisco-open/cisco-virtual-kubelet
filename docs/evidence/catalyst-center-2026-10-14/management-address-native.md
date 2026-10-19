# Guarded native management-address execution

A private native execution primitive now uses the existing REST transport for
the task-returning management-address PUT with the documented `newIP` payload.
It is **not enabled by the public configuration compiler**. Submission requires
an explicit admission/endpoint guard and a canonical physical serial. Native
UUID, serial, baseline address and destination collision checks run before and
after that guard. A denied, stale or uncertain submission consumes its one-use
closure; it cannot replay. Appliance-provided task URLs are ignored.

The resource key binds source selectors, native UUID, canonical physical serial,
requested address and a digest of the old address. The existing configuration
write envelope carries target UUID/serial; no new schema, transport, lease or
ownership mechanism was introduced.

The private verifier requires a retained Verifying operation with target and
task identity, a submission timestamp and no incompatible or cancelled
envelope. It binds the requested address to the retained selection, resolves
the same native UUID/serial, rejects collisions/AP targets, and requires an
independent endpoint/handoff guard. Native observations bracket that guard.
Matching Catalyst Center inventory by itself cannot complete verification or
release a physical device fence. The baseline digest is retained evidence for
the future bridge; this helper does not prove task ownership or authorize
rollback by itself.

Fixture tests pass for accepted requests, admission denial, changed UUID,
changed serial/address, a newly occupied destination, lost task receipts,
replacement during pre-submit endpoint proof, replacement during verification,
unproven new endpoint, delayed native convergence, missing guards, changed source
selection and replay refusal. Existing device/managed binding tests also pass;
final race regression 2.311s. See
[regression output](management-address-execution-regression.log).

Live guard-refusal qualification passed on all three switches: native plans
were denied before mutation, replay was refused, and unchanged UUID/serial/
address was independently observed. Test 1.80s, package 2.526s; see
[live output](management-address-guard-live.log). No management address changed.
The live test does not qualify an accepted mutation or the new API endpoint.

Public execution remains gated. The next bridge must retain source/owner/task
identity through existing managed admission, establish old/new endpoint
reachability and physical identity, coordinate the mutable CiscoDevice address
and worker rollout while retaining the fence, then independently verify and
settle through the established lifecycle. Existing `configurationManagedDevice`
requires exact controller/CiscoDevice address agreement; that rule has not been
relaxed or bypassed. Installed actors and an isolated versioned live endpoint
transition remain unqualified.
