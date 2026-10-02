# C3 claim-time network-authority qualification

Date: 1 October 2026. Candidate: clean commit `9c48a98c` on
`pr/johalley/tas-extentions`.

## Result and boundary

PASS for the evidence-bound, expiring manager grant and worker claim-time
enforcement exercised here. A target that opts into network policy can receive
mutation authority only for the manager-accepted sample identified by its
digest, producer revision, producer Pod UID and sequence, and only until the
original sample-derived expiry. The worker performs an uncached read and
requires that exact tuple immediately before every new mutation claim.

This closes the C3 grant/claim trust-boundary increment. It does **not** close
E03: administrator-required checks, critical-service prohibitions, overlapping
risk groups, byte pacing, continuous recovery/soak policy, and the physical
redundant/single-path/critical/congested service scenarios remain open.

## Safety contract implemented

- The manager obtains an uncached final observation before granting ordinary or
  drain admission. Device UID/generation/physical identity, live network-worker
  revision/Pod and the accepted policy result must all match.
- The grant records the accepted observation's deterministic SHA-256 digest,
  producer revision, producer Pod UID, sequence and absolute expiry. The expiry
  starts at collection time; reconciliation cannot make old evidence fresh.
- The evidence tuple is all-or-nothing. Native API validation permits atomic
  introduction at `Pending` to `Granted`, monotonic complete renewal while
  `Granted`, and clearing only when a revoked or policy-transitioned target is
  rearmed to `Pending`.
- A granted target may renew to a newer accepted sample only when the absolute
  expiry strictly increases. This permits a long, already accepted install to
  obtain current authority before a later activation without changing or
  replaying the accepted device operation.
- The worker rejects an expired, replaced or mismatched tuple before creating a
  new durable mutation claim. Once a physical RPC is accepted, later evidence
  failure stops new claims but preserves observation/recovery of that operation.
- Campaigns without an enabled network policy retain the existing behavior; no
  new device session, ServiceAccount or RBAC grant was introduced.

## Automated qualification

| Gate | Result |
| --- | --- |
| `go test -race -count=1 ./...` | PASS after the final renewal implementation |
| Pinned Kubernetes 1.35 `make test-envtest` | PASS; provider and controller real-API suites |
| Focused claim-time tests | PASS; exact tuple succeeds, expiry/replacement yields zero durable claims and zero activation markers, current worker rotation is required |
| Manager renewal tests | PASS; original-expiry binding, monotonic renewal and idempotence |
| Native API tuple transition tests | PASS; partial tuple and same-expiry mutation rejected, complete atomic grant and strictly later renewal accepted |
| Deterministic digest tests | PASS; stable canonical input, changed evidence changes digest, nil rejected |
| Generated CRD/deep-copy parity | PASS after the final change |
| Helm topology render contract | PASS |

## Reproducible artifact

| Item | Value |
| --- | --- |
| Commit | `9c48a98c44397a46cafc0afb445b01f4f18e11ca` |
| Image tag | `cvk-tas-extentions:9c48a98c` |
| OCI index | `sha256:3c87bdae886e9f14afd78cf8c47b731e89c28129a42c41f0ecd67b8cf7c0b9f7` |
| Linux/amd64 manifest | `sha256:196806f20c76149c2f0aab7d2b8a5e187cd9fdd6901bdc9813b66647696b34d4` |
| Image config | `sha256:24d09a2999e0fc1c6b6ad295ae6bc9eecbec04df47a516e05e24fb2404c06884` |
| Transfer archive SHA-256 | `19502257be80143ac0f0dc16fe228a9e7bc7fdb84e18a650efb9824816e598d5` |
| Physical Helm release | `cisco-vk`, revision 123, `deployed` |

The generated `IOSXESoftwareUpgrade` CRD was applied before Helm so the API
server enforced the new tuple schema during convergence.

## Physical read-only validation

Ubuntu16 ran k3s `v1.35.8+k3s1`. The manager, all three app-hosting workers and
all three network-management workers converged to the exact candidate. Fresh
accepted samples restarted on the replacement network workers and each sample's
producer Pod UID matched the manager-observed Pod UID. The three projected
Nodes were `Ready=True`, schedulable and untainted.

Three Kubernetes-native secure gNOI `OS.Verify` operations then succeeded:

| Device | DeviceOperation / UID | Running version |
| --- | --- | --- |
| `cat9k-live` (`198.51.100.100`) | `e03-os-verify-live-9c48a98c` / `fd7a7125-bef3-4423-b026-cd726a4b12e5` | `17.18.02.0.4112.1766116039` |
| `cat9k-lab-101` (`198.51.100.101`) | `e03-os-verify-101-9c48a98c` / `eefa88a6-0d1d-4463-ae71-0edd2069ec12` | `17.18.02.0.4112.1766116039` |
| `cat9k-lab-103` (`198.51.100.103`) | `e03-os-verify-103-9c48a98c` / `0e6607ac-4043-4a34-b390-23dc19aaaef7` | `17.18.03.0.5496.1776157760` |

Each operation was bound to the exact current manager-observed network worker.
The cohort reports no individual-supervisor install capability and an
unsupported standby supervisor. This is valid secure gNOI and current-worker
evidence, not redundant-supervisor qualification.

The controlled Pod rotation initially produced fail-closed status denials while
the manager still held the predecessor worker identity, plus ordinary Lease
optimistic-lock conflicts. After convergence, a final three-minute window was
free of manager and worker errors; all Nodes and workloads remained healthy.

No software image mutation was performed for this increment. Exercising a
negative authorization fence is not sufficient reason to disrupt a physical
device: unit, race and real-API tests establish zero-claim behavior, while the
physical run establishes exact candidate, schema, worker binding and secure
device connectivity.

## Remaining C3/E03 qualification

1. Implement administrator-required network checks, critical-service and
   singleton-path prohibitions, and bounded overlapping risk groups that include
   unhealthy non-target peers and concurrent campaigns.
2. Add measured byte pacing only after E02 supplies a non-management forwarding
   path, independent traffic source and predeclared accuracy tolerance.
3. Extend policy enforcement through recovery and soak without abandoning an
   already accepted device RPC; test policy epoch changes, API lag, manager and
   worker restarts, and install-to-activation delay.
4. Run E03-C–F with independently observed redundant, single-path,
   critical-service and congested-path fixtures. Labels and CDP adjacency alone
   do not prove forwarding redundancy or service continuity.
5. Complete reverse mixed-version/rollback and candidate-specific remote CI.

