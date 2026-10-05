# Corrected candidate: physical validation

Candidate: `bed271855fc788866b05bf85162b319e7d00d58c`, branch
`pr/johalley/tas-extentions`, Ubuntu16 Kubernetes 1.35.8.
**This is focused qualification, not full-roadmap merge acceptance.**

## Reproducibility and migration

The image is built from clean `git archive` of the full candidate, with that
full revision embedded in the binary. Helm revision 166 uses its matching chart.

- OCI manifest: `sha256:0a578e0bee6b8662aec60bef58dd73c682fd5fc109913d307bf429c9832b7af0`.
- Linux/amd64 config: `sha256:1d510b8c4cf7f309fc8c14a1b7db019b1988bdbe7097d17f67096d9453a51463`.
- The manager and all six scoped C9K workers use `cvk-tas-extentions:bed27185`.
- Native garbage collection removed both old `.103` Deployments after its
  retry backoff. No live finalizer, claim, receipt, ledger or Lease was cleared.
- All three virtual Nodes returned Ready. The excluded Nexus and historical
  duplicate Node are not this candidate's test targets.
- Both `.103` worker planes project only `device-ca.crt` from the existing
  managed ConfigMap as `ca.crt`. Neither projects the source Secret directly.

The [migration diagnosis](public-ca-and-policy-migration.md#snapshot-migration-exposed-native-cleanup-compatibility)
records the preceding `3d1ee490` failure. Its corrected-policy server dry-run
passes. The regression in an owned Kubernetes 1.35 cluster additionally proves
actual native cleanup of a stored legacy direct-CA Deployment and its children.

## Same-candidate strict HTTPS preparation and retirement

Only `.103`, device UID `782dc4b2-7e43-4188-8436-87633f0bf4fb`, serial
`FOC2416U0MV`, was prepared. The `.100`/`.101` HTTPS configurations were not
changed; strict transport is not claimed for those devices.

| Binding | Exact value |
| --- | --- |
| Parent | `roadmap-strict-bed27185-103` |
| Parent UID | `83c14d14-e8d3-4d6e-86ee-3b94801f4e9a` |
| Frozen/approved plan | `sha256:9d23c6c1364e4b34e10875f4a40a9c6d9572fafeaee3e5c7b5629615a8b9c0b7` |
| Leaf | `roadmap-strict-bed27185-103-cat9k-lab-103-86a6604d` |
| Leaf UID | `d58d42ab-1d26-4ecc-9b9e-2631ce110617` |
| Source digest | `c210d89b0bcbdeea4962b87b5f159c331988fe5a85d07a5a30da0438b2d99355` |
| Source size | 1,247,897,709 bytes |
| Receipt | `sha256:05c850f0d2894b1654a0985ddcf5f9194fbf915743975d055e6947fa72e4a2b3` |
| Network worker revision | `sha256:5dc42d490b756e4485ca9f867bed7798d7d2332308c13d6a8044531c30072b35` |

The manifest selects only that device and uses `PrepareOnly`, complete network
evidence, `BlockIfRunning`, one transfer/unavailable slot and the administrative
25,000,000 byte/s ceiling. Only its exact frozen plan received preparation
approval. No activation approval was issued.

One `PrimaryInstall` claim was persisted at `09:03:07Z`; preparation completed
at `09:04:39Z` on 5 October. It validated `17.18.02.0.4112.1766116039` while
running remained `17.18.03.0.5496.1776157760`. The parent health gate succeeded.
Cancellation revision 1 then settled. Receipt-bound retirement completed at
`09:08:15Z`, with native proof
`sha256:2ca0d86a9b15bc3c917e0312d8a1b722cc2a5f89c4130708b33140ae6f599d48`.
The entire original receipt and claim array remain unchanged.

Verified HTTPS native installer history is identical immediately after
preparation and after retirement: its sorted JSON SHA-256 is
`97ccd1e402ea97b179b665cc754e0578a6646e65210a24d68ffad00589a22eed`.
There was no replay, activation, software removal or OS reload. Independent
recomputation of receipt trust matches
`sha256:2decaec0545d5fe64b3cb47a6c647dda68f7406dfab5cc02d8a05574d12605dd`,
including public CA source revision `10580328`.

Times above are the lab control-plane clock. The workstation was approximately
one minute ahead; IOS XE's HTTP Date was approximately 130 seconds behind the
lab collector. Raw timestamps are preserved, not silently normalized. Native
retirement uses the server response clock in its receipt-correlated proof.

## App fixture correction and failed measurement

The preceding blocked migration left virtual Nodes NotReady long enough for
native taint eviction. The existing Deployment/ReplicaSet controllers created
replacement Pods. When workers recovered, replacements started on opposite
switches and obtained different DHCP addresses. Both current endpoints then
returned HTTP 200.

The actual Deployment templates use **preferred** node affinity, not hard
node pinning. A Pod's assigned `spec.nodeName` is not its owner's placement
constraint. Native lab admission restricts these unsigned packages to the two
authorized USB-backed C9Ks; attempts to bind elsewhere were denied. This is
evidence of native owner recreation and physical app startup, **not** evidence
of PDB-aware voluntary drain or measured uninterrupted service.

The old fixed-address collector completed with 258 samples, including **52
timeouts**, beginning at `08:59:33Z`. Preserve this failed measurement. It
neither proves a 52-request logical-service outage nor supports a zero-loss
claim: it followed abandoned Pod IPs and there was no stable Service probe.
The earlier, separately completed interval had 360/360 HTTP 200 samples.

A native ClusterIP Service `cvk-topology-drain-web` was subsequently created in
`cvk-pr194-workloads`, selecting only the existing drain-safe lab apps. Its UID
is `994dbf25-15f7-495b-a779-90518416426a`, ClusterIP `10.43.94.74`; a real HTTP
request through it returned 200. A fresh ten-minute profile samples the stable
Service once per second, records EndpointSlice identities every 30 samples,
uses a two-second request timeout, allows zero failed samples and requires
p95 latency at most 500 ms. It does not retroactively qualify the prior gap.

The new Service does not change device configuration, workload ownership,
Pod placement, signing enforcement, RBAC or any production CVK API.
The [executed Service manifest](lab-probe-service.yaml) is preserved. Inspect it
with native commands on Ubuntu16; discover its actual address rather than
reusing a Pod's historical DHCP address:

```sh
kubectl -n cvk-pr194-workloads get service cvk-topology-drain-web
kubectl -n cvk-pr194-workloads get endpointslices \
  -l kubernetes.io/service-name=cvk-topology-drain-web -o wide
kubectl -n cvk-pr194-workloads get pods -o wide
curl --noproxy '*' --max-time 2 http://10.43.94.74/
```

The last address is this run's discovered ClusterIP, not a portable default.

The completed stable-Service interval recorded **600/600 HTTP 200**, p95
3.99 ms, over 600.55 seconds ending `09:16:41Z`. Endpoint identities were
recorded throughout. This interval covers the public-CA fault/restoration
sequence below, not the earlier workload replacements or an OS reload.

At `09:17Z`, native server-dry-run Eviction passed with the existing PDB
`minAvailable: 1`. Temporarily tightening that exact PDB to 2 yielded
`TooManyRequests: Cannot evict pod as it would violate the pod's disruption budget`.
Both requests used `dryRun=All`, `deleteOptions.dryRun: [All]` and the exact Pod
UID precondition. No Pod was evicted. The original PDB minimum of 1 was restored;
both original Pod UIDs remained Ready. This validates the actual PDB boundary,
not CVK's complete voluntary drain/replacement/activation workflow.

## Same-candidate public trust fault and restoration

The exact retired receipt, empty ledger, settled maintenance and empty mutation
Lease were checked before changing the dedicated `.103` public-only CA copy.

1. At `09:09:38Z`, malformed non-secret test bytes replaced that copy. The
   manager removed `device-ca.crt` from the ConfigMap; both workers converged
   to empty mounts and zero ready replicas. Invalid bytes were never projected
   into a worker through the managed ConfigMap.
2. At `09:11:37Z`, only the dedicated malformed copy was deleted. Both worker
   revisions became `missing`, with empty trust mounts and zero ready replicas.
3. At `09:14:36Z`, the exact independently verified original public certificate
   was recreated. Both workers recovered with source revision `10593618`;
   all three Nodes and all six scoped workers were Ready at final inspection.

The public CA SHA-256 remains
`4b4857727a6d1a2e58a53d80c691c070cdfcd231d59182f30b58411a7721a202`.
No device certificate, trustpoint, signer key, credential or original identity
Secret was changed. Convergence is asynchronous, not instantaneous revocation.
A new strict-HTTPS inventory request succeeded at `09:18:05Z`, and native
installer history still has the exact post-preparation hash above.

The stable Service kept both existing `.100`/`.101` workloads observable during
these `.103` trust faults. It is not a transit-forwarding or redundant-path test.

## Read-only forwarding-fixture discovery

After recovery, ten read-only Cisco commands per authorized switch captured
interfaces, VLANs, trunks, routes and CDP neighbors. Follow-up app detail on
`.100` and `.101` confirms both current containers attach `eth0` to
**`mgmt-bridge100`**, with their DHCP addresses in the management subnet.
The management interfaces are in `Mgmt-vrf`, attached to a shared outside
switch. These successful HTTP probes are therefore not front-panel forwarding,
alternate-path, oversubscription or independently calibrated link-rate evidence.

Observed authorized front-panel adjacencies are `.100` ↔ `.101` on Gi1/0/1
and `.100` ↔ `.103` on Gi1/0/23. No direct `.101` ↔ `.103` adjacency was found
in these CDP captures. `.101` also has two active 10-Gigabit ports without
CDP mapping in this capture; default interface configuration does not establish
their remote endpoints or that they are safe test ports. The `.100` switch
also has links to outside switches. CDP absence is not proof of physical
absence, and none of those outside devices was accessed or changed.

The next forwarding prerequisite is an explicitly isolated data-plane fixture:
known ingress/egress endpoints and ports/VLANs, a verified alternate path and
capacity, a safe fault point, independent generator/receiver calibration and
fixed tolerances. Do not saturate the shared management network or relabel its
HTTP success as R2 qualification. Raw read-only captures are
`/tmp/cvk-20261005-path-{100,101,103}.jsonl` and
`/tmp/cvk-20261005-app-path-{100,101}.jsonl`; they remain local.

## Validation scope

Before this deployment: full uncached race suite (61 packages), all 47 pinned
real-API tests with no skips, full native shared-worker/startup/legacy-cleanup
matrix, rendered admission contracts, 36 Python safety tests and strict MkDocs
passed. API/CRD/RBAC/Helm generation repeated twice with no drift.
The [fleet-read result](fleet-read-budget.md) is a substep scale measurement,
not full controller-scale acceptance.

All six remote CI checks passed on this exact code candidate in
[run 37285965804](https://github.com/cisco-open/cisco-virtual-kubelet/actions/runs/37285965804),
including the native legacy-cleanup regression. Human approval and the wider
roadmap gates remain separate requirements.

### Raw evidence integrity

These captures remain local; this durable record does not publish credentials.

| Capture under `/tmp/` | SHA-256 |
| --- | --- |
| `cvk-20261005-ca-native-gc-denied.log` | `5956b97546ce224cbe29b3f02ff44dec47146429ed0e89f0fd66f4cba4187bd3` |
| `cvk-20261005-ca-gc-fixed.log` | `67332becef171386aabc5cd59fdeb4ff5ee59a3064509444c85fb4f7e2245283` |
| `cvk-20261005-gc-fleet-api.log` | `cf23fa4a58ff2c5f34d17622c9d08f038fcdb4b1127dfdcb850079c1f19f3bd8` |
| `cvk-20261005-gc-fleet-race.log` | `2fd2c4f02364cd4ab61536a802de4effa7fb39620d2ff222ceb00ef5a87fb0de` |
| `cvk-20261005-bed27185-retired.json` | `ca02c3d5e12e0f0476b846b72427357799e0b521924bc04967cc3ca3ce782c9c` |
| `cvk-20261005-bed27185-stable-service-probes.jsonl` | `e70f77c523e6c3646fa8be66b969053cab7039b9e5a6794ef8651d70ce3f5b1d` |
| `cvk-20261005-bed27185-pdb-dryrun.log` | `192c034b4234257a91b75a796a7bad668a713d400e666245bcca893e50845fc6` |

### Still-open acceptance

Revisit **every R0–R9** gate in the [execution plan](../../topology-roadmap-execution.md#remaining-completion-plan):
these tests do not qualify inactive-image replacement, both-direction drift,
forwarding redundancy/headroom, voluntary PDB drain, grouped drain, broad
distribution benchmarks, a second platform, sustained whole-controller scale,
independent cross-cluster fencing or the six final staged activation runs.
