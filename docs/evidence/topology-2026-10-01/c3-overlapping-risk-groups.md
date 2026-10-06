# C3 overlapping administrator risk groups

Date: 1 October 2026. Candidate:
`7417b09b52ef724606474f7730d38d665e93199f` on
`pr/johalley/tas-extentions`.

## What changed

The administrator topology policy can now define up to 16 named risk groups.
Each group has a protected-label selector, a maximum number of concurrent
transfers and a maximum unavailable count. Groups may overlap: one physical
device is charged to every group it matches.

Planning freezes each target's sorted memberships and a hash of the complete
relevant physical fleet membership. Admission recomputes that hash and rejects
membership drift. The single existing ledger compare-and-swap counts unhealthy
non-target members and reservations from every campaign against every matching
group before it admits a target. A policy, selector, budget or membership
change cannot reuse an earlier approval.

Hash canonicalization now deep-copies slice- and map-backed policy data before
sorting it. This fixes a pre-existing possibility that hashing could reorder
the live parsed policy. Invalid risk-group budgets are rejected before any
in-memory ledger mutation.

## Automated verification

The exact clean commit passed:

- overlapping-group, non-target-health, cross-campaign and malformed-policy
  ledger tests;
- deterministic membership hashing, duplicate-physical-identity rejection,
  selector validation and no-input-mutation tests;
- target freeze and pre-execution membership-drift tests;
- generated CRD API-server static validation;
- `go test -race -count=1 ./...`;
- the pinned Kubernetes 1.35 provider and controller envtest suites;
- topology Helm/render compatibility, including absent/empty legacy values,
  duplicate names and selector keys outside `requiredTopologyKeys`; and
- strict MkDocs rendering and `git diff --check`.

Kubernetes rejects CRD `uniqueItems` validation because it has quadratic
runtime complexity. The generated CRD therefore bounds the list to 16 items;
the controller and ledger enforce strict sorted uniqueness in linear time.

## Exact image and deployment identity

| Item | Value |
| --- | --- |
| Commit | `7417b09b52ef724606474f7730d38d665e93199f` |
| Image tag | `cvk-tas-extentions:7417b09b` |
| OCI index / local image ID | `sha256:534a213f7a5fccc44ae3cecb13c473d5ea382b8ec4976134808ef201cb0f9524` |
| Linux/AMD64 manifest | `sha256:ad281f400867fa925721cdd941d63ec346f294a768d9ffcc915112e1f3a39886` |
| Image config | `sha256:91e985def7e450361feae60e1ae4a2aa84c207dd7c4422c76c03a05c6e13f06c` |
| Gzipped image archive SHA-256 | `c7238fe0c6afe7a104f6d893434a3f58ebf85d0c595fde66501aa4b01bae0918` |
| Packaged chart SHA-256 | `f8986d66b759dd0508282c1d0219e633ce4fcda1747e57f6706b414ed0163764` |
| Cluster | Ubuntu16, k3s `v1.35.8+k3s1` |
| Helm release | `cisco-vk`, revision 127 |

The reviewed additive rollout CRD was server-side applied before the final
manager rollout. Its live schema contains the frozen membership SHA-256
pattern and the 16-item target membership bound. The controller, three app
workers and three network workers converged to the exact tag. The existing
administrator policy was intentionally left unchanged, and no software image
mutation was initiated for this admission-accounting increment.

## Physical read-only qualification

All three CiscoDevices and Nodes returned `Ready`, with no taints. The latest
complete manager-accepted network sample matched the current manager-bound
network-worker Pod UID and revision on every device:

| Device | Current/accepted worker Pod UID | Sample sequence | Accepted at |
| --- | --- | ---: | --- |
| `cat9k-live` (`.100`) | `024e8ef6-3608-4ef7-9dc6-874359b4863a` | 5 | 21:49:53 UTC |
| `cat9k-lab-101` (`.101`) | `8617229c-4693-48cb-a09e-cf6c8353cf23` | 6 | 21:49:58 UTC |
| `cat9k-lab-103` (`.103`) | `51321fbb-35cd-4f7c-b949-7922c7b35346` | 6 | 21:49:42 UTC |

Fresh Kubernetes-native, secure, read-only `GNOIOSVerify` operations
succeeded:

| Device | DeviceOperation UID | Verified running version |
| --- | --- | --- |
| `.100` | `54038917-26cb-42df-b893-55e9770db186` | `17.18.02.0.4112.1766116039` |
| `.101` | `7268b0b9-a720-4dd5-8844-1c4c6dd9bd36` | `17.18.02.0.4112.1766116039` |
| `.103` | `a5f0ccff-830a-46be-9953-3e1f505abdb6` | `17.18.03.0.5496.1776157760` |

The explicit settled log scan across the manager and three current network
workers contained no warning or error records.

## Acceptance boundary

This closes the code/API portion of E03-A for overlapping membership,
cross-campaign atomic accounting and membership drift, and qualifies the exact
candidate's physical deployment and secure device connectivity. It does not
claim that labels prove forwarding redundancy. The lab still lacks an
isolated traffic source, service probe and controllable redundant/singleton/
critical/congested path fixture. E03-C–F physical service-path tests and actual
byte pacing remain open.
