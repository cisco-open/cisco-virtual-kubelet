# Bootstrap serial fence kernel

**Current recovery status:** the retained UUID below has now been removed and
the native Prepared/runtime-denial qualification passed after connectivity
returned. See [admission and requalification evidence](bootstrap-fence-admission.md).
Earlier timeout descriptions below are historical.

A manager-side helper now constructs and observes a retained serial reservation
using the existing Kubernetes Lease kind. It reuses the shared source-bound
bootstrap identity observer, canonical physical identity, operation digest and
holder naming. Existing managed mutation leases and ND/direct-device/SWIM paths
are unchanged. No new CRD, RBAC or public dispatch capability is introduced.

## Binding and retention

The Lease name hashes the canonical serial independently of controller and tenant.
Every participating manager must use one configured shared Lease namespace.
The binding records controller/config UID and generation, operation ID/digest,
opaque native target ID, canonical serial, source UID/version and owner namespace.
It also binds the exact Deployment/ReplicaSet/Pod UID chain, controller-worker
service-account username and template contract hash. It contains no manifest
values, credentials or token. The reservation carries a
retention annotation and evidence finalizer, no owner references and no expiry.

Reservation requires an explicit runtime compatibility check, which must verify
the installed manager/admission policy and configured shared namespace. No
production runtime supplies this callback yet. The helper repeats source and
global conflict observations around creation, repeats the runtime check, and
rechecks the retained Lease UID and complete binding. Existing matching evidence
is read without an update. Create races use Kubernetes AlreadyExists semantics;
foreign, expired, deleting, replaced or malformed evidence is refused. An
uncertain creation is retained; later Prepared observation can read a matching
fence but cannot replay a native operation.

The separate readback helper needs no live source, so source edits/deletion do
not erase a retained reservation. Only NotFound means absence. Invalid or
unreadable evidence returns an error; future enrollment must wait on either a
returned fence or an error. There is no release, renewal, takeover or dispatch
method. A returned Lease is not a native execution permit.

## Verification scope

Race-enabled fixtures cover simultaneous same-operation reservations, serial
collisions across namespaces/controllers, existing and raced ownership, lost
create responses, missing/replaced UIDs, removed annotations, foreign/expired
fences, late source/device conflicts and denied runtime checks. They also check
readback after source changes/deletion and rejection of malformed, duplicate,
future-protocol, deleting and empty-holder evidence. A deleting-object fixture
uses Kubernetes Delete with the finalizer retained rather than setting a server
managed deletion timestamp during Create.

The full shared foundation suite passed in 32.486 seconds; ND and pinned-model
tests passed from cache. The CC configuration/inventory/PnP/SWIM suite passed in
69.580 seconds. The final strict documentation build passed in 4.92 seconds, and the
ledger still contains exactly all 93 pinned `cc_*.tf` resource types. Ledger
coverage does not imply complete execution parity.

The tunnel recheck returned HTTP 200 for token generation, PnP and network
inventory, with zero PnP records and three managed devices. The subsequent native
Prepared test registered generated serial `CVK441A6CC0`, UUID
`6ad123500bc0186771caafae`, then timed out during native claim preparation
(62.91 seconds; package 63.787 seconds). It did not reach the new runtime-denial
assertion, so this run does not qualify that native-assisted boundary. No claim
or device action was submitted. The saved journal has no active operation and
contains the registration ownership receipt. Recovery evidence is retained under
`/private/tmp/cvk-pnp-registration-1691719004/` (directory 700, files 600).

Exact retained-fixture cleanup was attempted using that receipt, but its first
network-baseline read timed out after 20.01 seconds (package 21.031 seconds),
before any DELETE. A subsequent token probe also failed with a transport error,
although SSH PID 68198 still listened on local port 19443. Therefore UUID `6ad123500bc0186771caafae` remains pending exact
cleanup; empty PnP inventory has **not** been restored or verified. No fresh
registration or device operation should run before this retained fixture is
resolved. The cleanup helper has no create path and refuses changed ownership,
contacted/claimed state or unrelated inventory.

## Connector incarnation binding

Reservation now selects the sole live connector and reuses the managed path's
controller-worker validator. That validator was extracted behind its unchanged
public managed-reservation entry point; bootstrap calls the shared core with
controller and Pod references directly, without fabricated Device/Node identities
or an invalid managed reservation. The checks include the Deployment's controller
annotations, Recreate/one-ready-replica state, ReplicaSet parent UID, Pod readiness,
service account, contract hash and exact controller UID/generation/type arguments.

The bootstrap reader reobserves original objects after shared validation, refusing
late resourceVersion/spec/status changes. The fence records the actor incarnation
and rechecks it before/after creation and after runtime authorization. A new valid
connector under the same controller/operation cannot adopt the old fence. Readback
still preserves the original evidence after source deletion or worker replacement;
it does not require the recorded actor to remain live. Old incomplete bindings
without worker evidence are treated as malformed, never as absence or authority.
No production bootstrap fences have been created by these tests.

Fixtures cover missing/overlapping/unready workers, wrong ReplicaSet parents,
wrong command arguments and stale templates, paused controllers, late Deployment,
ReplicaSet, Pod and controller changes, and unchanged retained evidence after a
valid replacement Pod appears. Focused shared-worker/bootstrap tests passed in
4.382 seconds. The full shared suite passed in 32.789 seconds, provider
reservation tests in 3.532 seconds, and ND/model tests passed from cache.
CC configuration/inventory/PnP/SWIM regressions passed in 69.628 seconds. The
strict documentation build passed in 4.80 seconds and the exact pinned
`cc_*.tf` ledger audit still matches all 93 resource types.
These use simulated Kubernetes objects and do not attest an
installed manager or native dispatch authority.

The lab remains unreachable on this continuation: token generation timed out.
The earlier generated PnP UUID is still retained for exact cleanup; no new native
fixture or physical operation was attempted.

## Remaining integration

This helper is intentionally not wired into production claiming. Installed actor authorization, enrollment/admission exclusion, policy installation and rollout fencing,
non-reconstructible dispatch authority, cancellation/settlement/release, public
claim execution and correlated native workflow/template completion are pending.
Repeated absence checks alone do not atomically exclude new declarations or
unknown hardware aliases. Positive reservation tests use simulated Kubernetes
state and do not qualify installed admission/RBAC or multiple real managers.
Full PnP claiming and full NetAsCode parity remain incomplete.


The reservation helper now also requires [live protection inspection and
installation binding](bootstrap-protection-runtime-binding.md). Its remaining
runtime callback cannot substitute for that direct policy/binding check.
