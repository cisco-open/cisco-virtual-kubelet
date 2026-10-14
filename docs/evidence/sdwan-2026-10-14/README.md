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

Native fixture coverage now includes 84 resource declarations (78 distinct types)
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
