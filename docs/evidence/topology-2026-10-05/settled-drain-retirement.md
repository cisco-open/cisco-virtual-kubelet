# Physical voluntary drain and prepared-image retirement

This is a bounded R4/R3 follow-up, not complete-roadmap acceptance. The
preparation/drain ran on clean runtime `bed271855fc788866b05bf85162b319e7d00d58c`
on Ubuntu16 and the authorized `.101` C9K. No activation or reload was approved.
The native running image remained 17.18.03; the prepared target was 17.18.02.

## Real application drain

The [executed manifest](drain-prepare-lab.yaml) is preserved without any Secret
values. It was server-dry-run validated and created without approval; only the
resulting exact frozen hash was subsequently approved. Do not reapply this
historical intent to overwrite its retained audit, or reuse its old approval.

Campaign `cvk-live/roadmap-drain-bed27185-101`, UID
`b8ee036b-b7d7-4d6c-86c1-98afa3fddba4`, bound frozen plan
`sha256:04fd050e5ad27e0f9fb082bac6c6e5dca9fa70ac1fc85a81935c9abb7e74dedb`.
Its leaf `roadmap-drain-bed27185-101-cat9k-lab-101-ed048c9e` has UID
`fcd5a376-5524-41f5-a939-4de69696fee6`.

Before approval, both existing app Deployments were healthy and `.100` had
verified spare capacity. Their templates use preferred affinity across the two
USB-backed eligible Nodes; the actual bound Pod's nodeName is not a template
pin. Temporarily tightening the existing PDB from minimum 1 to minimum 2 made
CVK block admission before creating a leaf or requesting eviction. Restoring
minimum 1 allowed normal progress; no manual workload deletion or scale-down
was used. These existing apps are unsigned and depend on eligible USB storage;
this is not signed-package or no-storage positive qualification.

Times below are raw Ubuntu16/Kubernetes UTC on 5 October 2026:

| Time | Actual observation |
| --- | --- |
| 09:57:58 | Drain session `3a6b7a80-fd53-4a0d-a954-7995d79fe25a` started |
| 09:58:01 | Old Pod protected for coordinated device cleanup |
| 09:58:06 | Native Eviction requested |
| 09:58:09 | Deletion observed |
| 09:58:28 | Native ReplicaSet's replacement Running/Ready on `.100` |
| 09:58:29 | `.101` device-clean acknowledgement, inventory revision 1 |
| 09:58:30 | Old Pod released; drain subsequently Complete/Promoted |
| 10:00:14 | One primary-install claim recorded |
| 10:02:39 | Immutable preparation receipt created |
| 10:03:59 | Drain Settled; parent subsequently Succeeded after health soak |

Old Pod UID `e0372c34-40a3-4360-b41f-9a78c4dc7049` was replaced by
`23fbb82b-abc3-4f16-9050-85cb459d7204`, Pod
`cvk-topology-drain-a-0926-649c457fff-rcxqs`, on `cat9k-live`.
ReplicaSet UID `1802f6d2-ecbc-43a8-968e-3219c75e5b86` and Deployment UID
`3efd5abb-bd5d-401e-9afc-4a5f5c23aa0b` stayed unchanged.
The other app remained Running on `.100`. Direct HTTP to the new address also
returned 200 at 10:06:54 UTC. Native `.101` app inventory reported no apps.

The stable ClusterIP Service collector ran from 09:46:58.163864 through
10:16:58.883278 UTC (1,800.719 seconds), recording 1,799 HTTP samples, all 200,
with p95 latency 0.004011 seconds, below the predeclared 0.5-second budget.
EndpointSlices were captured every 30 samples. This is a one-second sampled
management-bridge Service test, not a zero-packet-loss claim, redundant
forwarding-path test, or service-through-reload qualification. It also does
not establish continuity after the collector ended. Device HTTPS response
clocks and local host clocks differ; raw clocks are not silently normalized.

## Discovered retirement defect and correction

Receipt
`sha256:cefa4ea74a20721cade7cd6e2960345c80b754efa5eccff57ac6a3de4f4904c1`
retains original control revision 0 and the complete settled-drain audit.
Cancelling the parent at revision 1 succeeded, but its separately authorized
exact-receipt retirement request was not published: the old validator required
the leaf's cancellation revision to advance. Advancing that revision would
contradict immutable settled-drain recovery evidence.

The correction keeps the old control/admission/worker acknowledgement, drain
session, Pod evidence, receipt and mutation claims unchanged. The manager
validates the cancelled, never-activation-approved parent and complete exact
drain binding, then appends separate observation-only retirement authority
with the newer parent revision. The worker still requires its current binding,
exclusive device lease, native quiescence and unchanged gNOI OS.Verify before
recording proof. No install, activate, package removal or force-clear action is
added. Unknown/unsettled/mismatched drains and changed eligibility fail closed.

Regression tests cover revision-zero nonempty drains, unchanged audit fields,
observer rejection, no device writes, native schema rejection of unresolved
drains, and old typed clients dropping new audit fields.

## Corrected candidate: physical retirement passed

Clean candidate `079f6b4f67b4c9f9e0d17f98f98765dc7d6cb0cd` was built from
`git archive`, not the working tree. The binary embeds that full revision.
Helm revision 167 deployed the matching chart and image on Ubuntu16 after the
upgrade CRD passed server-side dry-run and was applied using its existing
field manager. No forced field-ownership takeover was used. Image manifest:
`sha256:5b8793ccdad8d7b5c881075ac4fb8be16bbac978fb5d5538fe9e4cca360f9957`;
linux/amd64 config:
`sha256:c3be9178a8e5d0229c8542d93b358f30db7a3d9c68332d89dceec8f59b43c153`.
All six scoped workers converged to `cvk-tas-extentions:079f6b4f` and Ready;
the excluded Nexus Deployment was not changed.

