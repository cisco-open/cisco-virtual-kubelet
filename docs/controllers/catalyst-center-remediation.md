# SWIM preparation and remediation

Investigated on 8 October 2026, branch `pr/johalley/catc-support`.
The original investigation and offline planner are retained below. The branch
now implements an **opt-in manifest-driven preparation flow**. Live qualification
results must be checked separately from unit-test results; historical supervised
cleanup evidence is not evidence of automatic execution.

## Manifest-driven preparation

The branch also has an opt-in `standardReloadProfile: CatalystCenter323` under
the Catalyst Center image source. It requires the dedicated
`rollout-controller-reload-v1` handshake and pins the appliance build to
`3.2.3-75346.100`. Activation uses the modern endpoint with an explicitly empty
`compatibleFeatures` list. The exact observed xFSU 17.x-to-26.x version-path
warning for the requested target is classified as non-applicable. The inverse
lab path also recognizes the exact xFSU downgrade warning from running `26.02.1`
to selected `17.18.04`, requiring all three observed description, expected and
actual detail strings. This does not permit other downgrade paths or generic
image warnings. Device xFSU
eligibility failures, unknown warnings, flash failures and image compatibility
failures remain blocking. The 17.18.04 to 26.02.01 normal-reload path passed
[live qualification](../evidence/catalyst-center-2026-10-09/standard-reload/README.md)
on this appliance build. An empty feature list is not a portable guarantee for
other Catalyst Center builds.
Existing sources that omit this field retain strict readiness validation.

Preparation policies may set `distributionReserveBytes` to reserve additional
space before distribution while retaining `requiredFreeBytes + headroomBytes`
as the pre-activation floor. The reserve is conservative even when an image is
already cached. It must cover the full incoming archive **and extracted packages**; it does not
replace Catalyst Center's native flash checks or calculate extraction automatically.
The example reserves 2,800,000,000 bytes (1.4 GB for each), in addition to
the 3,070,230,528-byte activation floor. Verify those bounds for each image and
platform; they are not a universal sizing formula. The earlier live candidate
used only 1,264,266,123 reserve bytes and required supervised recovery. A changed policy
requires a new immutable ConfigMap reference and approved plan.

On the qualified appliance, distribution also performs `install add`: the
26.02.01 transfer consumed approximately another 1.27 GB for extracted packages
after the 1.264 GB archive. Size the distribution reserve for **both** when the
pre-activation floor must remain available. An archive-only reserve can pass
the first gate and then stop safely at the second gate. The policy does not
authorize deleting inactive target packages or unrelated application archives.

Use `examples/configs/catalyst-center/swim-preparation-policy.yaml` with the
`preparation` reference shown in `swim-rollout.yaml`. The source pins the immutable
ConfigMap's name, UID and SHA-256 of its exact `policy.json` bytes. Create a new
policy and a new approved plan to change limits. This is a CVK operational policy,
not an extension of the Netascode Catalyst Center desired-state model.

Read the actual reference after creating the policy (hash the stored data, including
its trailing newline, rather than a reformatted JSON document):

```sh
kubectl get configmap swim-retired-archive-cleanup-v1 -n network-devices -o json |
  python3 -c 'import sys,json,hashlib; c=json.load(sys.stdin); print(json.dumps({"name":c["metadata"]["name"],"uid":c["metadata"]["uid"],"sha256":hashlib.sha256(c["data"]["policy.json"].encode()).hexdigest()},indent=2))'
```

Use those three values under the Catalyst Center source's `preparation` field,
then create and approve the rollout through the usual frozen-plan process.

The manager validates the policy before freezing the plan. The new
`rollout-controller-preparation-v1` protocol and `CatalystCenterPreparationV1`
execution marker prevent older workers from executing the opt-in profile.
Sources without `preparation`, Direct/gNOI upgrades and Nexus Dashboard retain
their existing execution paths.

For each of the pre-distribution and pre-activation stages:

1. The XE worker retains the canonical mutation lease and maintenance reservation.
   It checks fresh committed OS/native install, boot, filesystem, preparation
   receipt and native application inventory. Partial or ambiguous evidence blocks.
2. The pure planner chooses only exact root `flash:gNOI_iosxe_<version>.bin`
   archives backed by **explicitly invalidated** CVK preparation receipts.
   Installed versions, current/target images, boot references, unresolved
   preparations, application files and arbitrary paths remain protected. XE's
   automatically cataloged `Present` state is accepted only for one `IMG/new`
   archive with an exact source path, verified native size, no pending package
   action, and no extracted packages anywhere in the observed flash tree. It is
   never treated as equivalent to an installed or added image.
