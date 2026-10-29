# Run IOS-XE operating-system campaigns

**For device operators, planners and approvers.** An `IOSXESoftwareRollout` changes
the device operating system through manager-created `IOSXESoftwareUpgrade` leaves.
It is separate from scheduling application Pods and is currently IOS-XE-specific.

## Before creating a campaign

- Complete [managed enrollment](topology-enable.md), with current device/Node
  identity, topology and worker-revision observations.
- Use the matching reviewed `v2026.11.0` CRDs, admission policies, chart, manager
  and workers. The staged activation, network-evidence and recovery extensions
  are introduced in November; update October installations before using them.
  Check [release records](releases/index.md) for artifact availability.
- Obtain the separately delegated planner and approver roles described in the
  [authorization reference](topology-reference.md#delegated-authorization).
- Qualify the exact image/hardware cohort, device service behavior and recovery
  path. An accepted topology label or heartbeat does not prove network continuity.
- Keep `BlockIfRunning` unless the separate [PDB-aware drain procedure](topology-drain.md)
  has been deliberately enabled and qualified for these workloads.

The sequence is: enable the mutation profile, configure trusted image sources,
review the plan and its optional gates, obtain exact-plan approval, then observe
execution and settlement. Preparation without activation has a separate approval
boundary described below.

## 1. Enable OS mutation { #topology-aware-ios-xe-software-campaigns }

Enable the existing device-side upgrade executor separately:

```yaml
gnoi:
  disabled: false
  enableSoftwareUpgrade: true
```

`topology.enabled` never grants software-mutation authority by implication.
The rollout controller and its manager/worker mutation RBAC are inactive until
both gNOI gates above are enabled. Read-only observation of retained leaves
continues after disablement so an unsettled operation remains fenced.
Keep `gnoi.enableWriteClass=false`: the managed Phase 2 coordinator rejects
generic `IOSXEOperationalAction` because those operations do not yet implement
the campaign reservation, claim, and maintenance-session contract.
On an enrolled managed device, direct or unmarked
`IOSXESoftwareUpgrade` objects are ignored. Submit an
`IOSXESoftwareRollout`; the manager creates retained, immutable,
UID/reservation-bound leaves only after admission succeeds.

### Secure gNOI and image-source prerequisites

Complete the IOS-XE secure gNXI configuration, password-metadata or mTLS
trust, and gNOI OS provisioning described in
[IOS-XE upgrade and downgrade runbook](gnoi-iosxe-upgrade-runbook.md) and
[gNOI software lifecycle design](gnoi-software-lifecycle.md). Managed topology
does not weaken or replace those controls.

For a dedicated verified gNOI trust bundle, the Kubernetes object uses a
same-namespace Secret:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: c9k-a-gnoi-tls
  namespace: network-devices
type: Opaque
data:
  ca.crt: BASE64_CA_PEM
  tls.crt: BASE64_OPTIONAL_CLIENT_CERT_PEM
  tls.key: BASE64_OPTIONAL_CLIENT_PRIVATE_KEY_PEM
---
apiVersion: cisco.vk/v1alpha1
kind: CiscoDevice
metadata:
  name: c9k-a
  namespace: network-devices
spec:
  # ...normal device fields...
  gnoi:
    port: 9339
    transportSecurity: tls
    tls:
      secretRef:
        name: c9k-a-gnoi-tls
```

`ca.crt` is required; `tls.crt` and `tls.key` must be supplied together when
mutual TLS is used. Keep private keys in Secrets, never ConfigMaps or rollout
specs.

The managed campaign API pins one image digest and family and accepts up to 16
named existing artifact endpoints. Each endpoint is either:

- anonymous HTTPS with verified TLS and no URL credentials; or
- SFTP with an endpoint-bound Secret and verified `knownHosts` data.

Scoped endpoints select protected `CiscoDevice.metadata.labels`; every
selector key must be listed in administrator `requiredTopologyKeys`. A
`distribution.cisco.vk/cache-domain` label is the recommended source-locality
key. It is protected by admission and may define a transfer-budget domain, but
cannot be projected to a Node. The lowest numeric priority among matching
scoped endpoints wins. Equal-priority matches fail planning. Exactly one unscoped catch-all
is required and is considered only when no scoped endpoint matches. Every
endpoint serves the single campaign-wide SHA-256 identity; CVK does not
silently fail over after approval.

`IOSXESoftwareRollout` remains a `v1alpha1` API. The published October
baseline uses `plan.image.sources` and per-target frozen source snapshots;
the earlier development preview used `plan.source`. There is no conversion
webhook between those shapes. A cluster that ran that older preview must first
settle and export its rollout evidence, remove the obsolete preview objects,
and upgrade the CRD and controller together. Do not deploy an older controller
over stored objects whose source schema it cannot interpret. The staged,
network-evidence and recovery contracts introduced in `v2026.11.0` also require
matching CRDs, admission policies, manager and workers.

Managed source resolution rejects redirects, environment proxies, URL
userinfo, queries/fragments, unsafe or mixed DNS answers, loopback,
link-local, multicast, unspecified, metadata, and other special-use
destinations. Private-address HTTPS is rejected. Private SFTP is allowed only
with an endpoint-bound Secret. This stricter policy applies to manager-created
managed leaves; standalone legacy leaf behavior remains compatible.

An SFTP Secret has this shape:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: site-a-image-source
  namespace: network-devices
  labels:
    cisco.vk/purpose: software-image-source
type: Opaque
stringData:
  allowedScheme: sftp
  allowedHost: images.site-a.example.net
  allowedPort: "22"
  username: image-reader
  privateKey: |
    -----BEGIN OPENSSH PRIVATE KEY-----
    REPLACE_ME
    -----END OPENSSH PRIVATE KEY-----
  knownHosts: |
    images.site-a.example.net ssh-ed25519 REPLACE_ME
```

The manager freezes each target's selected source, priority, URL, digest, and
Secret UID into the plan and managed leaf. In-place credential rotation under
that UID is allowed, but every use revalidates source purpose, endpoint, and
trust policy. Delete/recreate changes the UID, fences new claims, and requires
a newly frozen and approved rollout. Transient resolution failures retry only
that frozen endpoint within the install deadline. A permanent failure or
expired deadline fails the leaf and fences further campaign admission; it
never falls through to a different mirror under the approved hash.

## 2. Review disruption, risk and network gates

These controls apply before approval. Keep their operational constraints with the
plan; they are not implied by Kubernetes application-placement rules.

Administrator policy may also define up to 16 `disruptionProtections`. Each
rule has a DNS-label name, a reason (`CriticalService` or `SingletonPath`) and
a non-empty Kubernetes label selector whose keys are included in
`requiredTopologyKeys`. These rules are prohibitions, not hints: a matching
CiscoDevice cannot enter the current combined software lifecycle. The manager
checks them while freezing the plan and again from current protected labels
before admission. Adding or changing a rule changes both policy hashes and
requires re-planning; already-claimed physical work remains under observation
instead of being abandoned. The rule also blocks image preparation; `PrepareOnly` does not bypass
administrator disruption protections. Never infer redundancy merely
from a label—use qualified path and service evidence before removing a
singleton or critical-service protection.

Administrator policy may additionally define up to 16 `riskGroups`. Each
group has a DNS-label name, a non-empty Kubernetes selector restricted to
`requiredTopologyKeys`, `maxConcurrentTransfers`, and `maxUnavailable`. It may
also set `maxAggregateTransferBytesPerSecond` for a known shared distribution
or forwarding path. A
device may match multiple groups. The frozen plan records each target's sorted
groups and a SHA-256 digest of the complete relevant physical membership.
Before admission, the manager recomputes that membership and requires an exact
match. In the same ledger compare-and-swap used for global/domain budgets, it
counts unhealthy non-target members and reservations from every campaign
against every applicable group. All groups must pass. Changing a selector,
budget, or membership requires a new plan and approval. Risk groups are
explicit operator constraints; they do not prove that a forwarding path is
redundant or healthy.

For each matching group with an aggregate byte ceiling, the manager derives a
conservative per-transfer share by dividing that ceiling by the group's
`maxConcurrentTransfers`. The lowest share across overlapping groups is frozen
in the approved target and copied into the immutable leaf. The worker enforces
that value independently while materializing remote image bytes and while
streaming the verified image through gNOI OS.Install. Cache hits skip the
source-network segment but never the device segment. A resolver that cannot
enforce an opted-in source limit is rejected before install; an omitted limit
preserves existing behavior. An ordinary paced leaf requires the
`rollout-byte-pacing-v1` manager/worker admission protocol, which makes a
rolling deployment fail closed when an older worker does not understand the
new rate field. The ceiling applies only to CVK-owned traffic and does not
replace measured headroom or service-path validation. Ensure the install
timeout and maintenance window accommodate the paced image size plus device
validation and activation.

To opt into the network gate, require complete evidence and name the exact
interfaces and adjacencies that must be healthy:

```yaml
spec:
  plan:
    strategy: Reload # NoReboot is a combined strategy, not a proven prepare-only hold
    health:
      network:
        enabled: true
        requireCompleteEvidence: true
        requiredInterfaces: [GigabitEthernet1/0/1]
        requiredNeighbors: [core-a]
        requireInterfacesUp: true
        requireNeighborsFull: true
```

The network-management worker publishes only a bounded producer sample under
`CiscoDevice.status.healthObservation.network`; it does not publish raw CLI,
credentials, or arbitrary topology. That raw field is not rollout authority.
The manager validates the exact device incarnation, physical-identity hash,
current worker revision and Pod UID, positive sequence, original collection
interval and duplicate-free interface/adjacency identities. Only then does it
copy the sample to the manager-owned
`CiscoDevice.status.healthObservation.acceptedNetwork`. Native admission lets
the worker change the raw field but rejects worker changes to the accepted
field. Rollouts consume only the accepted copy and calculate freshness from
the start of collection, so a late status write or manager restart cannot
refresh old evidence.

Missing, unaccepted or stale evidence blocks the rollout with a reason such as
`EvidenceMissing`, `EvidenceStale`, `AlternatePathUnavailable`, or
`DeviceIdentityMismatch`. The gate is nil by default, so existing campaigns
retain their behavior. Physical idle-state CLI comparison is recorded, but
loaded directional-rate accuracy and redundant-supervisor health remain
unqualified until their declared E02 test fixtures exist; these signals must
not be inferred from labels or an unrelated successful device read.

## 3. Check runtime compatibility

Before replacing controllers, settle active older staging **and** network-gated
campaigns using their compatible runtime. Do not edit immutable leaves or
protocols, erase claims, or assume an arbitrary Helm rollback is safe. Apply
new CRDs before the matching manager/workers, with new campaigns paused.
The exact released-manager protocol check, actual released-manager Pod's
rejection of the newer native admission contract, and stored neighbor
map-to-atomic migration now pass. Interrupted deployment and the wider
reverse-manager/rollback matrix remain merge gates; see the
[compatibility follow-up](evidence/topology-2026-10-02/migration-and-diagnostic-followup.md).

The [protocol reference](topology-reference.md#campaign-protocol-versions) explains
the staged, network-evidence and pacing contracts. Settle old claimed work with
its compatible runtime before replacing it; a chart rollback is not a recovery
shortcut.

## 4. Submit, review, and approve { #submit-review-and-approve }

Start from
[`examples/topology/iosxe-software-rollout.yaml`](https://github.com/cisco-open/cisco-virtual-kubelet/blob/v2026.11.0/examples/topology/iosxe-software-rollout.yaml).
Create a new object for every upgrade or downgrade; executable intent is
immutable. A downgrade is the same workflow with a qualified lower
`targetVersion` and its exact compatible image/digest.

The creator must have the deliberately unbound rollout-planner role, and
`spec.plan.requestedBy` must exactly equal the authenticated Kubernetes
username. A campaign must start with neutral `spec.control.revision: 0`.

Edit and review the example locally, replacing its identities, image digest,
sources, targets, cohorts and time fields with the intended values. Then submit
that reviewed manifest using the planner's Kubernetes credentials:

```bash
kubectl apply -f examples/topology/iosxe-software-rollout.yaml
```

The observation and approval examples below assume the manifest keeps the name
`cat9k-17-18-4` in `network-devices`; substitute the exact chosen coordinates if
you renamed it. Submission creates the plan, not permission to mutate the device.

After creation, wait for `AwaitingApproval` and review the complete frozen
target/source/policy snapshot:

```bash
kubectl get iosxesoftwarerollout cat9k-17-18-4 \
  -n network-devices -o yaml

PLAN_HASH="$(kubectl get iosxesoftwarerollout cat9k-17-18-4 \
  -n network-devices -o jsonpath='{.status.frozenPlan.hash}')"
printf '%s\n' "$PLAN_HASH"
```

Review the qualification cohorts as part of that approval. Every selected
device must carry a protected
`operations.cisco.vk/qualification-cohort=<hardware-capability-class>` label.
`spec.plan.canaries` must contain one entry whose `name` exactly equals each
distinct cohort value, with at least one explicit target device in that entry.
The manager rejects missing cohorts, mismatched membership, duplicate canary
assignments, and a cohort without a canary. It freezes and revalidates the
cohort on every target, so later device-label or generation drift requires a
new plan. This prevents one successful hardware class from implicitly
qualifying a different class; incompatible hardware/image combinations need
separate qualified images or separate campaigns. The label is an
administrator assertion backed by lab evidence, not hardware auto-discovery.

Approval is append-only and authorizes only that exact hash. The approver must
have the separately delegated rollout-approver role, and `approvedBy` must
match that request's authenticated identity:

```bash
APPROVER="$(kubectl auth whoami -o jsonpath='{.status.userInfo.username}')"
APPROVED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

kubectl patch iosxesoftwarerollout cat9k-17-18-4 \
  -n network-devices --type=merge \
  -p "{\"spec\":{\"approval\":{\"planHash\":\"${PLAN_HASH}\",\"approvedBy\":\"${APPROVER}\",\"approvedAt\":\"${APPROVED_AT}\"}}}"
```

Do not copy the approval patch under a different identity. Approval is bound to
the live frozen hash and authenticated approver. Changed target identity or an
incompatible policy requires a new plan; compatible policy edits can only tighten
active limits. See [policy transitions](topology-reference.md#policy-and-plan-transitions)
for the exact rules. Never edit a frozen plan or immutable child to repair drift.

## Separate preparation from activation

For a qualified staged workflow, start from the
[`PrepareOnly` campaign example](https://github.com/cisco-open/cisco-virtual-kubelet/blob/v2026.11.0/examples/topology/iosxe-software-prepare.yaml).
Replace every placeholder and use the normal planning and approval process above.
`PrepareOnly` does not authorize activation: it retains immutable preparation
receipts and device ownership.

Only after every frozen target has completed preparation, an operator with the
separate `activate` permission may construct the
[activation approval patch](https://github.com/cisco-open/cisco-virtual-kubelet/blob/v2026.11.0/examples/topology/iosxe-software-activate-patch.yaml)
from the exact live plan, device/leaf UIDs and receipt hashes. Use its server-side
dry run before applying the append-only approval, within a qualified maintenance
window. Do not invent or copy receipt identities. See the
[staged lifecycle reference](gnoi-software-lifecycle.md#upgrade-examples) for the
contract and [unused-preparation recovery](topology-recovery.md#abandoning-an-unused-preparation)
if activation will not proceed. A retained receipt blocks ownership retirement.

## 5. Observe admission and execution { #admission-and-execution-behavior }

For each target, the manager checks:

- same namespace and frozen CiscoDevice UID and generation;
- driver `XE`, image-family match, unique declared physical identity, exact
  Node UID, projection hash, and worker protocol;
- deterministic source selection still resolves to the exact frozen endpoint
  and Secret incarnation;
- no administrator `CriticalService` or `SingletonPath` disruption protection
  matches the current protected device labels;
- fresh device and Node health, including current producer observations for
  `NodeIdentityReady`, `TopologyReady`, and `GNOIConfigurationReady`;
- the selected workload policy: `BlockIfRunning` requires no live Kubernetes
  or device workload, while `Drain` requires the exact bounded eligibility,
  PDB, session, teardown, replacement-readiness, and inventory proofs in the [drain procedure](topology-drain.md);
- global and every independent domain transfer/unavailability ceiling;
- every matching administrator risk-group transfer/unavailability ceiling,
  including non-target health and reservations from other campaigns;
- the strictest frozen risk-group byte-rate share on every applicable image
  transfer segment;
- existing unhealthy or maintained fleet members, including non-targets; and
- no conflicting device mutation.

Campaign health is intentionally narrower than end-to-end network assurance.
Baseline gates prove a fresh bound Node `Ready` heartbeat, manager-owned
identity/topology/gNOI configuration observations and the leaf lifecycle result.
The optional network gate above additionally requires its exact accepted
interface/adjacency evidence. Neither establishes production packet forwarding,
loaded directional-rate accuracy or complete stack/supervisor health. Qualify
those operational requirements independently; absent evidence is not a pass.

The final worker-side workload check is fail closed. Under `BlockIfRunning`,
terminating Kubernetes Pods still block; retained `Succeeded` or `Failed` Pods
alone do not. Any live app or orphan returned by the device driver blocks, and
a missing callback or inventory error blocks rather than assuming the device
is empty. Under `Drain`, only an exact selected teardown in the active session
may run, and its complete post-delete device inventory becomes durable worker
evidence before promotion. The provider coordination contract is
driver-neutral for future platforms, although campaign registration and the
software lifecycle remain explicitly IOS XE only.

Campaign phases are `AwaitingApproval`, `Paused`, `Executing`, `Soaking`,
`Cancelling`, `Succeeded`, `Failed`, and `Cancelled`. The explicitly frozen
canary from every qualification cohort is wave zero; non-canaries are wave one
and remain ineligible until every cohort's canary has settled its
post-operation checks. Budgets still serialize admission within a wave. A
completed leaf is not settled immediately: its effective manager-authenticated
health time, including the snapshotted Node heartbeat and required producer
observations, must be strictly later than leaf completion. A newer live
heartbeat alone cannot satisfy this gate. The device and Node must then remain
continuously healthy for the configured canary or wave soak interval. A stale
or unhealthy observation resets progress rather than consuming an elapsed
timer. `status.counts.cancelled` distinguishes targets fenced by cancellation
from failed targets.

Before a new device RPC, the worker commits an append-only, stage-specific
mutation claim bound to the current reservation, policy epoch, and control
revision. It must first publish a worker-control acknowledgement of that same
epoch and grant. An ambiguous install, activation, or rollback result keeps the
reservation and pauses safe progression until an operator reconciles physical
state.

`rollbackOnFailure: true` is the existing limited leaf attempt to reactivate
the previously observed version after post-verify failure. It is not a
fleet-wide transaction and is not a substitute for a separately planned
downgrade campaign.

The [health and reservation reference](topology-reference.md#health-and-reservation-evidence)
explains freshness, accepted observations and claims. A timer or Lease expiry
never proves that an accepted device mutation completed.

## 6. Pause, resume, or cancel { #pause-resume-and-cancel }

Only the rollout-planner role carries the custom `control` permission. Every
change increments `revision` and binds `requestedBy` to the authenticated
caller. Example pause:

```bash
REQUESTER="$(kubectl auth whoami -o jsonpath='{.status.userInfo.username}')"
REQUESTED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

kubectl patch iosxesoftwarerollout cat9k-17-18-4 \
  -n network-devices --type=merge \
  -p "{\"spec\":{\"control\":{\"revision\":1,\"pause\":true,\"requestedBy\":\"${REQUESTER}\",\"requestedAt\":\"${REQUESTED_AT}\",\"reason\":\"change freeze\"}}}"
```

Resume with a larger revision and `pause:false`. Cancel with a larger revision
and `cancel:true`; cancellation is terminal. Pause/cancel prevents future
claims but cannot undo an RPC already accepted by the switch. Inspect
`status.control.requestedRevision`, `effectiveRevision`, `pausePending`, and
`cancellationPending` rather than assuming a patch is immediately effective.

## Verify the outcome and continue

Use [campaign and leaf observation](topology-troubleshooting.md) to confirm the
requested control became effective, each device outcome is conclusive, current
post-mutation health and the required continuous soak passed, and reservations
settled. A successful transfer alone is not a completed upgrade. For preparation,
retain the receipts and ownership until separately approved activation or verified
invalidation; do not expect a prepared device to become unowned.

For an uncertain result, preserve evidence and use the
[recovery guide](topology-recovery.md). A planned downgrade requires a new approved
campaign; it is not a rewrite or replay of the previous one.