During the mixed-version interval the manager published the original pending
retirement request, but the old worker refused its unsupported authority.
The replacement worker waited for its exact desired binding. At **10:32:45Z**
it recorded `PreparedInvalidated` and the cancelled parent recorded
`PreparationInvalidated=True/NativeProofRecorded`:

- Native evidence hash:
  `sha256:a44cb30f6c9225f95d06734598093ef3ed4471048a5aeb06a3eb3e04b28a8e49`.
- Request hash:
  `sha256:20a301de7fd6641a0b4e623839a16e8e40d178a8e0b6937683ff39dcb647c2d1`.
- Observer Pod UID `9d240555-f81b-43ab-a6af-4ba735d256fc`, worker revision
  `sha256:c6b75c41e88c59b2935e696016b2c1523c8444dab9a8ae1b149016bd3a0f15f9`.
- Native running version remained `17.18.03.0.5496.1776157760`; the target
  was inactive/Installed and the abort timer inactive.

Full JSON equality checks passed for the original receipt, claims, drain,
admission and control records. The worker acknowledgement kept control
revision 0 and Settled state; its worker-config revision and timestamp
legitimately refreshed for the replacement observer. It was **not** an
unchanged whole-worker-control object. The ledger has no reservations and all
three device-wide mutation Leases have empty holders; the last release fence
remains recorded. All three Nodes are Ready with no maintenance taint or cordon.

The extracted native `show install log` output is byte-identical before and
after retirement (SHA-256
`a3285e713f8cb6d89bcc9e4e3c164a66d025b34cf88c5a64cfcf0b61d3e4d181`).
No install replay, activation, image removal or certificate change occurred.
A direct operator deletion of the reserved network-worker Pod was denied by
native admission; its UID remained unchanged. That is a negative authorization
result, **not a successful post-retirement worker restart**. The separate
manager Deployment restart succeeded, reacquired leadership at 10:35:35Z,
and preserved the same native proof and revision-zero settled drain. No
admission policy, finalizer, receipt, claim or Lease was manually cleared.

A separate 30-minute Service collector started at 10:26:19 UTC for migration
and recovery. Its completed result must be recorded before claiming that
whole interval; the earlier 1,799-sample capture ended before this deployment.
The preparation on `bed27185` and retirement on `079f6b4f` are intentionally a
mixed-candidate migration test, not final same-candidate acceptance.

Local full race tests, all **48** real Kubernetes API tests (zero skips),
native shared-worker/startup/admission tests, 36 offline Python safety tests,
generation, Helm lint and strict documentation build passed. The native
startup harness's old summary labelled its pre-commit working-tree build with
base `0749a0f1`; that line is not clean-commit binary provenance. A follow-up
corrects the harness to label dirty source and emit the executed binary hash.
The physical image above has separate clean-archive provenance.

## Private raw captures

These logs remain local; the durable account above contains no credentials.

| `/tmp/` file | SHA-256 |
| --- | --- |
| `cvk-20261005-r4-service-probes.jsonl` | `196b93f5e92d5f4db0b10302f72babbf1e6ace0c9182f2fd55b66911c70d1fbd` |
| `cvk-20261005-drain-retirement-before-deploy.json` | `0d43decd58d737ec53c5dd90b4652511453f1c9091599a56eb37e00a36e4cf15` |
| `cvk-20261005-r4-prepare.yaml` | `9a55a532eb7e14b97dc3f79f194fe866ee4ccc2367805fb9805e5ff9663a7274` |
| `cvk-20261005-r4-pdb-blocked.json` | `00c41b406c235d4b0502a2bd3fe7dbf78c26c8638611f9cab9c907310005d4f7` |
| `cvk-20261005-r4-native-prepared.jsonl` | `4d35b9585ee67b4d27dc79cb70acca54d98b813534de7fe102e23a11eb1ccc81` |
| `cvk-20261005-drain-retirement-after.json` | `920bfe4f07b65ab2501751eacf633eeb19b6671100052f2f83b05a135c300376` |
| `cvk-20261005-drain-retirement-native-after.jsonl` | `063683506c431d2b6b26564f9c4972e78a808a6e78dd280e5e9b7fc26963109f` |
| `cvk-20261005-drain-retirement-ownership-after.json` | `e64261abd2d40b9d1b140cfecb398a8822f04d5d80a9d97c4d45d6fa7075a4ed` |
| `cvk-20261005-drain-retirement-manager-restarted.log` | `dd38fcfb37268ff8cac4c4ac2ec700d1e93dd3b2698de4dbcbc1a6601779956a` |
| `cvk-20261005-drain-retirement-full-api.log` | `09a9502a2e61f3feba8dd60b25c0cf16f951fa2e21e12df7ee819555bebf54d0` |
| `cvk-20261005-drain-retirement-full-race.log` | `8abf056422178d6458cf432ccbaae707a8b1a067097a997b80c197a4f164a4b2` |
| `cvk-20261005-drain-retirement-kind.log` | `535f404a43ab295143df18eaadeaba0ae164019bb6dd13d30e73fe8cd91398e8` |

Remaining R4 gates include the wider negative/restart matrix, signed portable
workloads, and actual upgrade and downgrade with replacement availability and
qualified service/path observations. R3 still requires inactive-image drift
and both-direction qualification. Do not mark either complete from this run.
