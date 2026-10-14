# SD-WAN fixture qualification — 14 October 2026

Target: supplied lab Manager, HTTPS, version **20.18.2.2**. Read-only discovery
reported six inventory entries across `vmanage`, `vsmart`, `vbond` and `vedge`.
No device configuration, template attachment or policy activation was performed.
The endpoint credentials and session material are excluded from this record.

Baseline: NetAsCode v1.5.0 (`5fbf037ea57368c7e6e77694c81330b6f7b16666`),
CiscoDevNet/sdwan v0.11.5 (`d3194b612c3f24421ec1727b6fa6e9c0bc98b07f`).
Oracle runtime: checksum-verified Terraform 1.9.8, utils 2.0.3, local 2.9.1.

| Check | Observed result |
|---|---|
| Native authentication and inventory | Passed; Manager 20.18.2.2, six entries |
| Upstream classic banner versus native mapping | Passed, including line endings and default device types |
| Native classic banner lifecycle | Create, read-back, repeated observation, update, verification and cleanup passed |
| Native system profile + banner parcel | Create in dependency order, read-back and reverse cleanup passed |
| Native unattached configuration group | Profile reference resolution, creation, read-back and cleanup passed |
| Reproducible upstream oracle for all four resource types | Plan allow-list passed; upstream apply, both differential tests and destroy passed |
| Real Kubernetes reconciliation with simulated Manager | Passed using API server/etcd 1.35.0; Lease and status persistence and repeat reconciliation |
| Real Kubernetes reconciliation with live classic banner | Passed; verified apply and repeat reconciliation, followed by cleanup |
| Real Kubernetes reconciliation with live profile/parcel/group chain | Passed; all three resources converged, including both section Leases, followed by receipt-bound reverse cleanup |
| Go regression suite | `go test ./...` passed after native group integration |
| Focused race tests | Passed for adapter foundation, SD-WAN, cisco-vk command and default resolver; final adapter checks include the journal-settlement change |
| Helm | Lint and template rendering passed |

The upstream comparison creates resources with the pinned module, then invokes
CVK's own compiler and native reads to compare canonical fields. No Terraform
provider runs inside the CVK worker. Native and upstream fixture cycles are
separate, avoiding simultaneous writers. All observed fixture cleanup calls
succeeded.

The expanded oracles passed for 58 resource declarations (52 distinct types) and
the checked fixture attributes. The full 226-resource module surface is not qualified.
The real-API-server tests exercise reconcilers directly; they do not claim a
container-image rollout or Helm installation into the user's existing cluster.
No asynchronous device deployment or recovery is claimed.

## Expanded fixture run

The pinned Terraform plan contained 44 SD-WAN objects plus its `terraform_data`
input resource. Hierarchy prerequisites were applied first, followed by the full
fixture plan. All native differential tests passed; targeted region-list cleanup
and the remaining Terraform destroy completed successfully.

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
preferences. The upstream plan also exposed the application-priority settings
parcel that is created even when settings are omitted from YAML.

The combined 44-resource Kubernetes/live reconciliation test passed in 730.69s,
including idempotent convergence and receipt-based reverse cleanup. No device attachment, policy activation or router-level behavior
is claimed. Untested upstream fields and resources remain visible in the coverage
inventory instead of being counted as complete.

## System/CLI feature extension

A separate upstream fixture set creates 12 objects: two isolated profiles plus ten
feature parcels. Native lifecycle and upstream differential comparison passed for
CLI config, system basic, BFD, global, OMP, NTP, logging, MRF, flexible port speed
and performance monitoring. Both cycles cleaned up their created resources.
Basic/global/OMP/NTP/logging definitions are reproducibly generated from pinned
upstream mapping expressions, API value schemas and provider defaults. Unsupported
mapping expressions or validation keywords fail generation. Nested-list and union
schema validation have regression coverage; schema bounds rejected invalid fixture
values before any controller write.

The separate 12-object Kubernetes/live reconciliation passed in 108.06s,
including convergence and receipt-bound cleanup.

## Security, GPS, UCSE and ThousandEyes extension

- Expanded system batch: 13 objects. Security native create/read/update/read/delete
  and pinned Terraform comparison passed. Live Kubernetes reconciliation passed in
  122.64s, including idempotence and receipt-bound cleanup.
- GPS/UCSE batch: four objects. Native lifecycle and pinned Terraform comparison
  passed. Live Kubernetes reconciliation passed in 23.21s with cleanup.
- ThousandEyes batch: two unattached objects using a token variable. Native
  lifecycle and pinned Terraform comparison passed; no external agent was started.
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
