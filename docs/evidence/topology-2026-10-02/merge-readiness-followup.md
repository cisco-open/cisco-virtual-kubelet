# Merge-readiness follow-up: staged protocol and physical app preflight

Date: 2 October 2026. Base: `c024e040`. The protocol fix is in the commit
containing this record; it has **not** yet replaced physical runtime `6f3686e9`.
This is a partial merge-blocker closure, not complete E01/E07/E13 acceptance.

## Compatibility defect reproduced and repaired

The released October worker (`cf33e51c8ffc6d47acb313857665366d74eefe6c`,
`v2026.10.0`) accepts a managed leaf with the newer `PrepareOnly` strategy
when its grant uses `rollout-v1`. Its strategy handling predates independent
preparation. Therefore an additive strategy enum plus a legacy grant is not
a safe mixed-version boundary. No physical RPC was used to demonstrate this.

`bash scripts/test-staged-protocol-compat.sh` compiles a test inside an archive
of that **exact released code**, with its existing managed-worker fixtures:

```text
TestOctoberWorkerStagedProtocolFence/rollout-v1: PASS
  positive control: legacy gate permits progress and a claim
TestOctoberWorkerStagedProtocolFence/rollout-staged-activation-v1: PASS
  new protocol: legacy gate denies progress and claims
```

The new `rollout-staged-activation-v1` protocol is required for managed
PrepareOnly and preinstalled activation, including paced preparation. Manager,
worker and maintenance/drain validation use the same selection contract.
Native CRD validation rejects legacy grants for those intents; the real-API
test covers both preparation and activation, both older protocol markers,
and an accepted new-protocol positive control. A runtime guard also rejects
unrecognized strategies instead of silently treating them as reload.

Already-settled legacy Prepared/Succeeded audit records remain readable only
with manager **and** worker settlement. New activation can consume the exact
old immutable receipt after all existing identity, approval, inventory and
trust checks; this does not rewrite the receipt or inherit an approval.
Unknown consumer protocols do not release ownership. Legacy active staged
operations are not granted new claims by the new worker.

The same compatibility review found that the released worker silently drops
the additive network gate fields. New network-enabled campaigns therefore
freeze `spec.requireNetworkEvidence: true` into each leaf. Non-staged leaves
use `rollout-network-evidence-v1`; staged leaves use the staged protocol with
the same evidence requirement. Native admission rejects incompatible or
evidence-free grants; the worker independently denies missing, stale or changed
authority at each mutation claim. Ordinary non-network campaigns retain their
existing protocol. Standalone requests cannot silently opt into a managed gate.
The pinned released-worker test covers both Reload and PrepareOnly.

### Deployment / rollback boundary

1. Pause new campaigns and inspect manager/worker settlement and device
   claims. Resolve all active old-protocol preparation/activation **using the
   existing compatible runtime** before installing this candidate. Also settle
   existing network-enabled campaigns: their immutable leaves predate the new
   network requirement and cannot be upgraded in place. Preserve
   uncertain claims and leases; do not edit their protocol to bypass a block.
2. Retain schema/object exports and immutable receipt/approval history. Apply
   updated CRDs first, then the matching manager and workers, with execution
   still paused. Confirm bindings and read-only health before enabling work.
3. New staged grants require the new runtime. Rolling an October worker back
   under a new staged grant fails closed, but that is **not** an operational
   rollback qualification. Do not downgrade CRDs or resume with old managers
   while new-protocol state exists. Stored-object/schema rollback and the full
   mixed-version deployment matrix remain R1 work.
4. Do not use a new standalone strategy on a worker predating that strategy;
   standalone objects do not carry the managed grant handshake.

## Automated verification

| Check | Result |
| --- | --- |
| Actual released-worker positive/negative protocol test | PASS; repeatable script added to smoke CI |
| API protocol selection: paced/unpaced, prepare/reload/no-reboot, preinstalled | PASS |
| New-worker legacy preparation denial; exact new grant positive control | PASS |
| Settled-audit-only migration and exact legacy receipt consumption | PASS |
| Unknown strategy fails before device dispatch | PASS |
| Full `go test -race -count=1 ./...` | PASS; final affected packages rerun under race after follow-up assertions |
| Full pinned Kubernetes 1.35 `make test-envtest` | PASS, 42 top-level tests including native staged-grant denial |
| Pinned CRD/DeepCopy/Helm generation, repeated | PASS |
| Topology Helm render contract | PASS |
| Strict MkDocs and actionlint v1.7.12 | PASS |
| Native shared-worker admission on isolated pinned Kubernetes 1.35 | PASS on `80ff447f`; retained Lease bootstrap, rotation, cross-plane denial and reserved-resource deletion |

The subsequent network-protocol change also passed the full race suite and
all 42 top-level real-API tests. The real-API protocol test now includes
missing network authority, both legacy protocols, and a complete compatible
positive grant. The released-worker probe covers both strategies and both new
protocol markers. The first pushed CI run failed only its gofmt gate; the
one-line formatting correction was pushed as `e7bc6013`. A later candidate's
remote checks must still pass before merging.

Candidate remote CI and deployment/physical lifecycle tests are separate
gates; earlier `c024e040` green checks must not be relabelled as this fix's CI.

## Fresh lab evidence and corrected prerequisite inventory

Read-only DeviceOperations executed the actual `show version`, `show inventory`,
`show interfaces description`, `show ip route`, `show file systems`, `dir flash:`,
`show app-hosting list`, `show app-hosting infra` and `show iox-service` commands.
All three devices remain on 17.18.03; all three IOx infrastructures report
running services and stable CAF.

