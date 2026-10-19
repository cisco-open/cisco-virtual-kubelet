# SDA transit execution stage

## Scope and pinned mapping

CVK's ordinary transit executor still supports only IP-based global definitions.
This phase adds a separate physical SDA creation stage, without enabling an
unreserved metadata path. Both `SDA_LISP_PUB_SUB_TRANSIT` and
`SDA_LISP_BGP_TRANSIT` resolve `control_plane_devices` names to native inventory
IDs. The multicast option is projected only for PUB_SUB, matching the pinned
module, including when a BGP declaration contains that optional boolean.

The [module resource](https://github.com/netascode/terraform-catalystcenter-nac-catalystcenter/blob/a435555fe828f03cf3311d52fe1ab6572a247916/cc_fabric.tf)
and [provisioning dependency](https://github.com/netascode/terraform-catalystcenter-nac-catalystcenter/blob/a435555fe828f03cf3311d52fe1ab6572a247916/cc_device_provision.tf)
require provisioned control-plane devices. The pinned
[provider mapping](https://github.com/CiscoDevNet/terraform-provider-catalystcenter/blob/01058637dd5ed4fca93e1b2a96dd7032aec2d3a1/internal/provider/model_catalystcenter_transit_network.go)
sends their IDs as a set under `sdaTransitSettings`. IP name/type/protocol/ASN
replacement behavior is unchanged. This stage requires at least one CP, rejects
per-site SDA creation and extraneous IP settings, and does not yet implement
inventory-model aliases or mixed IP/SDA source composition.

## Execution and identity

The native observer reuses the shared inventory selector, canonical physical
identity rules, wired provisioned-record reader, assigned-site reader and site
inventory. Reachability, supported wired family, exact provisioning/site binding,
all CP IDs and unrelated transit dependencies enter the request fingerprint.
CPs already used by another SDA transit and incomplete peer CP membership
refuse before any request, preserving the incoming-only physical footprint.
Same-name conflicting objects refuse; matching readback grants no creator or
Delete authority. Existing transit updates are not performed.

Submission freezes the intent and payload, checks the native snapshot before
and after complete-footprint authorization, and allows only one attempt per
planned write. The existing authenticated transport, limiter and task POST are
used. Lost or malformed acknowledgement never permits replay. The managed
wrapper uses `observeConfigurationManagedTargets`, immutable CiscoDevice/Node
bindings and canonical group targets, with fresh native/managed checks around
authorization. No new transport, store, journal or schema is introduced.

Task observation requires the original task ID, submission time, successful
root and complete rooted tree, matching request fingerprint and stable repeated
native readback. It is deliberately **not** creator ownership, device convergence,
a final completion receipt or reservation release. The entire
`sda-transit-create:` family is reserved even when malformed or group fields are
pruned. Public action validation stays disabled, preventing legacy metadata
submission/settlement from bypassing physical admission.

## Verification

- [Regression](sda-transit-execution-regression.log): 48 SDA subcases plus existing
  IP transit, combined fabric preflight, managed footprint and group recovery
  cases pass with race detection in 5.260 seconds. Coverage includes both SDA
  types, canonical sets, pinned multicast projection, full guard footprint, occupied/incomplete peer refusal,
  source freezing, denied/late native or managed changes, no replay after loss,
  complete child task semantics, CP identity replacement and all seven reserved
  journal phases with absent physical group metadata.
- [Real Kubernetes API](sda-transit-execution-api.log): device/Node UIDs and status
  persistence, late Node replacement refusal, immutable rebind rejection,
  explicit fixture re-enrolment and global cross-namespace alias rejection pass
  in 9.299 seconds. The native POST is an HTTP fixture. No real reservation,
  installed worker or device convergence is exercised.
- [Immutable-identity diagnostic](sda-transit-execution-immutable-diagnostic.log):
  the initial test incorrectly attempted to rebind an existing Node identity.
  The real schema rejected it. The final test asserts that rejection and
  explicitly recreates only its fixture CiscoDevice. No production schema or
  identity rule was relaxed; CVK performs no automatic repair/re-enrolment here.
- [Lab prerequisite readback](sda-transit-execution-native-prerequisites.log):
  authenticated checks for `.100`, `.101` and `.103` find no wired-provisioned
  records, with successful reads. Zero product mutations are counted. This is
  evidence of the current prerequisite gap, **not** native SDA execution.
- [Foundations](sda-transit-execution-foundations.log): shared controller, ND,
  configuration group, software lifecycle, upgrade provider and direct GNOI
  checks pass; some unchanged packages use cached results.
- [Source and upstream fingerprints](sda-transit-execution-sources.json) and
  [strict documentation build](sda-transit-execution-docs.log).

## Remaining work

Public mixed-model compilation and provisioning sequencing, inventory alias/FQDN
projection, source/owner-bound journal preparation, actual all-member permit
submission and acknowledged recovery routing remain to be wired. Independent
CP/device convergence and final receipt/release must precede public activation.
Updates need complete old/new CP and connected consumer footprints; ownership,
adoption/replacement/delete and installed-actor qualification remain open.
The lab must first have suitable provisioned CPs; the earlier IP transit native
create also exposed a missing ConnectivityDomain application. This read-only run
did not re-test or repair that application. Full pinned model parity is incomplete.
