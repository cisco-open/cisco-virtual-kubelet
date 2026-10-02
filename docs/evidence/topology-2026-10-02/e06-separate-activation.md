# E06 separate activation qualification

Date: 2 October 2026

Cluster: Ubuntu16 k3s physical-lab owner

Qualified cohort: two Cisco C9300 switches, IOS XE 17.18.02 and 17.18.03

Final runtime candidate: `3899e327`

This record closes the E06 separately authorized activation contract for the
tested C9300/IOS XE cohort. It does not qualify workload service continuity,
native TAS group drain, a second platform, or fleet scale.

No credential values, Secret contents, management addresses, or maintenance
session tokens are stored here. The named Kubernetes objects remain available
in the lab cluster and the operator's private raw archive.

## Exact authority

The upgrade-direction campaign was
`roadmap-prepare-upgrade-20261002-101` (`5fbaa3ac-66bd-4970-a237-5dcad3c02520`).
Its frozen plan hash was
`sha256:eaafc6e6ad52e2b0096b282d99bcc793b1cf4e59a9a15d10dad79dd138e9a33e`.
The retained preparation leaf
`roadmap-prepare-upgrade-20261002-101-cat9k-lab-101-54744b3f` had UID
`58192293-1d4b-4a41-ae34-2502795e2ecb` and receipt
`sha256:42bf1fb56e7cc0e8247fb2e866a637a96342e2971046ecb66f1477c6eb2f1a68`.
The append-only approval bound that exact receipt, plan and device UID and used
the UTC claim window `04:27–06:00`. Activation executed only in the distinct
leaf `...-activate-270f44af`.

The downgrade-direction campaign was
`roadmap-prepare-downgrade-20261002-103` (`d11d1b77-adb3-4efa-a5f1-665537455cad`).
Its frozen plan hash was
`sha256:c10d58310ba33a6106d4263edb6f50334d715016b9067a6f7153bf2e03fba084`.
The retained preparation leaf
`roadmap-prepare-downgrade-20261002-103-cat9k-lab-103-a7787ae4` had UID
`db5f487c-42d1-4e0b-b776-e810934205bb` and receipt
`sha256:5561fcfb9b7ef0508bc5a964aa4132c3e647b7187e1fed2df8d32e8383436f84`.
Its UTC claim window was `05:00–06:30`; activation executed only in
`...-activate-cb319294`.

Each activation leaf contains exactly one durable `PrimaryActivation` claim.
The preparation leaves remain `Prepared` and immutable; they were not changed
into activation records.

## Execution and observations

1. A server-side dry-run detected that the installed CRD did not yet serve the
   additive activation-approval schema. The exact chart CRDs were applied
   before the controller image; no approval or device mutation was attempted
   against the old schema. This validates the required CRD-before-binary
   upgrade order.
2. The `.101` approval was installed while its activation window was closed.
   The campaign remained paused and no activation child or claim appeared.
3. At window opening, the manager created one preinstalled-image activation
   leaf. Its worker revalidated the exact preparation receipt, source/trust
   identities, native installed inventory, current policy and worker binding
   before claiming `PrimaryActivation`.
4. IOS XE 17.18 reports a completed inactive image as native `InProgress` in
   RESTCONF even while CLI inventory shows it inactive. Candidate `2646e217`
   accepts that state only when the exact retained receipt validates, the
   target/running versions match, the primary image is installed, and the
   preparation recorded correlated completed native-install evidence. Generic
   or mismatched `InProgress` inventory remains rejected.
5. The `.101` activation selected exact build
   `17.18.03.0.5496.1776157760`. The device reloaded, returned to secure gNXI,
   and passed exact OS.Verify without activation replay.
6. The reciprocal `.103` activation selected exact build
   `17.18.02.0.4112.1766116039`. During boot the gNXI endpoint temporarily
   reported that its authentication service was unavailable. The worker
   observed rather than replaying activation; secure gNXI recovered and exact
   OS.Verify then passed.
