# Owned discovery replacement qualification

Date: 2026-10-16. Branch: `pr/johalley/catc-support`.

## Scope and implementation

The pinned NetAsCode 0.5.0 module uses Catalyst Center provider ~>0.6.1.
The [provider discovery resource](https://github.com/CiscoDevNet/terraform-provider-catalystcenter/blob/v0.6.1/docs/resources/discovery.md)
uses immutable replacement. Cisco's [discovery update endpoint](https://developer.cisco.com/docs/catalyst-center/2-3-7-5/updates-an-existing-discovery-by-specified-id/)
starts/stops discovery; CVK does not treat that PUT as a configuration update.

Public discovery-only inventory Apply now uses the existing section ownership,
operation journal, task observer and atomic action-receipt publisher. Creation
retains the exact task-returned native UUID. An owned completed/inactive job can
be replaced after a later manifest revision: verify its original selected native
footprint, delete the exact UUID, verify task completion and repeated inventory
absence, publish a deletion receipt, then create and verify the revised job.
The same name has one retained receipt, including across replacement.

An allow-listed non-secret field mask plus canonical effect digest preserves the
original source shape independently of later optional-field changes or credential
name resolution. Passwords and credential payloads are never fingerprinted.
Imported matching jobs remain observable without adoption. Foreign/replaced IDs,
active jobs, native footprint drift, lost acknowledgments and changed preflight
snapshots refuse mutation/replay. Mask-less legacy receipts allow bounded
read-only recovery/no-op, not replacement. Prepared/Verified release checks cannot
bypass the result receipt; Delete requires prior creation lineage.

Known-task Unknown recovery is an optional internal plan callback. It reuses the
existing task reader and Unknown-to-Verifying transition and preserves the SSID
recovery predicate. It introduces no public API field, new provider/controller,
transport, ownership store or changes to physical upgrade dispatch.

## Native qualification

The restored tunnel at `https://127.0.0.1:19443` authenticated successfully using
the private credential file. Test logs and private artifacts contain no token or
credential values. Mutations were serialized and limited to nonce-named discovery
jobs against existing lab switch `198.51.100.101`, using registered global
credentials. Discovery may contact that device; no software upgrade, site
assignment or template deployment was requested.

`TestLiveDiscoveryOwnedReplacement` passed in 60.98 seconds (package 62.778s),
with race detection and real Catalyst Center HTTP APIs/tasks. It drove the public
manifest reconciler using simulated Kubernetes persistence and JSON-reloaded
source/journal state. Each accepted Create/Delete/Create task was deliberately
restored as Unknown, then observed to completion without resubmission.

- Generated job: `CVK_DISC_43cc1d92dc523beb`.
- Initial UUID: `40`; create task `01a141be-f0df-7895-a8fd-1fd2489561e4`.
- Manifest revision: protocol order SSH to SSH,TELNET, same config/job identity.
- Owned delete task: `01a141bf-4e7c-7129-b4c4-3bdf454bc2f5`.
- Replacement UUID: `45`; create task `01a141bf-62ef-7fbf-b9b0-6bdd38e39f34`.
- Exact qualification cleanup task: `01a141bf-d2f1-70cf-8476-a00b93493560`.
- Total: two POSTs, two DELETEs including cleanup; no PUT or no-op replay.
- Final absence and original discovery baseline restored; target device UUID,
  serial and inventory count unchanged.

Native log: `/tmp/cvk-discovery-replacement-native-final.log`.
Private artifacts: `/private/tmp/cvk-discovery-owned-4273553783/` (directory 0700,
files 0600). These host-local paths are evidence locations, not repository files.

An earlier attempt completed product replacement but its harness rejected the new
verified UUID before refreshing its cached ID. The assertion now accepts only the
current owner's published receipt. The retained replacement UUID `35` for
`CVK_DISC_fe520a1b41496cda` was separately reverified against its creation task,
receipt, original footprint and baseline, then deleted by exact UUID. Cleanup task
`01a141be-a561-7898-b6a1-c45c2ac84c33` passed; the original baseline was restored.
The recovery helper uses a private exclusive write-ahead cleanup claim and never
repeats a DELETE without an acknowledged task. Its claim utility is shared with
the existing SSID qualification helper. Log:
`/tmp/cvk-discovery-replacement-retained-cleanup.log`. A subsequent read-only
resume passed in 0.38s (package 2.413s), observing the original deletion task and
absence with zero mutation attempts; log
`/tmp/cvk-discovery-replacement-cleanup-resume.log`.

## Regression checks and remaining limits

Focused discovery, original-field-mask, ownership/capacity/release, Delete lineage,
repeated absence, ambiguous submission, replacement, Unknown recovery and shared
SSID completion tests passed with race detection (6.124s). Native-tag compilation
passed. The shared SSID cleanup-claim concurrency/no-replay regression also
passed with race detection (2.122s). YAML examples and strict documentation
build passed, as did `git diff --check`. ND/provider/controller regression checks passed on the replacement code.
The full Catalyst Center package passed with race detection in 608.239s
(`/tmp/cvk-discovery-replacement-package.log`). The later test-only shared cleanup
helper refactor was additionally checked by live-tag compilation and its focused
concurrency/no-replay test; the final native replacement test used that refactor.

This qualifies discovery-only configs using registered global credentials. It
does not qualify replacement through mixed inventory/templates configs, inline
credentials, per-site discovery scope, automatic adoption, source-removal pruning,
explicit unchanged-intent rerun, arbitrary source revisions during an outstanding
operation, or deployment of this replacement path in the lab's installed worker.
Legacy combined paths retain their previous behavior and do not acquire ownership
retroactively. Discovery job convergence is separate from per-device discovery
success and authentication reachability. No claim of full 93-resource parity or
branch merge readiness follows from this result.
