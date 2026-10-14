# SD-WAN fixture qualification — 14 October 2026

Target: supplied lab Manager, HTTPS, version **20.18.2.2**. Read-only discovery
reported six inventory entries across `vmanage`, `vsmart`, `vbond` and `vedge`.
No device configuration, template attachment or policy activation was performed.
The endpoint credentials and session material are excluded from this record.

Baseline: NetAsCode v1.5.0 (`5fbf037ea57368c7e6e77694c81330b6f7b16666`),
CiscoDevNet/sdwan v0.11.5 (`d3194b612c3f24421ec1727b6fa6e9c0bc98b07f`).
These upstream projects are source references for the native CVK port.
Qualification below records native CVK tests; Terraform is not a repository test
or deployment dependency.

| Check | Observed result |
|---|---|
| Native authentication and inventory | Passed; Manager 20.18.2.2, six entries |
| Upstream classic banner versus native mapping | Passed, including line endings and default device types |
| Native classic banner lifecycle | Create, read-back, repeated observation, update, verification and cleanup passed |
| Native system profile + banner parcel | Create in dependency order, read-back and reverse cleanup passed |
| Native unattached configuration group | Profile reference resolution, creation, read-back and cleanup passed |
| Real Kubernetes reconciliation with simulated Manager | Passed using API server/etcd 1.35.0; Lease and status persistence and repeat reconciliation |
| Real Kubernetes reconciliation with live classic banner | Passed; verified apply and repeat reconciliation, followed by cleanup |
| Real Kubernetes reconciliation with live profile/parcel/group chain | Passed; all three resources converged, including both section Leases, followed by receipt-bound reverse cleanup |
| Go regression suite | `go test ./...` passed after native group integration |
| Focused race tests | Passed for adapter foundation, SD-WAN, cisco-vk command and default resolver; final adapter checks include the journal-settlement change |
| Helm | Lint and template rendering passed |

Native fixture coverage now includes 128 resource declarations
and the checked fixture attributes. The full 226-resource module surface is not qualified.
All observed fixture cleanup calls succeeded.
The real-API-server tests exercise reconcilers directly; they do not claim a
container-image rollout or Helm installation into the user's existing cluster.
No asynchronous device deployment or recovery is claimed.

## Expanded fixture run

Additional live evidence:

- All 24 classic policy-object collections: native create/read/update/read/delete.
- Seven hierarchy nodes: three groups, three regions, one unassigned site;
  dependency-ordered creation, readback and reverse cleanup.
- Eight additional feature-profile containers and their implicit application-priority
  settings parcel: native create/read, changed-value update/read, cleanup.
- Full Go regression suite and focused race checks passed after these mappings.
- Delayed accepted-create readback has a regression test preventing duplicate POSTs.

The first policy fixture run found two Manager behaviors: long names are rewritten,
while some type labels change capitalization. Short fixture names and explicit
case normalization resolved these mismatches. Region lists require hierarchy
regions; the fixture now supplies those prerequisites. An absent tertiary color
preference was rejected by this Manager; the working fixture supplies all three
preferences. The mapping includes the application-priority settings
parcel that is created even when settings are omitted from YAML.

The combined 44-resource Kubernetes/live reconciliation test passed in 730.69s,
including idempotent convergence and receipt-based reverse cleanup. No device attachment, policy activation or router-level behavior
is claimed. Untested upstream fields and resources remain visible in the coverage
inventory instead of being counted as complete.

## System/CLI feature extension

A separate native fixture set creates 12 objects: two isolated profiles plus ten
feature parcels. Native lifecycle passed for
CLI config, system basic, BFD, global, OMP, NTP, logging, MRF, flexible port speed
and performance monitoring. The cycle cleaned up its created resources.
Basic/global/OMP/NTP/logging definitions are reproducibly generated from pinned
upstream mapping expressions, API value schemas and provider defaults. Unsupported
mapping expressions or validation keywords fail generation. Nested-list and union
schema validation have regression coverage; schema bounds rejected invalid fixture
values before any controller write.

The separate 12-object Kubernetes/live reconciliation passed in 108.06s,
including convergence and receipt-bound cleanup.

## Security, GPS, UCSE and ThousandEyes extension