7. A controller defect found by the physical run briefly re-entered activation
   bootstrap after a terminal child existed. Candidate `3899e327` initializes
   activation only when no activation child exists. Both campaigns then
   remained terminal `Succeeded/HealthGatePassed` across later reconciles.

Approximate `.101` activation timeline: claim `04:45:34Z`, outage observed by
`04:52:40Z`, activation leaf complete `04:53:54Z`, campaign settled by
`04:55:30Z`. Approximate `.103` timeline: activation `05:01Z`, outage
`05:05:51Z`, transient authentication bootstrap at `05:08:07Z`, exact verify
and leaf completion at `05:08:36Z`.

## Final physical state

The final `.101` DeviceOperation showed IOS XE 17.18.03 running and committed,
auto-abort inactive, secure password authentication enabled, gNMI provisioned,
the gNOI OS service `Up/Supported`, a healthy control processor, and no hosted
applications. The final `.103` DeviceOperation
`e06e-activate-downgrade-post-20261002` completed at `05:19:26Z` and showed:

```text
Cisco IOS XE Software, Version 17.18.02
IMG   C    17.18.02.0.4112
Auto abort timer: inactive
Secure server: Enabled
Secure password authentication: Enabled
GNMI State: Provisioned
GNOI OS Image service: Admin Enabled / Oper Up / Supported
1-RP0 Healthy
No App found
```

Both virtual Nodes were `Ready=True/KubeletReady` with no taints. Helm revision
147 ran `cvk-tas-extentions:3899e327` in the manager plus both app and network
workers. The topology ledger (`8587a09e-451e-43a1-b638-0b2c11af78b6`) ended
with an empty reservation map. The `.103` last-release fence identifies the
exact activation reservation, showing that cleanup used the normal atomic
ledger path.

A read-only DeviceOperation containing the protected word `reload` was denied
by the diagnostic command guard. This negative result is intentional evidence
that the observation interface could not be used as an alternate mutation
path.

## Defects found and fixed

| Candidate | Physical finding | Resolution |
| --- | --- | --- |
| `af50c7af` | A retained `Prepared` leaf owned the per-device queue and blocked its exact authorized activation | Only the exact managed activation child may yield the validated retained parent; unrelated or tampered work remains blocked |
| `2646e217` | IOS XE 17.18 RESTCONF retained `InProgress` after a complete inactive install | Require exact retained-receipt and correlated completed-native-install proof, rechecked immediately before the claim |
| `3899e327` | A terminal activation child could re-enter bootstrap and regress campaign status | Bootstrap only when preparation succeeded and no activation child exists |

All three fixes have focused normal and race coverage. The existing activation
suite also covers receipt-set hashing, forged/drifted authority, window expiry,
concurrent ownership, lost responses, activation deadlines, pause/cancel,
standby ordering, trust/inventory drift and no-replay recovery.

## E06 acceptance result

| Gate | Result | Evidence |
| --- | --- | --- |
| E06-A API/admission | Passed | Append-only exact receipt approval, distinct `activate` permission, forged/drifted/expired cases in envtest/render/unit suites |
| E06-B concurrency/failover | Passed for the implemented contract | CAS ledger/claim tests, retained-owner queue race, restart/no-replay suites; physical worker replacements did not duplicate claims |
| E06-C closed-window physical hold | Passed | `.101` stayed prepared with no activation child until `notBefore`, then activated from the separately approved receipt |
| E06-D fault handling | Passed for fake-device matrix and observed physical boot/transient-authentication cases | Lost-response/deadline/standby/uncertainty suites plus both physical reload observations; unresolved outcomes remain fenced |
| E06-E physical upgrade/downgrade | Passed for this cohort | `.101` 17.18.02→17.18.03 and `.103` 17.18.03→17.18.02, exact Verify, healthy device/Node, settled ledger |

E06 is therefore complete for the stated C9300/IOS XE 17.18.02–17.18.03
cohort. Receipt invalidation after an external image removal remains an E04/E05
extension and must not be implied by this result.
