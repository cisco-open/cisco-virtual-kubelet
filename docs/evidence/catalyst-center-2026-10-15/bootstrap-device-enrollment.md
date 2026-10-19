# Device enrollment guard for retained bootstrap evidence

## Integrated behavior

The manager now supplies `BootstrapFenceNamespace` to CiscoDevice reconciliation:
`CONFIG_LEASE_NAMESPACE`, or the manager Pod/release namespace when empty. It is
never derived from a device's tenant namespace. This reuses the existing chart
installation inputs; no new manifest field, flag, CRD or RBAC is added. Existing
manager Lease read permissions are sufficient.

For managed enrollment, or a retained managed Node identity during retirement,
reconciliation checks bootstrap evidence before finalizer registration, teardown,
Node projection, generated access and worker/config writes. It examines both the
spec-declared and retained Node physical identity, using the existing canonical
serial/name function. Uncached reads corroborate the current Device UID,
generation, spec, retained Node binding and deletion state, and recheck the
Device after the Lease lookup. Changed/deleted declarations or unavailable reads
hold reconciliation and requeue without proceeding to mutation.

The shared `CheckBootstrapEnrollment` helper requires absence at the reserved
serial key. Any Lease there holds enrollment, including malformed, foreign,
expired or deleting evidence. It does not parse an invalid binding into absence
or take over/remove a reservation. Its raw lookup is shared with the existing
strict bootstrap-fence reader; claim/readback validation still checks the full
binding independently. A different serial or tenant-only Lease does not block
the configured shared serial scope.

An empty manager bootstrap namespace keeps older/local configurations unchanged;
nonmanaged legacy devices without retained Node identity keep their existing
path. Therefore a future bootstrap runtime must verify the configured guard and
compatible manager rollout before granting new work. Neither compatibility
exception authorizes claiming.

## Scope and safety limits

This is the CiscoDevice manager reconciliation guard, not Kubernetes admission
for new Device objects or a distributed atomic lock. The bootstrap dispatcher
must still perform its final global identity check. Existing bootstrap identity
tests refuse new matching Device declarations; the reverse guard now prevents
that manager path from starting workers while retained evidence exists. Read
absence by itself is not dispatch authority or proof of unknown hardware aliases.

The guard does not stop already-running workers or cover independent aggregator
execution. Unknown/unmanaged legacy aliases, aggregator participation, installed
manager rollout/actor authority and other mutation consumers still require
containment in the production bootstrap authorizer. Public claiming remains
gated. There is still no grant, cancellation/settlement/release or native claim
completion path. This addition must not be used to bypass those requirements.

## Verification

Focused controller tests passed in 3.113 seconds. They cover absent, held,
expired, foreign and deleting evidence; other serials/namespaces; retained
identity after spec pruning; missing/failed uncached reads; stale/late Device
changes; and compatibility paths. Full Reconcile tests show refusal before
finalizer/deletion/projection/worker writes, including a deleting Device.

The full controller suite passed in 34.119 seconds and the full manager suite in
10.616 seconds. These include existing managed topology, handoff, gNOI and SWIM
controller tests. The shared configuration suite passed in 33.957 seconds;
provider reservation, ND and pinned model tests passed from cache. CC
configuration/inventory/PnP/SWIM tests passed in 69.447 seconds. The tests are
race-enabled; no existing provider or upgrade protocol was replaced.

The isolated Kubernetes 1.35 API-server qualification passed in 4.63 seconds
(package 6.610 seconds). It creates a protected Lease through the actual
reservation helper and real manager admission, verifies that enrollment for
its canonical serial is held, and verifies another serial remains clear.
Controller/source/worker state and remaining runtime compatibility are simulated.
As before, envtest lacks the background compiler controller: unobserved policy
status is rejected, then only that controller's status publication is simulated
explicitly. This is not an installed lab-manager qualification.

A read-only lab probe returned HTTP 200 for token generation, PnP and network
inventory, with zero PnP records and three network devices. No new native test
fixture or physical action was submitted in this stage. Lab cluster repair and
bootstrap policy installation were not performed. Full installed execution and
full NetAsCode parity remain incomplete.


The subsequent [aggregator write guard](bootstrap-aggregator-guard.md) now checks
known physical serials before aggregator startup and each engine write/cleanup
transaction. Drained-operation proof, unknown legacy aliases, other direct-worker
consumers, installed authority and public bootstrap dispatch remain pending.
