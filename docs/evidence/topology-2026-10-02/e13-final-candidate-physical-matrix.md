# E13 final-candidate physical upgrade/downgrade matrix

Date: 2 October 2026  
Runtime commit: `6f3686e9`  
Cluster: Ubuntu16, Helm revision 157  
Cohort: three physical C9300 IOS XE devices

This record closes the coherent three-device upgrade/downgrade matrix for the
capabilities and cohort stated below. It does not turn missing traffic,
application, second-platform or cross-cluster inputs into passing evidence.

## Candidate identity

The manager and all six C9K app/network workers ran
`cvk-tas-extentions:6f3686e9`. The Linux/amd64 image config digest was
`sha256:8b1ded747f105cab4d3eaf613bae98f6ad26005a4c7fbcb18c3c338157a623e7`;
the Docker manifest-list digest was
`sha256:2ccddc233fd92b700070c021cb51560d4530e38ea3b2ee4db91d303bcac03aee`.

`6f3686e9` fixes retained preparation retirement after an exact activation has
already succeeded and settled. A topology-policy ConfigMap resourceVersion may
change for metadata-only reasons within the same policy epoch. Requiring that
resourceVersion forever kept an otherwise consumed receipt in the queue. The
fix still requires the same policy UID and epoch, exact receipt/activation
identity and content, terminal success, verified target version and manager
plus worker settlement. Tests reject a changed policy UID or epoch.

## Executed sequence

| Direction | Campaign | Approved plan | Result |
| --- | --- | --- | --- |
| 17.18.03 to 17.18.02 | `roadmap-final-b8c713ff-downgrade-20261002` | `sha256:992f69e0d603552a84af15fb7d605c53793b4f552d59a5d151cb0b3c9f874c8a` | Three of three `Succeeded`; exact running and validated version `17.18.02.0.4112.1766116039` |
| 17.18.02 to 17.18.03 | `roadmap-final-6f3686e9-upgrade-20261002` | `sha256:5ed6851739be06e9baeff76db33ef520e868774c680d50a9204507bbace62c11` | Three of three `Succeeded`; exact running and validated version `17.18.03.0.5496.1776157760` |

Both plans selected the exact three-device physical cohort, pinned an image
digest and source identity, froze device/Node/physical identities and required
an explicit approval of the resulting plan hash. The downgrade transferred
1,247,897,709 bytes per target. The return upgrade transferred 1,249,368,115
bytes per target.

The canary ran first. Remaining targets progressed serially under
`maxUnavailable: 1` and `maxConcurrentTransfers: 1` at the global, region,
zone, redundancy-domain and cache-domain boundaries that applied to the
targets. While one device was unavailable, peers visibly remained blocked by
the topology budget rather than claiming a second mutation. The return order
was canary, `.103`, then `.101`.

## Safety and recovery observations

- Every leaf recorded exactly one primary install and one primary activation
  claim for its reservation. IOS XE's lost terminal install response was
  reconciled from exact native install inventory; activation recovery observed
  the recorded request and did not replay it.
- Reload recovery followed the expected sequence of timeout, connection
  refusal, temporarily unavailable IOS XE authentication service and final
  secure gNOI `OS.Verify` success.
- A transient `.103` manager-accepted network-evidence mismatch blocked the
  claim before mutation. The manager renewed the grant only after a new exact
  accepted sample, after which the worker claimed safely.
- All six leaves finished `Succeeded/Settled`, both parents reported
  `Succeeded`, every post-mutation health gate passed, and the topology ledger
  ended with no reservations.
- After each direction all three virtual Nodes returned
  `Ready=True/KubeletReady`; no maintenance taint or mutation holder remained.

A final read-only cluster check after the merged-head local gates again found
the three cohort Nodes Ready on IOS XE 17.18.3, the manager and each cohort
worker Pod Running on `cvk-tas-extentions:6f3686e9`, and
`reservations: {}` in the topology ledger. This check did not mutate devices
or replace the campaign's exact CLI and secure `OS.Verify` evidence.

## Workload boundary

The previously qualified app-hosting fixture on the canary was restored and
reached `Running`. A second historical fixture on `.101` used the unsigned
`flash:/nginx.tar` package and remained in `ContainerCreating`. The fixture
was scaled back to zero after capture. **Correction:** fresh device inventory
shows USB-backed IOx on `.101`; the earlier attribution to absent storage was
incorrect. Neither signature rejection nor the activation failure's root cause
was established. See the [follow-up diagnosis](merge-readiness-followup.md).

This is not E07 service-continuity evidence: there was no portable signed
package and independent endpoint probe spanning the complete disruption.

## Local raw evidence

The raw bundle remains local because it contains lab addresses, Secret
references and maintenance-session identifiers:

`/tmp/cvk-6f3686e9-final-matrix.tar.gz`

SHA-256:
`2c80a38eef4cefd4a794c351aa9112884552fc4750be86af579a5777c854eac8`

The bundle contains the pre-apply manifests, approved and final parent/leaf
objects, candidate Deployment inventory, Nodes after both directions,
manager/network-worker logs and the explicit unsupported-app capture. The raw
archive is evidence for the local operator, not a redistributable repository
artifact.

## Closure and remaining gates

This run closes the E13 final-candidate matrix for topology-budgeted IOS XE
17.18.02/17.18.03 software mutation on the tested C9300 cohort, including the
same-epoch retained-receipt regression. The following remain open and must be
reported as prerequisites for broader roadmap completion:

- E02/E03: independently generated forwarding load and declared redundant,
  singleton, critical-service and congested-path loss tolerances;
- E04/E05 extension: safe native image removal/replacement and explicit
  prepared-receipt invalidation;
- E07: portable signed app, independent endpoint continuity, no-spare-capacity
  and restart/cancel qualification;
- E08-C/D: a supported native Workload controller on physical CVK Nodes and
  group-aware physical drain/lifecycle;
- E11: a qualified second-platform image pair and service evidence;
- E12-D: a second cluster and offline ownership-transfer procedure, plus
  production throughput beyond the synthetic envelope.