3. The worker verifies exact receipt size and SHA-256 through the existing XE
   SSH transport's constrained single-file SCP reader. These lab XE releases do
   not implement gNOI File.Get/Stat. The controller worker gains no device
   credentials. The SSH side-channel retains its existing configured trust model.
4. The worker repeats live admission and native references, records a removal
   claim durably, and submits a single constrained `delete /force` operation.
   A restart never replays that claim. Native absence and observed free space
   establish completion; presence after a short observation grace period leaves
   `OutcomeUnknown` and retains the mutation fence.
5. Catalyst Center records its own synchronization claim before
   `PUT /dna/intent/api/v1/network-device/sync`. It requires the exact device's
   successful child task, then starts fresh readiness checks. Parent task success
   alone cannot advance the flow. Ambiguous submissions are never replayed.
6. Distribution/activation use the existing SWIM handoff. Native gNOI OS and
   install inventory still verify the committed target before maintenance release.

The initial profile is deliberately limited to qualified single-RP XE flash
inventory. It does not run `install remove inactive`, erase files by glob, repair
routes, modify startup configuration, or weaken readiness checks. Both file and
byte limits apply **cumulatively across both stages**. If eligible archives cannot
provide the requested free space plus headroom, execution stops with a blocker.
The current committed package set and boot manifest are retained as the rollback
baseline. Historical rollback metadata alone does not make a separately retired,
uninstalled source archive a retained rollback image.
A no-op preparation still requires complete evidence and sufficient observed
space; it is not permission to bypass a failed Catalyst Center Flash check.

Audit records are in the leaf's `status.controllerHandoff.preparation` and the
handoff's `status.inventorySyncs` / `status.readinessHistory`. Each cleanup record
retains its plan hash, policy hash, receipt UID, exact file identity and claim /
completion timestamps. Do not clear these records to force retry an unknown
mutation outcome.

Pausing and resuming keeps the original staging claim as historical evidence.
A resumed Catalyst Center handoff requires a fresh grant at the current control
revision, with the same reservation and policy epoch and all normal admission
checks. The original claim is neither rewritten nor duplicated. Direct-device
claim semantics are unchanged.

The manager can replace a worker at an explicitly paused, pre-preparation
boundary only when the handoff journal has no preparation, synchronization,
readiness, distribution or activation dispatch records. It retains the exact
maintenance session, topology reservation and live mutation lease. This does
not authorize replaying an ambiguous or already submitted action.

It can also repair a worker before the second preparation when the controller
has verified the completed distribution, the first preparation and sync are
complete, and neither the second preparation nor activation has been claimed.
The completed distribution journal remains unchanged. Native pre-activation
observation correlates the exact staged target with a successful install-add
after distribution started, using the device response clock, verified source
size, added package states and quiescent history. This handles XE's stale
`in-progress` version marker without changing retirement or direct-gNOI rules;
the target's archive and packages remain protected from cleanup.

An explicitly cancelled rollout can settle at the first completed preparation /
synchronization boundary, before any distribution or activation claim. The
controller requires a completed bound readiness task and atomically records
`PreparationCancelled`, permanently stopping dispatch. The XE worker independently
checks the unchanged healthy committed baseline and continued archive absence
before recording `Cancelled` with mutation-settled evidence. Existing manager
health gates then recover scheduling. The handoff remains as retained audit
evidence. Incomplete cleanup/synchronization, uncertain outcomes and later stages
cannot use this narrow cancellation path.

## Implemented: offline PlanOnly report

Run from the repository root, supplying a JSON observation:

```sh
go run ./cmd/cvk-preparation-plan < examples/configs/catalyst-center/preparation-plan-input.json
```

The checked-in example deliberately reports blockers: it has placeholder
identities, incomplete inventory, an unresolved reference and an old timestamp.
It must not be changed to claim complete evidence merely to obtain a green report.
For automation, build the executable first (`go build -o /tmp/cvk-preparation-plan
./cmd/cvk-preparation-plan`); the executable exits 0 for a space-satisfying
estimate, 2 for blockers and 1 for invalid input. `go run` wraps nonzero program
exit codes. Input is capped at 8 MiB; unknown fields, duplicate JSON keys and
trailing documents are rejected.

`internal/softwarelifecycle` owns the pure planner. It requires immutable
identity/policy bindings, positive space requirements, bounded freshness,
quiescent state, evidence for every reference domain, and a retained rollback
image. It considers only canonical `.bin` image archives with exact size and
SHA-256. References always protect files; packages, configuration, diagnostic
and application archives are excluded. Candidates are selected in path order
within both file-count and byte limits, stopping once the estimate covers the
required free space plus headroom. This conservative selection is not an
optimal-subset solver. Insufficient bounded capacity remains a blocker.

