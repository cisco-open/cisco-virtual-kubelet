# Discovery creation ownership and active stop/delete

Date: 2026-10-16. Branch: `pr/johalley/catc-support`.

## Pinned model and execution semantics

The pinned NetAsCode 0.5.0 module uses immutable discovery resources. The
[pinned provider implementation](https://github.com/CiscoDevNet/terraform-provider-catalystcenter/blob/v0.6.1/internal/provider/resource_catalystcenter_discovery.go)
records the discovery ID from its creation task; it does not require the discovery
runtime to be Complete/Inactive before recording the resource. Cisco's
[delete-by-ID contract](https://developer.cisco.com/docs/catalyst-center/2-3-7-5/delete-discovery-by-id/)
stops and removes the selected job.

CVK now records ownership of a masked discovery resource once the existing full
task observer proves successful native creation and readback proves the returned
ID, name and original selected-field fingerprint. The result receipt attests
resource creation, not completion of the scan or discovered-device convergence.
Unchanged desired jobs still return Waiting until Complete/Inactive; they cannot
become InSync merely because ownership was published. Mask-less legacy creation
recovery keeps its original completion requirement.

A later immutable replacement may stop/remove the exact owned active job before
creating its new definition. Delete-policy source pruning can remove obsolete
owned active or failed jobs without waiting indefinitely for runtime completion.
The same original native ID/name/field fingerprint, whole-model preflight,
one-use dispatch, durable owner/source/receipt/capacity checks, successful delete
task and repeated ID/name absence remain required. Imported, legacy-unverifiable,
replaced and drifted jobs do not acquire mutation authority. Opaque submissions
without acknowledged task IDs remain fenced. No production restart endpoint,
public field, controller, transport or upgrade backend was added.

## Public reconciliation tests

The discovery safety suite passes with race detection (6.850s). The new active
cycle creates a resource that remains Active, publishes ownership, repeatedly
refuses InSync for unchanged intent, replaces it after a manifest revision and
prunes the second active resource on source removal. All four Accepted operations
are JSON-restored as Unknown and recovered without replay. Changed native creation
footprints cannot publish ownership or authorize removal through a later source
revision. Failed-job cleanup, foreign preservation and existing discovery recovery
checks also pass. An initial negative fixture reused a stale config resourceVersion;
refreshing the fixture before its source change fixed that test without relaxing
product CAS checks.

A real isolated Kubernetes 1.35 API-server run now executes the full public active
cycle, rather than only writing synthetic receipts. It installs the Config and
Controller CRDs, uses real UIDs/generations/resourceVersions and persistent status,
and rebuilds the reconciler between transitions. Native HTTP/task responses are
fixtures in this test. `TestEnvtestDiscoveryActivePublicReconciliation` passes
(7.01s). Existing discovery receipt/CAS/release and IPAM tests also pass (package
21.359s). This is not installed-worker or live-native-plus-Kubernetes qualification.

## Native active qualification

The first native attempt completed creation/replacement/pruning and restored its
baseline but explicitly skipped active qualification: the appliance completed its
creation task after the active scan window. Private evidence:
`/private/tmp/cvk-discovery-owned-2466360186/`; log:
`/tmp/cvk-discovery-active-native.log`. It was not counted as an active-state pass.

The corrected test uses a test-only exact-owned-job restart as its precondition.
Before each PUT it verifies the original published creation receipt, ID/name and
field fingerprint, then writes an exclusive private no-replay claim. The request
contains only the exact ID and `discoveryStatus: Active`, as allowed by Cisco's
[start/stop endpoint](https://developer.cisco.com/docs/catalyst-center/2-3-7-5/updates-an-existing-discovery-by-specified-id/).
These private qualification requests are separate from CVK's public journal and
are not presented as a production restart feature.

`TestLiveDiscoveryActiveOwnershipLifecycle` passes (65.89s; package 68.031s).
Generated job `CVK_DISC_c034a2281095bd4f` used native IDs `75` and `80` against
existing .101. Both were observed Active/Queued immediately before the public
replacement/source-prune DELETEs. The four public operations recovered from
Unknown without replay; the two bounded test-only restart PUTs were separately
recorded. Final discovery baseline, target device ID/serial and device count were
restored/unchanged. No upgrade, template or site provisioning was requested.

| Effect | Native task |
| --- | --- |
| Public Create, ID 75 | `01a14248-9d9b-71bd-9f5f-483aaa91b80f` |
| Test-only restart, ID 75 | `01a14248-f416-7581-a25e-411341aa5d8c` |
| Public replacement Delete, ID 75 | `01a14248-f843-7df3-87fe-bd484b0b5ac1` |
| Public Create, ID 80 | `01a14249-1569-7adc-bdb4-aaff7438e79e` |
| Test-only restart, ID 80 | `01a14249-80fe-7d61-8fc7-97319e078fa6` |
| Public source-prune Delete, ID 80 | `01a14249-845c-77a2-b68b-a6445235e0cb` |

Private evidence: `/private/tmp/cvk-discovery-owned-1688435772/`.
`complete.json` records cleaned/activeQualified true, two POSTs, two DELETEs, two
private starts, two active-ready observations and two Active pre-delete reads,
with four known-task recoveries. `ownedWhileActive` is zero on this appliance;
native early creation-receipt publication is not claimed. Its creation/task
separation is verified by the public HTTP fixtures and real API-server test.
Native log: `/tmp/cvk-discovery-active-native-final.log`.

A separate `TestLiveDiscoveryInProgressOwnershipLifecycle` run passes (80.08s;
package 82.201s). It waits for the exact owned job to be Active/In Progress before
revising source, and independently records the same state immediately before each
DELETE. This qualifies executing-scan interruption as well as queued cancellation.

- Job: `CVK_DISC_af9c0e92f07b6307`; native IDs `85` and `90`.
- Public create tasks: `01a1424b-b209-7909-809e-4702f2d709bf` and
  `01a1424c-4f07-7fc1-af5d-894df6c802c2`.
- Bounded test-only restart tasks: `01a1424c-037c-7fed-a96d-c40d194fe492` and
  `01a1424c-a16d-7417-8670-80d943d5b904`.
- Public replacement/prune delete tasks:
  `01a1424c-33b4-7ace-b96f-e5b8c7113cce` and
  `01a1424c-cf7e-7e4e-a0d7-36327d9f7232`.
- Private evidence: `/private/tmp/cvk-discovery-owned-917808646/`;
  native log: `/tmp/cvk-discovery-in-progress-native.log`.
- Final `complete.json`: cleaned/activeQualified true, condition `In Progress`,
  two POSTs, two DELETEs, two private starts and four known-task recoveries.
  Both pre-delete files record Active/In Progress. Original native jobs and
  target device identity/count were preserved; no fixture remains.

The complete Catalyst Center package passes with race detection (621.365s;
`/tmp/cvk-discovery-active-package.log`). All production changes were present in
that run. Strict documentation validation passes and `git diff --check` is clean. The later test-only public lifecycle helper
extraction and API-server integration were exercised by the separate envtest run.
ND, gNOI, SWIM/software-lifecycle and model/migration-tool race checks pass
(migration tool 1.534s; other packages cached).

## Remaining boundaries

An initial create without a verified ownership receipt remains fenced on source
change; this addition does not introduce cancellation/handoff for an unverified
submission. Unchanged retained active jobs can still hold up a multi-job cohort.
Mixed discovery/tag/template/device cleanup, inline credentials, per-site scope,
legacy migration/adoption, richer runtime failure/outcome reporting, owner handoff,
whole-CR deletion and installed actors remain pending. These results do not claim
whole-section parity or branch merge readiness.


## Follow-on cohort progress

The retained-active-job cohort barrier listed above is now removed for validated
independent discovery creates/prunes; [cohort qualification](discovery-cohort-progress.md)
records the implementation, real API-server checks and native .101 cycle. Whole
cohort completion still gates InSync, and the other remaining boundaries apply.
