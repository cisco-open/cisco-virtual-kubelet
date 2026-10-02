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

## Validation matrix

- `go test -race ./...`: passed on `ddc02b82`.
- `make test-envtest` with the repository-pinned Kubernetes 1.35 binaries:
  passed, including prepared-receipt required/immutable admission.
- `make deepcopy-gen manifests`: produced no tracked diff.
- strict topology Helm lint and render: passed (`74` relevant native objects).
- generated config-family and parity checks: passed.
- locked MkDocs third-party license check: passed from `.venv`.
- All three physical virtual Nodes reported `Ready=True/KubeletReady` after
  deployment.

## Qualification boundary

This evidence closes physical install-only preparation, worker restart/no
replay, and retained ownership conflict behavior for the two tested C9300
directions. It does **not** claim separate activation authorization: E06 still
requires an activation API bound to the exact receipt, a distinct approver
permission, activation windows and atomic disruption reservation before either
retained preparation can be activated. Until that exists, these receipts must
remain retained and ordinary campaigns must stay blocked.