Reports include every file decision and a SHA-256 of the normalized input.
The digest binds policy, references, identity, timestamp and inventory revision;
it is not a signature or an authorization token. `ValidatePreparationPlan`
rejects changed or expired input. A new observation, even with identical files,
requires replanning. Estimated reclaimed bytes do not prove actual free space.

This tool does not collect live inventory, resolve prepared-receipt lifecycle,
authenticate supplied evidence, connect to devices or submit actions. The device-worker integration described above supplies these facts independently
of target manifests. The offline tool itself has no mutation authority.

## Historical investigation (before handoff and automatic preparation)

The following sections retain the original findings and design discussion. The
manifest-driven implementation above supersedes their implementation-status
statements; the historical supervised evidence remains separately identified.

### Subsequent supervised test: managed maintenance boundary

The user explicitly approved reserving and cordoning the empty `.101` node for
a supervised controller upgrade. The attempt created the audit ConfigMap
`cvk-live/cvk-swim-101-20261008`, then Kubernetes admission rejected the Node
update: managed Nodes are manager/bound-worker owned. Execution stopped before
the Lease update or any SWIM distribution/activation. A subsequent read confirmed
`spec.unschedulable=false` and no mutation Lease holder. The audit record is
`BlockedBeforeReservation`; no admission policy was disabled or identity impersonated.

This is an implementation gap, not a request to relax the cluster's security.
`maintenance.Coordinator.BeforeMutation` explicitly rejects generic disruptive
operations in managed topology. `CiscoDeviceReconciler.validateMaintenanceRequest`
requires a campaign-owned `IOSXESoftwareUpgrade` and its exact manager grant,
worker identity, mutation claim and maintenance session. Therefore a new
operational-action cleanup kind by itself cannot provide the managed SWIM flow.
The XE software-upgrade worker must retain ownership of maintenance and delegate
the controller operation through an authenticated durable handoff. The CC worker
must not acquire a parallel lease or impersonate the device worker.

Modern SWIM engine tests now cover correlated device workflows and fresh
readiness before each submission. They do not establish a deployed upgrade,
working automatic cleanup or an implemented maintenance handoff.

## Recommended ownership

Use manifest-selected preparation policy with two explicit owners:

- **Device worker:** exact-file inventory, eligibility checks and, when
  authorized, gNOI removal through the existing operational-action path.
- **Catalyst Center worker:** controller inventory/readiness, distribution,
  activation and controller task/workflow observations.

Keep controller credentials in the controller worker and device credentials in
the device worker. Preparation through the device worker does not change the
upgrade executor: SWIM remains Catalyst Center. Do not implement a second SSH
or gNOI stack in the CC adapter. Read-only SSH was used for this investigation
only; it is not the proposed runtime transport.

## What the appliance exposes