- Expanded system batch: 13 objects. Security native create/read/update/read/delete
  passed. Live Kubernetes reconciliation passed in
  122.64s, including idempotence and receipt-bound cleanup.
- GPS/UCSE batch: four objects. Native lifecycle
  passed. Live Kubernetes reconciliation passed in 23.21s with cleanup.
- ThousandEyes batch: two unattached objects using a token variable. Native
  lifecycle passed; no external agent was started.
  Live Kubernetes reconciliation passed in 13.07s with receipt-bound cleanup.
- Generator now preserves required nested fields, NaC scalar defaults, JSON-schema
  choice values, IPv4 formats, and fixed virtual-application wrappers. Regression
  tests check nested names, missing fields, variable mapping and invalid values.
- TrustSec failed live validation: required `data.sxpNodeId` is absent from the
  pinned provider; its empty default device password is also rejected. The failed
  attempt's parent profile was removed. TrustSec is recorded as compatibility-blocked
  and rejected by the compiler before deployment.

Full Go regression and focused race suites passed after this extension. A final
read-only sweep confirmed all new fixture names absent. Generated metadata was
regenerated byte-for-byte from the pinned sources.

## SNMP, AAA and policy-profile definitions

SNMP native lifecycle and live Kubernetes
reconciliation passed (13.28s for Kubernetes), with cleanup. The fixture includes
nested OIDs, community variables, security groups, user credential variables, and
a trap target. No fixture was attached to a device.

AAA native lifecycle passed for local user
variables, computed RADIUS/TACACS group names, nested servers, accounting rules,
and authorization rules. The fixture remains unattached.

The singular policy-profile fixture and fifteen policy parcels passed compiler
checks for all 16 objects. Native profile creation was rejected:
this Manager already has its single permitted policy-object profile. No profile
or parcel was created by that attempt. These sixteen declarations remain
unqualified, pending a controlled existing-parent workflow or an empty Manager.

