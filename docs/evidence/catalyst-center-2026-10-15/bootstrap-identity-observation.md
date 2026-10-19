# Shared bootstrap identity observation

The shared controllerconfiguration package now exposes a read-only bootstrap
identity observation for hardware with no known conflicting cluster identity.
It uses the existing operation digest, canonical physical identity and
source/owner validator. Managed reservations retain the same source checks;
there is no new CRD, lease family, grant, protocol or RBAC permission.

## What it proves

An uncached manager reader corroborates the exact controller UID/resourceVersion,
Prepared operation, current config generation, owner and inline/ConfigMap source
provenance. The shared source checker is extracted from the existing managed
reservation path rather than reimplemented for CC. This does not attest the
provider-specific payload schema, native readiness or controller-worker actor.
Those remain separate checks.

Two unfiltered cluster-wide list rounds inspect CiscoDevice declarations and
retained Node identities, orphan Node MachineID/SystemUUID values, and other
controllers' retained single/group operations. A matching canonical serial
refuses the observation, including across namespaces or when introduced during
dependency reads. Global list failure also refuses it. Source and controller
identity are corroborated after the final lists.

The returned type contains controller/config references, generations,
operation/digest, opaque native target ID, canonical physical identity and source
UID/version. It contains no source values, credentials, Device/Node stand-ins or
execution methods. It cannot be passed to existing managed dispatch as a permit.
Namespace-scoped connector workers are not given cluster-wide list permissions;
future manager wiring must supply the global uncached reader.

This is a bounded observation of known cluster identities, not proof about
unknown aliases or hardware outside the cluster. Repeated absence is not an
atomic lock against new declarations or concurrent controllers. Serialization,
final reobservation and actor admission are still necessary before dispatch.
Existing per-CiscoDevice mutation leases remain untouched.

## Verification

Fixtures cover inline and ConfigMap sources, unrelated declarations, local and
foreign-namespace CiscoDevice conflicts, retained Node identities, orphan Node
MachineID/SystemUUID aliases, and other controllers' single/group operations.
Injected late device/Node/operation aliases, failed global list, changed source
or ConfigMap revision, changed controller, report mode, owner changes,
noncanonical serials and submitted operations are refused. List options must be
unfiltered and cluster-wide. Returned evidence excludes a private template
variable carried in the test source. Existing managed intent-binding tests also
pass. The final focused tests passed in 3.149 seconds; the adjacent test log
retains the result.

The full shared reservation suite passed in 31.648 seconds; ND and pinned model
tests passed from cache. CC configuration, inventory, PnP and SWIM regression
passed in 69.531 seconds. These cover the common-source refactor and existing
managed paths as well as the added bootstrap observation.

The native-assisted Prepared qualification passed in 5.06 seconds (package 7.154
seconds). Generated serial `CVKD3768278` received native PnP ID
`6ad11e1e0bc0186771caaf3a` and Prepared operation
`f0112f8c15263e4ae1bcc3139a5f6290`. The shared observation matched its current
source, physical identity and native target in simulated Kubernetes state.
Rebuilt claim planning revalidated the native binding; denied admission retained
the exact journal without a site-claim POST or grant. Exact unclaimed/not-contacted
fixture cleanup restored empty PnP inventory and the shared network baseline.
Private evidence is under `/private/tmp/cvk-pnp-registration-3269307884/`.

The Kubernetes portion uses a fake client. This is not live cluster-wide alias,
RBAC, admission or concurrent manager qualification. No device claim/reset/upgrade
was submitted; the transport trace allowed only fixture database registration
and exact-UUID cleanup.

## Remaining execution work

The observation is not yet wired into a production bootstrap manager lifecycle.
Controller-worker binding, a source-bound serialization/grant mechanism that
coordinates new declarations, deployed admission, cancellation/settlement,
public claim dispatch and correlated native workflow/template completion remain
pending. This addition does not make PnP claiming or full NetAsCode parity ready.


A subsequent [serial fence kernel](bootstrap-serial-fence.md) provides retained
source-bound Lease evidence. Production enrollment exclusion, actor admission,
grants, settlement and public claiming are still pending.