The installed appliance reports `3.2.3-75346.100`. The device-image details API
returned no `compatibleFeatures` property for any of the three switches.
The documented modern [distribution request](https://developer.cisco.com/docs/catalyst-center/distribute-images-on-the-network-device/)
contains image IDs and validation IDs; the documented
[activation request](https://developer.cisco.com/docs/catalyst-center/update-images-on-the-network-device/)
also supports feature key/value pairs. Neither published request schema
establishes per-file cleanup allowlists, digest preconditions, byte ceilings
or CVK preparation/rollback exclusions. Absence from these responses/docs is
not proof that no other product API can control cleanup; these guarantees
remain **unverified**, so they must not be advertised as supported.

Cisco [documents native automatic cleanup](https://www.cisco.com/c/en/us/td/docs/cloud-systems-management/network-automation-and-management/catalyst-center/3-1-x/user_guide/b_cisco_catalyst_center_user_guide_3_1_x/b_cisco_dna_center_ug_3_1_x_chapter_0100.html)
when distribution encounters insufficient flash. Consequently distribution can
be a destructive preparation operation, not just a file transfer. Do not submit
it as a way to discover what the appliance would delete. CVK should first
establish sufficient verified space and enforce its retained-image obligations,
or qualify a native cleanup capability that can enforce the same policy.

## Physical findings

### `.103`: protect install state before selecting files

Read-only CLI on `cat9k-lab-103` returned:

- `show install summary`: committed `17.18.03.0.5496`; inactive
  `17.18.02.0.4112`; auto-abort timer inactive.
- `show boot`: next-reload boot file `flash:packages.conf`.
- Flash free space: **2,504,589,312 bytes**, about **2,388.6 MiB**.
  The previous 17.18.04 readiness run requested about 2,620 MB; fresh
  controller/platform calculations are required at execution time.
- `gNOI_iosxe_17.18.02.0.4112.1766116039.bin`: **1,247,897,709 bytes**,
  alongside extracted 17.18.02 and 17.18.03 packages/configuration files.
  Its size makes it worth investigating, not automatically eligible to delete.
- `flash:core/`: 34 old `.core.gz` files totalling **114,425,405 bytes**
  (109.1 MiB). Even archiving and removing all 34 would leave only about
  **2,497.7 MiB**, below the reported requirement before additional headroom.
- App-hosting archives including `nginx.tar` remain on flash, although
  `show app-hosting list` returned no installed applications. An empty current
  application list does not make deployment archives eligible for removal.

Kubernetes retains prepared receipts for both 17.18.02 and 17.18.03, plus
explicitly invalidated older receipts. For example,
`roadmap-prepare-downgrade-20261002-103-cat9k-lab-103-a7787ae4` remains
`Prepared` with a 17.18.02 receipt and settled manager admission; its activation
child was previously observed as succeeded. The planner must resolve whether
each receipt was consumed, superseded or explicitly retired. Neither a stored
receipt alone nor its age establishes a current reservation or deletion safety.
Do not delete historic Kubernetes objects to manufacture eligibility.

The investigation did not exhaustively traverse flash, hash candidates or
resolve every package/receipt reference. **No file deletion plan is approved
by this inventory.** Archive-first diagnostic cleanup could be another typed
action in future, but must have its own retention policy and verified archive
receipt; it cannot be hidden under image cleanup.

### Transfer path: keep configuration separate

All three switches have a `Mgmt-vrf` default route via `198.51.100.1`.
`.101` and `.103` have no default-VRF default route. `.100` has a default-VRF
default route via `10.49.156.1`. Three ICMP probes to `192.0.2.254` failed from
each switch in both default and management VRFs.

These observations support the controller's warning about default-VRF transfer
reachability but do not establish an HTTPS/SCP failure: ICMP can be filtered.
Next qualify the actual device/controller transfer direction, protocol, source
interface, VRF and TCP port. Do not add a generic default route or route leak to
make a precheck green. If a change is needed, express it as a separate existing
configuration resource and wait for its exact generation to converge.

### Reload mode

The previous controller check reported xFSU ineligibility on `.100` because of
its STP root/forwarding-link state. That does not automatically prohibit a
normal reload. Conversely, a requested normal reload does not itself prove
that CC's API will select that mode. Qualify the API feature contract, then
classify a known xFSU-only warning as non-applicable. Never change STP topology
solely to satisfy an upgrade mode that was not requested.

## Existing CVK building blocks and missing guarantees

| Existing component | Reuse | Required extension for remediation |
| --- | --- | --- |
| `IOSXEOperationalAction` / `FileRemove` | Immutable single-path operation, explicit device confirmation | Bind the parent preparation, device UID, approved plan and expected file content; retain old standalone behavior separately |
| Operational-action reconciler | gNOI dispatch, durable invocation marker, canonical mutation lease, maintenance callback | Revalidate eligibility at dispatch and independently verify absence/free space; distinguish uncertain RPC outcome from confirmed removal |
| Shared gNOI client | Existing file operations and authenticated device session | Qualify inventory/hash/removal support on the target platform without introducing a CC-owned device connection |
| Native software lifecycle backend | Install/image observation and preparation-retirement evidence | Produce the complete protected-reference set; current `Backend` is not a general cleanup planner |
| Manager/topology authority | Identity binding, maintenance policy, reservations and admission | Coordinate child preparation with SWIM; operational-action lease ownership alone is not a complete rollout grant |
| CC readiness client | Product binding, bounded results and task identity checks | Bind remediation outcomes and check applicability to the selected image/mode; preserve blocking behavior for unknown evidence |

Today `FileRemoveArgs` carries only `path`, and `runFileRemove` calls
`gc.Remove(ctx, path)`. It must not be treated as already implementing safe
image cleanup. New optional guarded semantics need protocol/capability checks
so older workers cannot accept a request and ignore its new preconditions.
The gNOI client already has `Stat`, `Get` and `Remove`, but successful `Stat`
does not qualify destructive `Remove` on the appliance. Its service-level
capability cache is not method-level qualification. Nor does a file metadata
read establish a complete flash/install/reference inventory: the current path
validator requires a file component, so root-directory enumeration through
`Stat("flash:")` is not an available shortcut. Use an optional platform
inventory capability where required, reusing the existing driver/session.

## Suggested manifest contract

Keep target intent small; place mandatory exclusions, allowed action types and
limits in an administrator-owned policy. The following is an **illustrative
future target fragment**, not a currently accepted Kubernetes API:

```yaml
deviceRef:
  name: cat9k-lab-103
remediation:
  policyRef:
    name: lab-swim-preparation
  execution: Direct
  allowedActions:
    - RefreshInventory
    - RemoveUnreferencedImageFiles
    - RecheckReadiness
  maxMutationAttempts: 1
```

`Direct` here explicitly selects the existing device preparation executor;
the parent upgrade remains pinned to `CatalystCenter`. Targets may narrow but
never expand the policy. Scope controller observation actions to the CC worker
even when cleanup uses Direct. Resolve and freeze policy UID/generation and
references server-side. Do not put guessed image filenames or unqualified CLI
commands into target manifests.

The resolved plan/status needs device/controller UIDs, image identity,
policy revision, complete inventory evidence, protected references, exact
candidate paths/member IDs/digests/sizes, estimated reclaimed bytes and the
required free-space threshold. After execution record actual reclaimed space,
the action UID/invocation or task receipt, fresh checks, and whether SWIM may
advance. Unknown outcome keeps the operation fenced for reconciliation.

The phase sequence is:

```text
Plan preparation -> Authorize child -> Execute and verify child
                 -> Fresh preflight -> Admit and execute SWIM
```

Do not let the parent hold the device lease while waiting for a child that
needs the same lease under another identity. Retain the manager's workflow
reservation; serialize each action through the existing device authority and
revalidate state when SWIM subsequently acquires admission. If continuous lease
ownership is required, implement an authenticated durable handoff rather than
a second lock or a shared arbitrary holder string. Any intervening mutation
invalidates the relevant preparation evidence.

## Implementation order and acceptance

1. Implement an observation-only preparation planner and `PlanOnly` report.
   Resolve receipt consumption/retirement and distinguish protected, eligible,
   unknown and policy-excluded files. An incomplete inventory produces no
   deletion authorization.
2. Add guarded exact-file removal to the existing device action path, with
   capability negotiation, manager-authorized immutable plan binding and fresh
   checks immediately before the one permitted invocation.
3. Add the CC operation's preparation-policy selection and generation-bound
   configuration prerequisites. Wire durable storage, reservations, action
   receipts and uncertainty recovery before enabling distribution.
4. Qualify normal-reload API settings and protocol-aware transfer checks;
   classify known non-applicable checks without suppressing general warnings.
5. Test protected-file rejection, changed digest/path, partial inventory,
   stale identity/policy, consumed versus live receipts, changed state between
   child and SWIM, lost deletion response, worker restart, and simultaneous
   Direct/SWIM requests. Verify no duplicate mutation or weakened retention.
6. Start physical execution on `.101` after actual transfer validation; its
   preceding readiness flash check passed. Resolve `.103` separately instead
   of enabling blanket cleanup across the fleet.

This design adds capability to the existing paths. It does not require
reworking ND or changing default Direct/gNOI upgrade behavior. Runtime support,
CRD fields and end-to-end SWIM remediation are still pending.

## Supervised recovery qualification, later on 8 October

The paused `.101` run established an additional requirement: readiness can use
cached controller filesystem inventory. After exact-file cleanup, native flash
free space increased from 2,032,721,920 to 3,281,846,272 bytes, but a newly
submitted readiness task still reported the old 1,938 MB value. Preparation
must therefore include a completed, device-correlated
[controller inventory synchronization](https://developer.cisco.com/docs/catalyst-center/sync-devices/)
between native cleanup verification and the next readiness submission.
A fresh task ID alone does not establish fresh underlying device inventory.

The SWIM journal now supports bounded readiness replacement after a new parent
grant, preserves previous receipts, rejects reused task IDs and retains the
existing unknown-outcome fence. This is not an automatic cleanup executor.
The lab cleanup was supervised: the exact obsolete 17.18.02 archive was copied
to Ubuntu16, matched to its recorded SHA-256 and size, compared against a fresh
native SHA-512, checked against boot/install/preparation state and removed once
while the rollout was paused and its canonical lease was current. Dispatch and
post-removal absence/free-space evidence were retained. No boot, package,
staged target or app archive was removed. See the updated live evidence for the
subsequent activation outcome.