| Device | Current app infrastructure | Consequence |
| --- | --- | --- |
| `.100` | USB-backed `/vol/usb1/iox`; verification already disabled; existing nginx HTTP endpoint returned 200 | Existing app baseline only, not proof of continuity during drain/reload |
| `.101` | USB-backed `/vol/usb1/iox`; verification already disabled | Historical attribution to absent SSD/USB is not supported by this fresh inventory; diagnose actual startup failure |
| `.103` | Internal `/mnt/sd3/iox_alt_hdd_mount_dir/iox`; signature verification enabled | Keep signing enforcement; package filenames alone do not establish trusted executable content |

No signature policy or trust anchor was changed. `hello-app.iosxe.tar` is
documented elsewhere in this repository as unsigned. Additional candidates
include `.103`'s `package_fullchain.tar`; a prior probe manifest was located,
but no positive startup/service qualification was established by this review.
[Cisco's app-hosting troubleshooting guidance](https://www.cisco.com/c/en/us/support/docs/switches/catalyst-9500-series-switches/222780-understand-app-hosting-on-catalyst-9000.html)
identifies `show app-hosting infra` as the signing-setting diagnostic.

Observed test-cohort adjacency is `.101` ↔ `.100` ↔ `.103`. Other discovered
neighbors include shared outside/management infrastructure, which is not
permission to fault that infrastructure. This observation does not establish
a redundant forwarding path or two independently measured traffic endpoints.

### Actual second-replica attempt: E07 not passed

The existing `cvk-topology-drain-b-0926` Deployment was restored from zero to
one replica. The native scheduler placed it on `.101`; the app installed and
reported `DEPLOYED`, but activation did not reach `ACTIVATED`/`STOPPED` within
the one-minute observation timeout. CVK retained the routine-write mutation
lease for its full safety duration; subsequent retries correctly did not
acquire it. The device's exact app detail also reported `DEPLOYED`, with no
attached network interfaces. The filtered device log confirmed installation
but did not establish why activation failed. This is **not** established as a
signature failure, successful deployment or settled device outcome.

A further `show app-hosting resource` returned 25% available CPU, 2048 MB
available memory and 55331 MB available storage, with no other app listed.
The failed app's profile requests 20% CPU, 409 MB memory and 10 MB disk.
This does not support simple aggregate resource exhaustion as the diagnosis;
it also does not prove the network configuration or every activation
prerequisite is valid.

The Deployment's original zero-replica intent was restored. Cleanup must pass
through normal CVK reconciliation and the retained lease, not force deletion
or fence removal. Until native app absence and lease settlement are recorded,
`.101` must not receive further disruptive work. Preserve the failed Pod UID
and correlated app ID in the local raw record for diagnosis. No new OS
upgrade/downgrade was submitted in this follow-up.

## Evidence locations

Secret-free test outputs and raw lab captures are retained locally:

| File | SHA-256 |
| --- | --- |
| `/tmp/cvk-merge-october-compat.log` | `eab7680738afac3ed17f66ae52bbce0abded435955baa0450b07bf1bbba2a533` |
| `/tmp/cvk-merge-race.log` | `19cef071a57567a554b1d4d47cf9373a1efccee5c557a3bcc5527bd6b15e11cb` |
| `/tmp/cvk-merge-envtest.log` | `d0e04b9069bee02e70be07e7d1b78404804c7e961dd65f9c82865fe790c55ae1` |
| `/tmp/cvk-merge-readonly-results.json` | `19df640b0f0c571ead0389215e80167e673480a875f72b48fba1ada093b56e1f` |
| `/tmp/cvk-merge-app-infra-results.json` | `521a8ca312f8e21b81567eff38a03bbdd3f6fc0de30b54c38763230906156185` |
| `/tmp/cvk-merge-network-final-race.log` | `aad894c49cae389df963a27832f684db9759c1548b087d1cee59416f1445977a` |
| `/tmp/cvk-merge-network-envtest.log` | `f603364b562d87185a8a721e77846efa0c414aca4a1a5c3e07f2f6241ba287d7` |
| `/tmp/cvk-merge-network-compat.log` | `bc8ada063f727b1b4575f4f64bbfc1c617d304b8bf5db8397c3b1a10f482abf7` |
| `/tmp/cvk-merge-shared-worker.log` | `53ef2cbe6e721a36f1b918a0e58a87eac3c3da0dc31f88f32606c26cad89e1a4` |

Additional live diagnosis is in `/tmp/cvk-merge-app-startup.txt`,
`/tmp/cvk-merge-app-diagnosis.json` and `/tmp/cvk-merge-active-leases.json`.
The sanitized findings above are the durable branch record; raw captures are
not suitable for publication without review/redaction.

## Next merge blockers, in order

1. Reconcile `.101` app activation and normal cleanup; preserve the fence until
   safe. Qualify a portable HTTP fixture and spare eligible capacity.
2. Complete R1 stored-object/reverse-manager migration and rollback tests.
3. Complete R3 safe native image invalidation/recovery, not a force-clear path.
4. Qualify the independent traffic/redundancy/congestion fixture, then actual
   app continuity/PDB negatives and restart recovery.
5. Deploy the frozen candidate and execute the full six separately approved
   prepare/activate sequences with service/path evidence before claiming the
   core merge gates closed. Wider roadmap work stays separately tracked.
