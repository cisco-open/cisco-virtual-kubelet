# Fabric authentication overrides: model, guarded execution and native readiness

The pinned NetAsCode 0.5.0 `cc_authentication_templates.tf` declares global,
Low Impact fabric, Open Authentication fabric and Closed Authentication fabric
instances of `catalystcenter_update_authentication_profile`. Fabric overrides
use `fabric.fabric_sites[].authentication_template`; Closed Authentication also
supports `bpdu_guard`. This change adds their resolved model and read-only
reporting, plus private guarded preparation and recovery. Public execution is
still pending physical admission/reservation integration.

This records the initial staged implementation. The subsequent
[public group lifecycle](fabric-authentication-public.md) implements reservation,
permit dispatch, receipt and settlement integration; its native qualification
limits still apply.

## Shared structure

The compiler projects site overrides before the existing full fabric parser;
unrecognized sibling and zone fields remain errors. Name-only site declarations
retain their existing behavior. Override values must be resolved and explicit,
as for global authentication templates. BPDU false is preserved as an explicit
boolean; BPDU input for other profiles and incomplete/unknown models are refused.

The existing paginated authentication reader now accepts an explicit fabric
scope. It requests `fabricId` and `isGlobalAuthenticationProfile=false`, then
requires exact fabric identity, unique valid profile identities/names and complete
modeled fields. Global queries retain their existing scope. These query and
update semantics follow Cisco's
[2.3.7.9 GET contract](https://developer.cisco.com/docs/catalyst-center/2-3-7-9/get-authentication-profiles/)
and [PUT contract](https://developer.cisco.com/docs/catalyst-center/2-3-7-9/update-authentication-profile/).

Fabric writes reuse `authConfigurationExecutor` for payloads, task responses,
one-use dispatch, frozen intent and repeated readback. No separate API client,
retry/task loop, journal schema or provider foundation was introduced. A native
Voice VLAN value is preserved when present without exposing a new NaC input
field or inventing an absent default. Native ACL descriptions remain preserved;
omitted fabric BPDU preserves its observed value, while explicit BPDU is managed.
Global updates continue omitting fabric scope and BPDU.

The private preparation primitive requires a caller-supplied authority check,
an existing fabric and the selected built-in native profile. Before dispatch it
reobserves the full selected hierarchy/fabric/profile set through the existing
core guard. This refuses native identity/value/description/Voice VLAN/BPDU
changes and authority loss, including changes in a later selection. All accepted,
denied and uncertain attempts consume the shared plan.

The existing journal target stores the native profile UUID and parent stores the
fabric UUID. A versioned resource key binds logical site/profile selection and
the original hierarchy UUID. Serialized verification selects only the accepted
pair and rechecks these bindings twice. A same-name hierarchy replacement cannot
satisfy the original operation; a missing later selection does not block its
readback. This private verifier does not perform settlement or release a lease.
The entire `fabric-auth:` resource family is reserved independently of group
metadata. Missing/pruned group fields or malformed/future resource versions
cannot fall through to metadata completion or legacy single-device helpers.
Public group action validation remains disabled for this staged family.

## Public boundary and fixtures

The compiler wraps the complete fabric composition in an admission gate whenever
overrides are present. It cannot create an earlier site, zone, VN or other
foundation resource before refusing an unadmitted override. The report path
compares two observations and emits an Add for an absent fabric or an Update
for changed selected fields.

HTTP/race fixtures cover all three profiles, explicit false BPDU, native
description/Voice VLAN preservation, inherited BPDU retention, stable no-op,
serialized recovery, same-name hierarchy replacement, native fabric/profile
replacement, description/Voice/BPDU changes, lost acknowledgement, mutable caller
intent, missing authority/fabric, later selection changes, replacement during
observation and late recovery changes. Reader tests refuse foreign/global scope,
duplicates, partial responses, missing UUID/required fields and misplaced BPDU.
Malformed/future reserved journals are retained across Prepared, Submitting,
Accepted, Verifying, Verified and Unknown phases without dispatch or completion.
These use simulated authority and native HTTP responses. They do not qualify
installed worker coordination or real fabric configuration.

The broader fabric, authentication, configuration/admission and SWIM race suite
passed in **200.210 seconds**. The independent Nexus Dashboard suite passed in
**4.970 seconds**. The final focused suite passed in **3.852 seconds**, covering the subsequent
reserved-family guard and report example. Strict documentation compilation and the exact
93-type pinned resource ledger audit pass.

## Native readiness

The lab returned three global templates and zero fabric sites. A read-only run
used an existing native hierarchy area with a resolved Closed Authentication
override. The complete report produced two additions: the missing fabric and its
override. Public execution refused device admission; private planning refused
missing authority and, separately, the absent native fabric. All checks completed
with **zero writes** and the empty fabric baseline unchanged. The global native
round-trip/no-op regression also passed. See
`fabric-authentication-native-read.log`.
The read-only tests completed in **5.193 seconds** for the package; the fabric
readiness test itself took **1.64 seconds**.

No fabric was created to manufacture positive evidence. Accepted native override
updates, BPDU behavior on devices and installed actors remain unqualified. The
example is intentionally Report mode:
`examples/configs/catalyst-center/fabric-authentication-report.yaml`.

## Remaining implementation

- Bind these operations into existing physical group preparation, all-member
  admission, durable reservation claims and full task/readback recovery.
- Integrate dependency receipts, source/owner validation and verified settlement
  with the public worker/manager lifecycle; preserve fences for uncertain tasks.
- Qualify accepted native updates on a real fabric, restart/recovery, relevant
  device behavior and cross-section ordering with global templates.
- Implement ownership-aware reset/removal and broader default/field combinations.

The earlier unexplained global raw-response discrepancy remains documented in
[global template evidence](global-authentication-templates.md). This addition
does not resolve that issue or establish complete authentication-resource parity.
