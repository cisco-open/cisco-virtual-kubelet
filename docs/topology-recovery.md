# Recover work and retire managed ownership

**For authorized operators and cluster administrators.** Choose the recovery task
before changing state. Downgrading the IOS-XE OS, abandoning an unused preparation,
retiring managed topology and transferring a device to another cluster are
separate operations with different authority and evidence requirements.

## Before any recovery

Inspect [campaign, leaf and worker evidence](topology-troubleshooting.md). Export
current UIDs, claims, receipts, policy/ledger identity, Events and redacted logs.
Settle uncertain physical mutations before releasing ownership. Do not delete or
recreate an authority object, alter an immutable leaf, erase a claim, or assume
that an expired timer makes a device safe. Use the matching runtime and CRDs;
[delegated roles](topology-reference.md#delegated-authorization) do not substitute
for the operation-specific checks below.

## Rollback and retirement

There are two distinct rollback procedures.

For a failed or undesired IOS-XE version, settle any ambiguous operation first,
then create and approve a new `IOSXESoftwareRollout` whose target version and
digest describe the qualified downgrade. Do not mutate or replay an old
campaign.

For managed-topology feature retirement:

An installation that was retired with an older chart must first be upgraded
once with the current chart and `topology.enabled=true`. Older instructions
allowed the completed `topology.cisco.vk/request-legacy-handoff` annotation to
be removed, and admission intentionally does not allow that authorization to
be recreated after `status.nodeIdentity` is gone. If every selected retired
device still has its completed request, leave those annotations in place. If
any selected retired device does not, use a temporary non-empty fleet selector
that matches none of the retired devices for this topology-enabled upgrade
(for example, require an absent
`topology.cisco.vk/retirement-migration-hold=selected` label), and verify the
selector result is empty before upgrading. This hold prevents an unintended
forward enrollment; without it, the device will be deliberately re-enrolled
and must complete another legacy handoff before topology can be disabled.

Wait for the controller rollout and its managed-admission preflight to
succeed; this installs and verifies the retained
`<release>-cisco-virtual-kubelet-legacy-node-marker` policy and binding that
protect released Nodes after the controller is disabled. Only then perform a
second Helm upgrade with `topology.enabled=false`, retaining the same policy
identity and temporary selector values. A direct transition from an older
retired release to the current disabled chart is rejected when retained
topology state exists but that guard is absent or stale. An installation that
has never created retained topology state does not require this migration.
The guard's digest annotation is an installation/version stamp used by Helm;
Helm does not recompute the live policy Spec. The exact server-stored Spec was
already hashed and checked by the topology-enabled manager preflight.

1. stop creating campaigns and request an effective pause;
2. resolve every claimed mutation and wait for all reservations, leaves, and
   maintenance sessions to settle;
3. export campaign, leaf, policy, ledger, Event, and redacted log evidence;
4. while `topology.enabled=true`, set
   `topology.cisco.vk/request-legacy-handoff` on each managed CiscoDevice to
   that object's current `status.nodeIdentity.nodeUID`, using an identity with
   the delegated `topology` verb;
5. wait for `status.legacyHandoff.phase=Complete` on every requested device and
   verify `status.nodeIdentity` is absent, the Node carries the matching
   `topology.cisco.vk/legacy-handoff=<node UID>` audit marker, the configured
   namespace-shared legacy worker is Ready with a heartbeat later than
   `isolatedReadyAt`, and the temporary UID-scoped ServiceAccount and bindings
   are absent;
6. run a live Helm upgrade with the same release, policy namespace/name, and
   ledger name, keeping `controller.leaderElect=true` and
   `aggregator.enabled=false`, then set `topology.enabled=false`; and
7. verify the two functional accounts and all managed profile bindings are
   removed. The controller has already restored the configured legacy account
   for topology-disabled compatibility; it is not a managed-topology identity.

A retained `Prepared` leaf is intentionally not idle ownership even when its
manager admission is `Settled`: its receipt reserves that exact device and
installed image for a later separately authorized activation. Reverse handoff
and managed device deletion therefore remain blocked while any exact managed
leaf is `Prepared` or carries a preparation receipt. Complete its approved
activation while the managed owner is intact, or retain managed ownership. Do
not copy the receipt/approval to another owner or delete the leaf as an
invalidation shortcut. The explicit cancellation/recovery path below can retire
an unused preparation after native verification; it cannot clear uncertain
mutation outcomes.

## Abandoning an unused preparation

This recovery contract, introduced in `v2026.11.0`, has a
[physical single-supervisor C9300 retirement result](evidence/topology-2026-10-05/live-migration-and-recovery.md#successful-native-recovery-after-replacement),
including runtime replacement and immutable receipt preservation. It is **not
full production qualification**: fresh re-preparation and no-replay recovery
also passed in that follow-up, but image drift, both image directions and
verified worker RESTCONF transport remain separate acceptance requirements.
It applies only to a completed, settled `PrepareOnly` campaign that has never
received `activationApproval`. It does not support dual-supervisor recovery,
unresolved install/activation outcomes, or a running OS changed since preparation.
It neither removes software nor changes boot settings, certificates, workload
placement, or app-hosting configuration.

For retirement after a settled workload drain, install the matching CRDs before
the manager and network workers. Older binaries cannot authorize this case;
they retain the receipt rather than rewriting the settled drain. Do not roll
back to a schema that rejects an already recorded retirement authority. Keep
the complete original drain and preparation audit when upgrading.

1. As an operator with `control` permission, cancel the campaign using a new
   control revision, your authenticated `requestedBy`, and current UTC
   `requestedAt`. Wait for the campaign to reach `Cancelled` and the network
   worker to acknowledge that exact control revision with `Settled` state.
   An already-settled workload drain is an exception: its original control,
   admission, drain session and worker acknowledgement remain unchanged. The
   manager validates the cancelled parent and complete drain evidence, then
   publishes separate, observation-only retirement authority carrying the
   newer parent revision. Do not manually advance the retained leaf revision.
2. Review the retained leaf's `preparedReceipt`, its exact device/upgrade UIDs,
   hash and the campaign's `status.frozenPlan.hash`. Do not regenerate a hash
   or copy a receipt from another object. Retain the original objects for audit.
3. A separately authorized operator adds `spec.preparationInvalidation` once.
   Bind the chart's `*-rollout-recoverer` ClusterRole through a namespaced
   RoleBinding. Neither worker account, planner nor activation approver gets
   the custom `recover` permission implicitly. A recoverer does not receive
   `control`, `approve`, or `activate` permission from this role.

For example, after filling **all** placeholders in `invalidation.json` from
the live objects and the authenticated operator identity:

```json
{
  "spec": {
    "preparationInvalidation": {
      "planHash": "<exact status.frozenPlan.hash>",
      "requestedBy": "<authenticated Kubernetes username>",
      "requestedAt": "<current RFC3339 UTC timestamp>",
      "reason": "Abandon unused preparation before replanning",
      "receipts": [{
        "deviceUID": "<preparedReceipt.deviceUID>",
        "upgradeUID": "<preparedReceipt.upgradeUID>",
        "receiptHash": "<preparedReceipt.receiptHash>"
      }]
    }
  }
}
```

```sh
kubectl -n network-devices get iosxesoftwareupgrade PREPARED_LEAF -o yaml
kubectl -n network-devices patch iosxesoftwarerollout CAMPAIGN \
  --type=merge --patch-file=invalidation.json
kubectl -n network-devices get iosxesoftwareupgrade PREPARED_LEAF -w
kubectl -n network-devices wait iosxesoftwarerollout/CAMPAIGN \
  --for=condition=PreparationInvalidated --timeout=5m
```

The request may name an exact subset of the cancelled campaign's prepared
targets. The manager validates every requested reference before publishing
per-leaf authority. Each bound network worker acquires the normal device-wide
Lease, reads native install inventory and gNOI `OS.Verify`, and checks:

- the installer is quiescent at every reported location;
- the original running version remains committed and `OS.Verify` agrees;
- the exact inactive target is installed or absent, not active, unknown or
  in progress; and
- cancellation, settlement, receipt identity and worker binding still match.

Only then can the leaf become `PreparedInvalidated`, retaining the immutable
receipt and appending actor/request binding, observation time, native-evidence
hash, running version and worker provenance. Manager, worker queue, and device
handoff checks use the same release predicate. The campaign remains
`Cancelled`; `PreparationInvalidated=True` covers only its requested receipts.
A request or phase string alone does not release ownership. Old activation
intent cannot reuse an invalidated receipt. A new campaign needs fresh planning,
preparation and approval.

On missing/ambiguous inventory, live binding drift, an unavailable observation
capability, or a foreign Lease, keep ownership and inspect worker logs. IOS XE
inventory that remains `in-progress` is not sufficient by itself. The IOS XE
adapter can corroborate that exact target only when the original immutable
receipt's positive source size and installation interval match one completed
native add, every target package is added, the source package is verified, all
install locations are idle, and the original running image remains committed.
It requires the native response clock to map that original interval; the later
recovery request does not extend the permitted history window. Missing,
ambiguous, later or unresolved native activity keeps ownership held. This
normalizes observation proof only; it does not modify device inventory or
authorize image reuse/activation. A repeated IOS XE add may leave the exact
`gNOI_iosxe_.bin` source placeholder; corroboration additionally requires one
exact version-bound, added IMG package at every location before applying the
same size/history/clock checks. Arbitrary or absent filenames remain blocked.
Full recovery qualification is still required for the release cohort, including
verification of the separate RESTCONF server identity. Do not force-edit status,
erase claims, remove finalizers, clear Leases, or delete packages to bypass it.
The request and evidence are append-only; schema/runtime rollback must preserve
them. Older writers that omit them are rejected by the current schema.

One exact activation consumes that queue ownership only after it is terminal
`Succeeded`, verifies the prepared target version, records a conclusively
settled device mutation, and settles the same manager/device/Node/campaign/
policy/plan bindings. The Prepared object and receipt stay append-only for
audit, but a later campaign may then acquire the device queue. The manager and
provider use the same predicate; any missing completion, condition or identity
binding continues to block.

## Complete the reverse writer handoff

The following checks and annotation belong to retiring managed topology, after
the inventory and operation settlement above. They do not authorize an OS change.

The same fail-closed rule applies to `StagedForNextBoot` and to every
non-terminal leaf, even if a contradictory historical object says manager
admission is `Settled`. Resolve and independently verify that device-side state
before requesting handoff; settlement alone must not reinterpret an uncertain
or future boot mutation as terminal.

If workload drain was ever enabled, include each retained cleanup Role/Binding
in the evidence export. After all claimed work and maintenance sessions settle,
verify that its namespace has no reserved drain marker/finalizer. Follow the
[drain cleanup checks and exact-pair removal](topology-drain.md#4-disable-drain-only-after-cleanup)
before disabling managed topology; do not delete unrelated namespace RBAC.

For example, repeat this for each managed device and do not remove the request
while topology remains enabled—the selected device would otherwise begin a new
forward enrollment:

```bash
DEVICE_NAMESPACE=network-devices
DEVICE_NAME=edge-01
NODE_UID="$(kubectl get ciscodevice "$DEVICE_NAME" \
  --namespace "$DEVICE_NAMESPACE" \
  -o jsonpath='{.status.nodeIdentity.nodeUID}')"
kubectl annotate ciscodevice "$DEVICE_NAME" \
  --namespace "$DEVICE_NAMESPACE" \
  topology.cisco.vk/request-legacy-handoff="$NODE_UID" --overwrite
kubectl wait ciscodevice "$DEVICE_NAME" \
  --namespace "$DEVICE_NAMESPACE" \
  --for='jsonpath={.status.legacyHandoff.phase}=Complete' --timeout=30m
```

The controller must complete its exact UID-bound writer transfer. The
[reverse-handoff reference](topology-reference.md#reverse-handoff-state-contract)
explains crash recovery, temporary identities and durable phases. Do not remove
the request or temporary binding early; the completion checks in this procedure
are required even if a replacement Pod appears Ready.

## Offline transfer to another cluster

Cross-cluster transfer is an offline ownership move, not active/active
failover. A Lease in one cluster cannot fence a writer in another cluster.
Before the destination receives device credentials or creates a CiscoDevice:

1. pause source-cluster campaigns and prove there is no topology lock, ledger
   reservation, unsettled maintenance session, claimed operation, or retained
   `Prepared` receipt for the device;
2. complete the UID-bound reverse handoff above, then delete the source
   CiscoDevice through its normal finalizer and verify its worker Deployments,
   Pods, per-device Leases, generated bindings and Node are absent;
3. remove the source credential Secret or rotate/revoke the device credential
   and trust material so a retained source-cluster copy cannot authenticate;
4. retain the exported device UID, Node UID, handoff status, ledger and
   operation history as audit evidence, but do not import approvals, claims,
   receipts, Leases or Kubernetes UIDs into the destination; and
5. only after source revocation is independently verified, create a fresh
   destination Secret/CiscoDevice and require new read-only discovery, exact
   physical identity, topology projection and worker-binding health before any
   mutation is approved.

Exercise a negative old-writer request after revocation and a positive
destination observation before declaring the move complete. If the source
cluster is unreachable and the device credential cannot be rotated or revoked,
the transfer is blocked. Cross-cluster Lease expiry, copied Kubernetes state,
or merely scaling the source controller to zero is not sufficient fencing.
Automated multi-cluster failover remains out of scope.

An interrupted upgrade from the earlier per-device model has one additional
recovery state: exact generated legacy RBAC can exist before its UID marker.
If topology is disabled in that window, the retained boundary detects the
cluster binding; the disabled manager audits the complete ServiceAccount and
bindings, writes the exact CiscoDevice-UID marker, and keeps that isolated
worker rather than guessing that the namespace-shared legacy identity is safe.
This is a supported fail-closed recovery state, not a completed retirement. To
remove it, re-enable topology, let forward enrollment prove the shared managed
worker and retire the generated identity/marker, then perform the normal
reverse handoff above before disabling topology again. Do not delete the marker
or any one binding by hand.

The live downgrade check rejects missing or UID-mismatched policy/ledger
objects, incomplete manager RBAC, any remaining `status.nodeIdentity`, and any
handoff phase other than `Complete`. The policy, ledger, versioned policy/binding
pairs, fixed functional profile roles, and supplemental manager role/binding carry
`helm.sh/resource-policy: keep`. Setting `topology.enabled=false` or
uninstalling Helm does not remove them. In-flight handoff workers and retained
manager-owned audit state still rely on native admission, so these objects are
an active safety boundary, not stale installation debris. Remove them only
after deleting every dependent split worker, functional ServiceAccount/binding,
Node marker, and durable device state.
Never delete/recreate only the ledger or a same-name CiscoDevice to clear a
failure; both changes produce new UIDs and invalidate durable authority.

Offline `helm template` and GitOps pruning cannot perform or prove this
retirement because Helm `lookup` has no live objects to inspect. Use the live
upgrade above. If an existing GitOps system must remain active during the
transition, exclude existing CVK RBAC and retained topology objects from prune
until the controller-reported handoff is complete and the live Helm check has
succeeded.

## Verify completion

For an OS downgrade, verify the new approved campaign's conclusive device result,
post-mutation health and settlement. For unused preparation, require native
verification and recorded `PreparedInvalidated` authority while preserving the
receipt. For topology retirement, require `legacyHandoff.phase=Complete`, the
expected legacy writer and audit marker, and removal of managed bindings through
the supported live transition. For a cluster transfer, independently prove the
source can no longer authenticate before enabling the destination.

If any proof is unavailable, retain the relevant guards and investigate through
[observation](topology-troubleshooting.md). Re-enroll through the
[setup guide](topology-enable.md) only after the prior ownership has conclusively
ended.