NaC type references reviewed for numeric API encoding:
[TLOC preference](https://netascode.cisco.com/docs/data_models/sdwan/policy_objects/tloc_list/)
and [class-map queue](https://netascode.cisco.com/docs/data_models/sdwan/policy_objects/class_map/).
The latter documentation names `class_maps`; the pinned module instead consumes
`forwarding_classes`. This discrepancy is recorded rather than silently renamed.

AAA live Kubernetes reconciliation passed in 13.14s with idempotent convergence
and receipt-bound cleanup.

Full regression and focused race suites passed for this checkpoint. The final
read-only sweep confirmed SNMP, AAA and isolated policy-profile fixture names absent.

## Classic feature-template extension

The CLI/global/multicast fixture batch passed native lifecycle and Kubernetes/live reconciliation (15.22s) with reverse cleanup.
The cellular controller/profile batch passed the same checks (12.43s for
Kubernetes). All five templates were unattached.

Classic metadata derives API paths, input constraints, unset-value behavior,
Boolean string encoding and implicit provider defaults from pinned sources.
The cellular controller retains the provider's ignored data-profile tree and
firmware auto-SIM default even though the NaC module does not expose those fields.
Tests cover resolver stability and strict rejection of unknown template collections.

Full Go regression and focused race suites passed after the classic extension.
Final read-only checks confirmed all five classic fixture names absent.

## Classic trees, lists and application wrappers

Eight additional resource declarations passed native create/read/update/read/delete
and isolated Kubernetes reconciliation with cleanup:

| Fixture | Kubernetes duration |
|---|---:|
| Classic BFD | 8.75s |
| Classic IGMP | 8.82s |
| Classic NTP | 8.31s |
| Classic OMP | 8.40s |
| Classic PIM | 8.45s |
| Classic applications (two templates) | 11.64s |
| Classic DHCP | 8.50s |

All templates remain unattached. Tests cover nested row identities, optional flags,
NTP credential variables, OMP's conditional OSPF advertisement and disjoint hold-time
range, fixed application wrappers, DHCP ranges and static leases. No application
was started and no DHCP service was enabled on a router.

PIM mapping validation found a missing empty `send-rp-discovery` object. The
metadata generator now preserves these provider defaults. PIM RP-address `optional`
is not serialized by the pinned provider and is explicitly unsupported. Temporary
diagnostic instrumentation was removed after resolving the mismatch.

Full Go regression and focused race suites passed. Classic metadata regenerated
byte-for-byte, and final read-only checks confirmed all new fixture names absent.

## Service DHCP and trackers

The six-object service fixture covers a service profile, DHCP server, endpoint
tracker, and interface/route/SIG object trackers. Expanded native lifecycle passed
in 18.82s. Live Kubernetes reconciliation,
idempotence and receipt-bound cleanup passed in 39.73s after correcting the test
harness's managed-section selection. The failed harness run made no mutations.

Generated API-data schemas enforce required properties, alternatives, conditions,
and unique array members before deployment. Tests reject invalid DHCP option
combinations, incomplete static leases, and missing type-dependent tracker fields.
NaC object-tracker types retain their lowercase public spelling and map explicitly
to the provider API's Interface/Route/SIG values.

## Transport cellular features

The four-object fixture covers a transport profile, cellular controller, and two
cellular profiles (authentication off/on). Native lifecycle passed in 12.71s
and live Kubernetes reconciliation with cleanup in
25.57s. Credentials are variables and all profiles remain unattached.

The metadata preserves the provider's non-eSIM discriminator, authentication
branches, and slice-differentiator dependency. Full API-data schemas reject invalid
profile-ID/no-overwrite/slicing combinations. Slicing follows the provider's
20.18.1-or-newer behavior; runtime Manager qualification remains restricted to the
existing qualified version range.

Full Go regression and focused race suites passed. Parcel metadata regenerated
byte-for-byte from the pinned upstream revisions.

## Tracker groups and classic IPsec/SNMP/logging

| Fixture | Native lifecycle | Live Kubernetes |
|---|---:|---:|
| Expanded service graph (nine objects) | 37.95s | 82.03s |
| Classic IPsec | 3.19s | 10.73s |
| Classic SNMP | 3.20s | 8.65s |
| Classic logging | 3.24s | 9.20s |

Every run completed cleanup. The service graph adds endpoint and object-tracker
groups with named references. Unit tests verify dependency ordering, rejection of
missing/duplicate/external names, and zero writes until both dependencies exist.
Native readback verifies resolved IDs in group payloads.

IPsec uses nested IKE/IPsec settings and a pre-shared-key variable. Numeric DH groups
follow the [published NaC example](https://netascode.cisco.com/docs/data_models/sdwan/edge_feature_templates/ipsec_interface/)
and are encoded as provider strings. The fixture is unattached and shut down.
SNMP covers nested views/OIDs, the NaC read-only authorization default, credential
variables and a trap target. Logging covers TLS authentication-name translation,
cipher lists, and IPv4/IPv6 servers. Its broken upstream ciphersuite-variable
expression is documented and rejected before mutation.

Full Go regression and focused race suites passed. Both generators reproduce their
metadata byte-for-byte; previously qualified classic mappings remain unchanged.

## Classic GRE

The unattached, shut-down GRE fixture passed native lifecycle (3.20s)
and live Kubernetes reconciliation with cleanup
(8.72s). Conditional ingress/egress ACL rows preserve literal/variable precedence
from the module, and scalar tracker names use the provider's list encoding.

Full Go regression and focused race checks passed. Metadata regenerated
byte-for-byte and all previously qualified classic definitions remained unchanged.

## Native implementation extension awaiting live qualification

Added 24 previously pending declarations: the remaining seventeen modern
policy-object collections, transport IPv4/IPv6 trackers and tracker groups,
Cisco SSE, system CA certificates and classic SIG credentials. Seven new fixture
sets and report-mode companion configs exercise these definitions locally.

Local tests verify source-to-API prefix splitting, numeric string encoding,
multiple API fields derived from one input, concatenated application/family and
geolocation arrays, conditional fields, profile-family restrictions, dependency
ordering and native reference resolution. Missing references, reference cycles and
references through the wrong public collection are rejected. A simulated Manager
test confirms that an absent dependency prevents a POST.

The pinned Cisco SSE schema has a `required` list misplaced inside `properties`
for a DPD interval branch; generation explicitly repairs that structure. Nested
tracker discriminator defaults are preserved. These changes have not been
qualified against a live Manager.

Validation: `go test ./...`, focused adapter/command race checks, and the isolated
Kubernetes reconciliation test with a simulated Manager passed. The inventory
fixture audit passed. Both metadata generators reproduced byte-for-byte, and all
previously qualified mapping definitions were unchanged.

No new live Manager or router tests were run for this increment. The status is
At that checkpoint: 84 fixture-qualified declarations, 40 awaiting live tests, 101
pending implementation, and one compatibility-blocked declaration. See the
[complete live-test and implementation backlog](../../controllers/sdwan-live-test-backlog.md)
for individual resources, fixtures and prerequisites. Full deployment parity is
still incomplete.

## OSPF and security continuation

Six of the remaining 101 declarations now have native mappings and working live
fixtures: classic security, classic OSPF, service OSPF, service OSPFv3 IPv4 and IPv6,
and transport OSPF. Added report-mode companion configs for all three fixture sets.

| Fixture | Native create/read/update/read and cleanup | Live Manager through Kubernetes envtest |
|---|---|---|
| `classic-security.yaml` | Passed, 3.25s | Passed, 8.52s |
| `classic-ospf.yaml` | Passed, 3.30s | Passed, 9.28s |
| `routing-ospf.yaml` | Passed, 19.10s | Passed, 38.22s |
| `transport-trackers.yaml` | Passed, 31.57s | Passed, 57.86s |
| `cisco-sse.yaml` | Passed, 6.47s | Passed, 12.93s |
| `classic-sig-credentials.yaml` | Passed, 3.34s | Passed, 8.95s |

The tracker, SSE and SIG fixtures were implemented in the preceding increment. Together these
runs increase live fixture qualification from 84 to 96 upstream declarations.
All registered cleanup calls passed. Kubernetes tests used local API server/etcd
1.35.0 with the native adapter and live Manager; no user-cluster rollout or router
attachment was performed. Counts now stand at **96 fixture-qualified, 34 implemented
but unqualified, 95 implementation-pending, and one TrustSec compatibility block**.

Security tests cover authentication-type translation, preservation of integrity
values, numeric-to-string replay-window/key-ID/epoch encoding, and conditional
infinite-lifetime flags. OSPF tests cover area container construction, the pinned
module's missing/true/false `no_summary` semantics, nested interfaces, conditional
LSA rows, policy variables, invalid inputs and missing-reference rejection.
The pinned classic OSPF module reads `dead_intervale_variable`; its correctly spelled
counterpart remains unavailable. Modern OSPF route-policy names are validated as
dependencies; using them still requires the pending native route-policy definitions.
VPN associations, device deployment and routing behavior remain unqualified.

SIG credential descriptions now follow the upstream name-dependent mapping.
Initial live attempts returned HTTP 400 `GTEMP0007`, “Data validation failed”.
Source review identified missing `isGlobal: true` and explicit empty `vipValue`
fields for variable secrets. After correcting those fields, the canonical Umbrella
fixture passed both native CRUD and Kubernetes reconciliation. Zscaler field
mappings also preserve the provider's paired value/variable-name fields; Zscaler
credential values and external authentication remain untested. The fixture contains
only variable names, and no credentials are stored in evidence. Its canonical name
must be absent before testing; existing global templates must not be adopted.

Both source generators reproduced metadata byte-for-byte. Previously qualified
metadata is unchanged. `go test ./...` passed; adapter race checks passed. Terraform was not executed or added as a
dependency.

## Classic interfaces, switchport, EIGRP and Zscaler continuation

Six additional declarations passed native create/read/update/read with cleanup
and Kubernetes reconciliation through local API server/etcd 1.35.0 and the live
Manager:

| Fixture | Native lifecycle | Live Kubernetes reconciliation |
|---|---:|---:|
| `classic-svi.yaml` | Passed, 3.33s | Passed, 8.52s |
| `classic-cellular-interface.yaml` | Passed, 3.17s | Passed, 8.76s |
| `classic-switchport.yaml` | Passed, 3.32s | Passed, 9.13s |
| `routing-eigrp.yaml` | Passed, 6.50s | Passed, 12.88s |
| `zscaler-sse.yaml` | Passed, 6.71s | Passed, 12.95s |
| `service-switchport.yaml` | Passed, 6.51s | Passed, 13.55s |

The Zscaler Manager rejected the initial fixture because it required
`data.trackerSrcIp`; a nested interface also required `tunnelSourceInterface`.
The fixture supplies both and passed with nested interface and tracker rows.
The temporary diagnostic response logging used to identify those missing
attributes was removed. Existing generated mappings were unchanged.
All five fixtures are unattached; router forwarding and feature associations
remain outside this evidence. Status: **102 fixture-qualified, 34 implemented
but unqualified, 89 pending implementation, one compatibility-blocked**.

## Custom application catalog

The NetAsCode `application_catalog.custom_applications` model now compiles to
the native custom-application API body, including server names, L3/L4 rows and
business-relevance translation. A simulated Manager test passed asynchronous
task completion, post-task ID lookup, readback, update and deletion. The real
Manager rejected creation with HTTP 400 `POLICYVALIDATION0001`: “SD-AVC is not
enabled on any vManage.” Read-only inventory showed the fixture absent after
the failed attempt. Live lifecycle and Kubernetes reconciliation remain
pending until SD-AVC is enabled in a test lab. Status: **103 qualified, 35
implemented but unqualified, 87 pending, one compatibility-blocked**.

## Service multicast fixture

`service-multicast.yaml` passed native create/read/update/read with cleanup
(6.78s) and live Kubernetes reconciliation (12.38s). The fixture includes
nested IGMP join groups, PIM interfaces and source-specific multicast. The
provider schema requires an access list for static RP rows; that branch was
not part of this fixture. No VPN association or router activation was tested.

## System device-access policies

`system-device-access.yaml` adds IPv4 and IPv6 device-access parcels with
sequence IDs, names, default and base actions, destination ports, inline source
prefixes, and IPv4 source ports. Native create/read/update/read and cleanup
passed twice (9.68s and 9.45s), and live Kubernetes reconciliation passed
twice (17.99s and 17.90s). The pinned provider request schemas are embedded
from the locked source and validated before writes.

`system-device-access-references.yaml` validates dependency ordering and
policy-object prefix-list reference paths locally. A simulated Manager test
also verified missing-reference refusal and cross-profile ID substitution.
Its live attempt stopped at
the policy-object profile POST: this Manager already has its singleton profile
and rejects a second one with HTTP 400. The referenced-parcel branch therefore
remains unqualified against a live Manager. No policy object or device-access
parcel was created by that attempt.

## Topology mesh and site UUID targeting

`topology-mesh.yaml` creates an isolated hierarchy group and site, a topology
profile, and a mesh policy using `site_groups`. Native create/read/update/read
and reverse cleanup passed in 11.44s. Real Kubernetes reconciliation passed
in 21.35s, including status/Lease persistence, idempotence, and cleanup.
The Manager returned HTTP 405 for the mesh collection GET; the native adapter
now finds the parcel in its parent profile and reads it by ID. The fixture
resolved the declared site to the live hierarchy UUID. A local test also
checks legacy site-name mode and rejects unknown site groups. No policy was
activated or attached to a device.

## Topology hub-and-spoke

`topology-hubspoke.yaml` creates two isolated hierarchy groups and sites, a
topology profile, and a hub-and-spoke policy with a selected hub, a spoke
site-group, and a nested hub preference. Native create/read/update/read and
reverse cleanup passed in 15.20s. Real Kubernetes reconciliation passed in
32.92s, including status/Lease persistence, idempotence, and cleanup. The
Manager received hierarchy UUIDs for all three target arrays. Local tests
verify group expansion, legacy name mode, and rejection of unknown groups.
No policy was activated or attached to a device.

## Service and transport IPv4 ACLs

`ipv4-acls.yaml` creates isolated service and transport profiles and IPv4 ACL
parcels with accept/drop sequences, match criteria, counters, logging, DSCP,
and port lists. Native create/read/update/read and cleanup passed in 11.62s;
real Kubernetes reconciliation passed in 22.92s with idempotence and cleanup.
The pinned provider request schemas validate emitted payloads.
`ipv4-acl-references.yaml` locally validates policy-object prefix, mirror,
and policer references and dependency ordering. That branch remains without
live qualification because the lab already owns the singleton policy-object
profile. No ACL was attached to a router.

## Service and transport IPv6 ACLs

`ipv6-acls.yaml` creates isolated service and transport profiles and IPv6 ACL
parcels with next-header and prefix matches, ports, accept/drop actions,
counters, logging, and traffic class. Native create/read/update/read and
cleanup passed in 11.45s; real Kubernetes reconciliation passed in 22.93s
with idempotence and cleanup. The pinned provider request schemas validate
the emitted payloads. `ipv6-acl-references.yaml` locally validates IPv6
prefix, mirror, and policer dependencies. Those reference branches remain
without live qualification because the current lab owns its singleton
policy-object profile. No ACL was attached to a router.

## Service and transport route policies

`route-policies.yaml` creates isolated service and transport profiles and
route-policy parcels with IPv4/IPv6 protocols, numeric matches, communities,
metric and local-preference actions, next hop, and origin translation. Native
create/read/update/read and cleanup passed in 11.79s; real Kubernetes
reconciliation passed in 22.99s with idempotence and cleanup. The pinned
provider schemas validate payloads. `route-policy-references.yaml` checks
as-path and prefix-list references and dependency ordering locally. Reference
branches remain without live qualification because this lab owns its
singleton policy-object profile. No policy was activated on a router.

## Transport VPN fixtures

`transport-management-vpn.yaml` and `transport-wan-vpn.yaml` each passed native create/observe/update/observe/cleanup against Manager 20.18.2.2 and live Kubernetes reconciliation with repeated idempotent observation. The management fixture covers DNS, host mapping, and IPv4/IPv6 next-hop routes. The WAN fixture covers ECMP, DNS, host mapping, IPv4 next-hop and IPv6 null routes, a NAT64 pool, and TE service. Both are unattached parcels; interface associations and device forwarding are untested.

`transport-management-interface.yaml` passed native create/observe/update/observe/cleanup and live Kubernetes reconciliation. The child parcel resolved its management VPN parent ID, used the nested Ethernet endpoint, and cleaned up before its parent. The fixture covers static IPv4/IPv6, a secondary IPv4 address, an ARP entry and MTU options. Device attachment remains untested.

`service-lan-vpn.yaml` and `service-dual-router-ha.yaml` each passed native create/observe/update/observe/cleanup and live Kubernetes reconciliation. The HA fixture resolved a declared LAN VPN parcel ID before POST and cleaned up in reverse dependency order. LAN VPN qualification now includes core fields, host mapping, IPv4/IPv6 next-hop and null routes. NAT, OMP advertisements, route leaking and child interfaces remain to implement.

Six VPN routing associations passed native create/observe/idempotent-update/observe/delete and live Kubernetes reconciliation: service OSPF, EIGRP, multicast, OSPFv3 IPv4/IPv6, and transport OSPF. The Manager returns the associated routing parcel ID and payload from each nested endpoint. A redundant PUT of an unchanged association returned HTTP 400, so CVK now treats that update as idempotent, as the pinned provider does. The association fixtures cleaned up their links before routing parcels and VPN parents.

`service-lan-vpn-svi.yaml` passed native create/observe/update/observe/cleanup and live Kubernetes reconciliation using the nested LAN VPN SVI endpoint. Its fixture covers IPv4/IPv6 addressing, ARP and MTU. VRRP, ACL associations, DHCP and device activation remain untested.

`service-lan-vpn-svi-dhcp-association.yaml` passed native create/observe/idempotent-update/observe/delete and live Kubernetes reconciliation. CVK resolved the LAN VPN ID, SVI ID and DHCP server parcel ID before associating, then cleaned up in reverse dependency order.

`service-lan-vpn-ethernet.yaml` passed native create/observe/update/observe/cleanup and live Kubernetes reconciliation using the nested LAN VPN Ethernet endpoint. Its fixture covers IPv4 addressing and ARP. `service-lan-vpn-ethernet-dhcp-association.yaml` passed the same native lifecycle and live reconciliation through the three-level DHCP association endpoint. Both are unattached parcels; advanced interface branches and device behavior remain untested.
