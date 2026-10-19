# SDA transit journal, permit and acknowledged recovery

This phase connects the [native SDA stage](sda-transit-execution.md) to CVK's
existing owner/journal CAS, physical group identity, actual all-member submission
permit and retained task recovery. It supersedes that phase's missing journal/
permit bridge and acknowledged recovery routing. It does not activate new SDA
manifest submission or supply device convergence and reservation release.

## Durable operation and submission

A new global transit has no product UUID before creation. The logical target is
its exact native dependency fingerprint; parent is empty. A strict versioned
resource decoder binds that target to the resource, action and complete CP group.
Only this resource family receives the explicit global-target preparation rule,
using the existing global physical operation pattern. No product UUID, native
creator identity or new schema is fabricated.

Preparation checks the declared source, controller/config revision, complete
native and managed identities and existing section owner. Shared preparation
atomically retains owner plus Prepared operation and rereads the full group.
Pruned/malformed/empty/missing groups, wrong actions/targets, undeclared requests
and foreign owners refuse without a product request. Pruned retained operations
remain fenced rather than enter legacy logical-resource completion.

Dispatch reconstructs the same source-bound plan and requires the real shared
`GroupSubmissionPermit`, including its complete member claim/grant/lease checks.
Native and managed evidence is rechecked around the exact Submitting journal and
one-use dispatch proof. The existing native sender is invoked once. The task is
recorded through that proof; a known task is returned even when durable
acknowledgement fails. Missing acknowledgement remains uncertain, without retry.
No reservation, lease protocol, transport or parallel journal is introduced.

## Recovery and completion boundary

Owned observation brackets the original root/task tree and native readback with
source, owner, exact operation, complete group and repeated managed/native state.
Accepted and Unknown **with an acknowledged task** can recover to Verifying via
`BeginGroupVerification`. Taskless Unknown, failed/running children, missing
readback, source or owner changes and admission loss do not advance or replay.

The public retained-operation reconciler now routes acknowledged SDA actions
through this observer. Repeated Verifying passes recheck original evidence and
report `GroupConvergenceVerificationUnavailable`. The operation and all CP
reservations remain retained; there is no final receipt, Verified transition or
release. Automatic Prepared submission and new-intent compilation remain gated.
This is execution recovery evidence, not exclusive creator ownership.

## Verification

- [Race regression](sda-transit-journal-regression.log): 73 SDA subcases (the prior
  48 plus nine preparation and sixteen permit/recovery cases), a separate public
  taskless-Unknown case, and adjacent transit/fabric/group regressions pass in
  11.027 seconds. The permit cases use actual shared group claim/grant/lease
  machinery for two CPs; native responses and workload/actor admission are
  explicitly simulated. They cover source/owner/Node drift, permit revocation
  before/after POST, lost reply, known-task retention, complete child tasks,
  public Accepted/Unknown restart recovery and the retained convergence fence.
- [Two real private Kubernetes API cases](sda-transit-journal-api.log) pass:
  persisted owner/logical target/full group and ConfigMap UID/resourceVersion,
  late source refusal and unchanged retained journal; plus deliberate group-field
  dropping by the test writer, proving pruned-readback refusal and no legacy
  finish. This uses the current real schema, not an older deployed CRD. No
  product request or installed worker is involved.
- [Foundations](sda-transit-journal-foundations.log): shared controller/group, ND,
  software lifecycle, upgrade provider and direct GNOI checks pass, with cached
  results for unchanged packages.
- [Authenticated lab readback](sda-transit-journal-native-prerequisites.log)
  confirms no wired-provisioned records on `.100`, `.101` or `.103`; reads
  succeed and zero product writes are counted. No native SDA creation or
  convergence is claimed, and no provisioning/repair is attempted.
- [Final source/upstream fingerprints](sda-transit-journal-sources.json) and
  [strict documentation build](sda-transit-journal-docs.log).

## Still required for public SDA execution

Independent device convergence, final durable receipt and group settlement/
release remain mandatory before new-intent activation. Complete mixed-model
compilation/preflight and provisioning sequencing, inventory alias/FQDN mapping,
new public preparation and automatic Prepared dispatch are still pending.
Existing-transit updates need complete old/new CP and connected-consumer
footprints. Native creator/adoption/replacement/delete and installed actors
remain unqualified. Suitable provisioned CPs and native SDA service availability
are prerequisites for accepted live testing. Full pinned model parity remains
incomplete.
