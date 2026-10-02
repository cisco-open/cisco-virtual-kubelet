# Physical preparation and retained-ownership qualification — 2026-10-02

This directory records the sanitized physical IOS XE qualification added after
the October release. Receipt and image hashes needed to identify the tested
state are retained. Raw Kubernetes/device captures remain in the operator's
local `/tmp/cvk-topology-raw-2026-10-02/` archive because they include live lab
addresses, Secret references and maintenance-session tokens; they must not be
published with the branch.

## Candidate history

| Commit | Purpose | Result |
| --- | --- | --- |
| `4dee7ef5` | Add `PrepareOnly`, immutable prepared receipts and retained device ownership | Unit and API contract established |
| `f7e84315` | Accept an inactive IOS XE install only with correlated native inventory | Required for IOS XE's lost terminal install response |
| `eaf3b96d` | Correlate install evidence in the device clock domain | Kept skew handling fail closed |
| `c001f8f0` | Use the authenticated RESTCONF response `Date` when gNOI device time is unavailable | Physical preparation passed in both directions |
| `ddc02b82` | Persist retained-receipt conflicts as `Blocked/PreparedOwnershipRetained` | Physical negative test passed without a retry loop |
| `925b8ce2` | Delete the IOS XE `shutdown` presence leaf for explicit no-shutdown intent | Physical isolated-link restoration passed |
| `740ffd0e` | Wait for exact manager-owned managed-config worker binding | Replacement workers converged without transient admission errors |
| `25de1796`–`5e4cf1ae` | Define exact receipt-bound approval and execute activation in a separate leaf | E06 API and runtime contract established |
| `af50c7af` | Yield the retained preparation queue only to its exact authorized activation | Physical activation acquired authority without weakening unrelated conflicts |
| `2646e217` | Corroborate IOS XE 17.18 inactive inventory from exact retained install evidence | Physical activation passed without accepting generic `InProgress` |
| `3899e327` | Keep activation completion terminal after a child exists | Upgrade and downgrade remained `Succeeded` across later reconciles |
| `3f765999`–`23d8c060` | Add versioned scale evidence and settle safely consumed/unclaimed ownership | Synthetic scale and retained-audit paths passed |
| `5fb1738f`–`050ab07a` | Harden replanning, exact legacy-account migration and UID-bound re-enrollment | Physical `.100` reverse/forward handoff passed without changing its Node UID |
| `9e6cdfcc` | Count a successful content-bearing gNOI stream by its verified size | Corrected IOS XE's incomplete terminal progress accounting |
| `1556238a` | Share exact consumed-Prepared ownership semantics between manager and provider | A later physical `.103` campaign advanced while the immutable audit record remained |
| `12b513b7` | Exercise CVK's native TAS guard and rollout-ledger contention through real API servers | Kubernetes 1.37 served-field/race and Kubernetes 1.35 conflict/read gates passed |

The exact `ddc02b82` Linux/amd64 image was built locally. Its image config
digest was `sha256:59d9fd46bdb91f1334af41043dfafb9844906ecaebd664c058e9b406ef5a61cb`
and its Docker manifest-list digest was
`sha256:72bbcb74ef809b8a82fe0f8f25def9e52d8b1a2053be4de8ca2622f2ecd3ed25`.
Helm revision 141 deployed that tag to the manager and all six physical-C9K
app/network workers.

## Physical results

### `.103`: downgrade-direction preparation

`e04-prepare-only-result.yaml` records `cat9k-lab-103` running 17.18.03 while 17.18.02
was installed and validated as inactive. The leaf reached `Prepared` with
receipt:

`sha256:5561fcfb9b7ef0508bc5a964aa4132c3e647b7187e1fed2df8d32e8383436f84`

No `OS.Activate` claim or RPC was made. The device stayed on 17.18.03 and the
manager settled its transfer/disruption reservation while the immutable
receipt retained exclusive ownership of the device UID.

### `.101`: upgrade-direction preparation and restart

`e04-prepare-only-upgrade-result.yaml` records `cat9k-lab-101` running 17.18.02 while
17.18.03 was installed and validated as inactive. The leaf reached `Prepared`
with receipt:

`sha256:42bf1fb56e7cc0e8247fb2e866a637a96342e2971046ecb66f1477c6eb2f1a68`

The network worker was replaced after preparation. A new read-only
`DeviceOperation` reconfirmed the running version, installed inventory, boot
state, secure gNXI state, control-processor health and app-hosting inventory.
The native IOS XE operation count stayed at three before and after replacement,
and the replacement worker emitted no `OS.Activate` entry. The two appearances
of the primary install claim in the leaf are the live claim and its immutable
copy inside the receipt, not two device mutations.

### Retained ownership and controller behavior

The first live negative test found that the retained receipt prevented a
second preparation, but the expected safety condition surfaced as a reconcile
error and left stale status. `e05-retained-ownership/competing-rollout.example.yaml`
is a sanitized form of the manifest used to repeat that test on `ddc02b82`.
After exact-plan approval, the campaign reported:

