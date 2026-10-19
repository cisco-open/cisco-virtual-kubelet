# Planned-AP observed identity and recovery

## Native contract and implementation

The pinned provider marks AP radio changes as destroy/create. Safe replacement
therefore needs stronger lifecycle control than a fresh name-based lookup.
The [pinned provider resource](https://github.com/CiscoDevNet/terraform-provider-catalystcenter/blob/v0.6.1/internal/provider/resource_catalystcenter_planned_access_point_position.go)
documents that replacement constraint. CVK continues to refuse replacement.

The [isolated native contract probe](planned-ap-task-result-native.log) creates
and updates an AP through restarted public reconciliation, checks all three
coordinate bounds without mutation, and removes the exact AP and hierarchy.
Create and Update tasks contain no AP UUID result. A separate exact-task/tree
[schema-only inspection](planned-ap-task-result-schema.json) records root fields,
progress representation types and zero exact AP UUID matches, without retaining
native bodies, progress text or credentials. This finding applies to the lab;
it is not a claim about every Catalyst Center release.

New public creates use `planned-ap-observe:v1` and retain a
`planned-ap-identity:v1` result atomically with Verified through the existing
configuration action-receipt persistence helper. The result binds the logical
resource digest, original source mode and exact observed AP UUID; its immutable
operation retains the floor UUID, owner/config/controller generations, task,
source and time. A complete task tree and repeated matching desired-state,
geometry and typed native observations are required before publication. Count
and byte capacity are reserved before the individual create is dispatched.

This is an **observed identity receipt**. It grants neither exclusive creation
proof nor deletion permission. No unkeyed private native-state fingerprint is
published. Raw typed observations remain in memory. Unknown native AP fields
are not a protected ownership baseline.

Receipt parsing rejects malformed/pruned results, wrong source mode, task/time/
generation loss, physical metadata, duplicate operations/resources and UUID
aliases, and returns independent copies. The recorded floor/AP identity is
checked before convergence and earlier geometry changes. Missing or replaced
recorded identities stop execution instead of being adopted or recreated.
Updates retain the original observation receipt unchanged.

The existing shared source/owner/journal guard now brackets repeated AP reads
before one-use Create/Update dispatch. Publication uses the existing CAS and
post-write receipt readback. Generic Verified release requires the exact receipt;
matching retained records also fence a partially pruned current operation.
Acknowledged Unknown tasks use the existing declared-resource/full-tree observer
without resubmission. Taskless uncertain outcomes remain fenced.

Legacy operations and pre-existing unrecorded APs are not backfilled with these
records. Existing nondestructive Retain behavior remains distinct from native
creator ownership. No CRD, transport, physical reservation or ND implementation
is introduced or replaced. The sites capability catalog now describes the
already implemented image/AP reporting and partial Apply boundaries accurately.

## Verification

- [217 focused checks](planned-ap-identity-focused.log), comprising 207 subcases
  and ten standalone checks, pass in 11.325 seconds. They include 21 receipt
  boundary cases, count/byte capacity and partial-operation settlement, six
  floor-identity cases, eight public replacement/loss/marker-loss cases, nine
  private design composition cases and 48 design no-dispatch scope checks.
  Existing AP model/catalog/geometry/radio/readback/recovery regressions also pass.
- [17 real private Kubernetes API cases](planned-ap-identity-api.log) pass in
  32.246 seconds with Kubernetes 1.35.0 and repository CRDs: nine composition
  cases and eight normal/private-key identity-fence cases. The initial empty
  required-field fixture was correctly rejected by CRD admission; the final
  marker-loss cases persist a schema-valid malformed result and prove CVK itself
  refuses convergence without emitting another mutation. Product HTTP is fake.
- [Combined native cycle](planned-ap-identity-native.log) passes in 156.08 seconds
  including exact AP/hierarchy cleanup. It exercises three site creates, one
  initial floor update, two image uploads, AP creation, a same-UUID AP edit, a
  later keyed geometry edit and one same-AP corrective update. All eight
  acknowledged tasks recover through Unknown and restarted reconciliation.
  Design mutation count remains six, three subsequent no-ops emit no writes,
  and final bookkeeping contains three site creators, one latest-site row, one
  image row and exactly one observed AP identity matching the original AP UUID.
  The record persists through image revisions and geometry/AP updates.
- Native HTTP/tasks are real; Kubernetes persistence, worker restarts, projected
  keys and acknowledged-task uncertainty are simulated. This is map/site design
  qualification, not operational AP provisioning, an upgrade or installed-worker
  qualification. Replacement/loss injection is fixture/private-API qualified,
  not a live destructive fault test.
- [Configuration/template/site/design/journal/SWIM regressions](planned-ap-identity-broad.log)
  pass in 229.663 seconds. [ND/shared configuration/direct-upgrade foundations](planned-ap-identity-foundations.log)
  pass, partly cached. [Capability catalog checks](planned-ap-identity-catalog.log)
  pass separately after the descriptive catalog update.

Reproduce the combined native cycle using the existing runner loading credentials
privately and the same environment described in
[private design qualification](site-private-design.md). Add
`CATC_PLANNED_AP_TASK_RESULT=isolated-result-inspection` and select
`TestLiveCatalystCenterPlannedAPManifest` with
`CATC_PLANNED_AP_EXECUTION=isolated-hierarchy` for the task-result probe.

[Source hashes](planned-ap-identity-sources.json), the
[93-resource inventory audit](planned-ap-identity-ledger.log) and
[strict documentation validation](planned-ap-identity-docs.log) accompany this phase.

Complete in-memory native AP observations and unmapped-update protection are
subsequently qualified in [native-state evidence](planned-ap-native-state.md).
This does not upgrade the observed identity receipt to creator authority.

## Remaining work

Radio/type replacement, exclusive creator/adoption policy, complete private AP
native-state ownership, consumer protection and public delete/prune remain open.
The absence of a native created-UUID result must not silently become deletion
authority. Source-mode/key migrations, installed manager/worker persistence,
mixed-version rollout and operational AP/WLC hardware qualification remain
separate requirements. The overall 93-resource pinned-model goal is incomplete.
