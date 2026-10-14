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

Only the four listed resource types and the checked fixture attributes have
live parity evidence. The full 226-resource module surface is not qualified.
The real-API-server tests exercise reconcilers directly; they do not claim a
container-image rollout or Helm installation into the user's existing cluster.
No asynchronous device deployment or recovery is claimed.