```yaml
phase: Executing
counts:
  blocked: 1
targets:
  - deviceName: cat9k-lab-101
    phase: Blocked
    reason: PreparedOwnershipRetained
    message: >-
      prepared device ownership is retained: the target device UID is owned by
      the retained preparation for cat9k-lab-101
      (sha256:42bf1fb56e7cc0e8247fb2e866a637a96342e2971046ecb66f1477c6eb2f1a68)
```

The expected child did not exist, the controller-log reconciliation-error
count was zero, and the owner remained `Prepared` with the same receipt after
the test. The bounded negative-test campaign was then cancelled using control
revision 1 so no active test campaign remained.

### E10 isolated physical link

[`e10-isolated-link-change.md`](e10-isolated-link-change.md) records a
controlled physical link shutdown and restoration. The read-only graph moved
from complete (five nodes, nine edges, zero diagnostics) to incomplete (five,
seven, three), then returned to complete from fresh accepted evidence. The
test exposed and repaired IOS XE no-shutdown convergence and managed-worker
binding-order defects. Recovery respected the retained disruption lease; no
fence was manually bypassed.
Manager replacement preserved the topology-content hash while fresh evidence
advanced provenance, and native admission denied a live functional-worker
attempt to alter manager-owned accepted evidence. These results close E10's
defined read-only diagnostic acceptance matrix.

### E06 separately approved activation

[`e06-separate-activation.md`](e06-separate-activation.md) records the closed-
window hold and reciprocal separately approved physical activations. `.101`
activated its retained 17.18.03 preparation; `.103` activated its retained
17.18.02 preparation. Both exact versions were verified after reload, secure
gNXI/gNOI returned healthy, both Nodes returned Ready without taints, and the
ledger settled empty. Three defects exposed by the physical flow were repaired
and retested through exact candidate `3899e327`.

### E12 synthetic scale and ownership fencing

[`e12-synthetic-scale-and-handoff.md`](e12-synthetic-scale-and-handoff.md)
records the versioned 1/10/50/100-target benchmark profile and pinned Linux
results, production-manager latency/RSS sampling, retained-preparation fence,
and the physical `.100` reverse/forward handoff. E12-D cross-cluster transfer
and large-fleet production throughput remain open.

### E08 native TAS served-object guard

[`e08-native-tas-served-guard.md`](e08-native-tas-served-guard.md) records the
production CVK drain guard reading an actual Kubernetes 1.37 Pod with
`spec.schedulingGroup`, rejecting a stale resourceVersion after a live update,
and failing closed again for the current grouped Pod. This closes the
fake-reader gap without claiming physical grouped-workload qualification.

### E09 transfer measurement and cache decision

[`e09-transfer-measurement-and-cache-decision.md`](e09-transfer-measurement-and-cache-decision.md)
records separate origin-to-worker and worker-to-device bytes/times on `.100`
and `.103`, the IOS XE terminal-progress accounting defect and exact physical
retest. The bounded decision is to retain the verified ephemeral cache and
defer a shared PVC cache for this measured local path. Broader source/path and
resource-cost measurements remain required before generalizing that decision.

## Validation matrix

- `go test -race ./...`: passed on `12b513b7`, including the complete
  controller and topology rollout fault suites.
- `make test-envtest` with the repository-pinned Kubernetes 1.35 binaries:
  passed, including prepared-receipt required/immutable admission and the
  real API-server E12 ledger contention/read matrix.
- The pinned Kubernetes 1.37 native TAS lane passed with the production CVK
  served-object guard included.
- `make deepcopy-gen manifests`: produced no tracked diff.
- strict topology Helm lint and render: passed (`74` relevant native objects).
- generated config-family and parity checks: passed.
- locked MkDocs third-party license check: passed from `.venv`.
- All three physical virtual Nodes reported `Ready=True/KubeletReady` after
  deployment.
- A live server-side dry-run that removed `.status.preparedReceipt` was denied
  by both append-only immutability and the `Prepared`-phase receipt invariant.
- Strict shared-account RBAC denied the app-hosting account all upgrade-leaf
  mutation, denied the network-management account leaf creation, and allowed
  only its managed status path. All 27 installed validating policies reported
  no type-check warnings.
- Final topology-ledger reservation count was empty, all three disruptive
  maintenance Leases had no holder, both retained leaves were
  `Prepared/Settled`, and the manager emitted zero reconcile errors during the
  final ten-minute check.

## Qualification boundary

This evidence closes physical install-only preparation, worker restart/no
replay, retained ownership conflict behavior, separately approved activation
in both directions, the E09 measured cache decision, and E12-C single-cluster
handoff for the tested C9300/IOS XE cohort. It does not qualify E07 application
continuity, E08 physical native TAS, E11 another platform, E12-D cross-cluster
transfer or large-fleet production throughput.
