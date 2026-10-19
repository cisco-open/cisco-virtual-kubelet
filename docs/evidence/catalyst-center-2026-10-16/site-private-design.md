# Private site and drawing-design composition

## Implementation and boundaries

Private keyed singular sites can now include pinned floor-image and planned-AP
intent. The existing section owner, source snapshot, projected comparison key,
creator/latest site receipts, common journal and authenticated transport remain
the authority. No new foundation, transport, CRD or secret store is introduced.
Bulk private site composition remains refused.

Before image or AP dispatch, the enclosing executor checks the exact owned floor
UUID and repeated complete native site/ancestor footprints around the shared
source/owner/journal guard. Private baseline checks still run before convergence.
Planned-AP submission also freezes and repeats typed AP observations, preserving
omitted MAC/Z/radio settings and refusing late appearance, replacement or edits.
Floor identity and geometry are rechecked before reading a potentially replaced
floor's drawing inventory.

The common one-use guard now supports exclusive synchronous senders. Image
preflight failures can claim the same guard-bound no-dispatch capability and CAS
retirement as task submissions. Previously this capability was lost in the
synchronous receipt path, leaving a refused upload unnecessarily Unknown. Native
errors and lost responses still acquire no refusal proof and cannot replay.

Declared planned-AP Accepted and acknowledged Unknown operations require the
existing complete rooted task tree and exact resource selection. The common
reconciler retains source/controller/owner checks. Taskless uncertainty is not
recovered by inspecting apparent desired state.

Site creator receipts are unchanged. Images retain bounded upload-completion
receipts, not proof of current native bytes. Planned APs still use scoped Retain
convergence, not individual creator ownership or dependency-safe deletion. A
subsequent phase adds [retained observed identity](planned-ap-identity.md), which
detects replacement/loss without claiming exclusive creation. Typed
AP observation does not claim to preserve unknown native fields.

## Qualification

- [Focused tests](site-design-focused.log): three synchronous guard cases, seven
  stale/native AP observation cases and nine restarted public manifest cases.
  These include optional defaults, image ordering/revision, acknowledged task
  recovery, certified refusal retirement and ambiguous-response fencing.
- [36 refusal-scope checks](site-design-scope.log) reject physical, task-bearing,
  destructive, legacy and malformed operations across image/AP create/AP update.
- [Nine private API cases](site-design-api.log) pass against isolated Kubernetes
  1.35.0 and actual repository CRDs. Product HTTP is simulated. This proves
  persistence/schema boundaries, not installed manager/worker behavior.
- [Native cycle](site-design-native.log) passes in 154.84 seconds including exact
  cleanup. It creates an isolated area/building/floor, updates floor width, then
  composes image upload and AP creation, changes image bytes, edits AP X and
  changes floor width again. The final geometry step requires one additional
  update to the same declared AP. Exact complete typed AP readback and three
  subsequent no-op reconciliations pass. The design phase counts six mutations:
  two synchronous uploads, three AP writes and one geometry write. The initial
  site phase adds four writes. All eight acknowledged tasks recover through
  retained Unknown and restarted reconciliation without resubmission. Three
  creator receipts remain unchanged; final bounded status has three creators,
  one latest-site row and one image row. The AP is removed by exact UUID before
  the hierarchy is removed child-first, with both cleanup readbacks verified.
- Native HTTP/tasks are real; Kubernetes persistence, restarts, projected-key
  files and acknowledged-task uncertainty are simulated. Only map design and
  site metadata are changed. No AP provisioning, device operation or upgrade is
  performed.
- [Configuration/template/site/design/journal/SWIM regression](site-design-broad.log)
  passes in 225.669 seconds.
- [ND/shared configuration/direct software-upgrade foundations](site-design-foundations.log)
  pass, with unchanged packages partly cached.

The [initial live attempt](site-design-initial-native-failed.log) stopped after an unexpected corrective AP operation
following geometry change. Its isolated hierarchy remains retained because the
test stopped before observing final completion; it was not treated as a clean
pass or automatically deleted. Exact retained identities are:

```text
Global/CVK-Q-20261016-210037-000000000
area     64a209d0-7f46-4ecd-8b38-6d8ddeea1efe
building ebabe5fd-a90d-4249-934c-78e258ec048e
floor    63db8865-aa8a-4372-8d9f-79769714523a
AP       a565372d-c34f-49b8-8a5b-132e2be0f441
```

The corrected qualification permits at most one exact same-AP correction after
geometry, verifies final state, counts every mutation and requires stable no-ops.
This is explicit qualification of the existing convergence behavior, not a
generic retry or permission to replay an uncertain operation.

Reproduce with the existing live runner loading credentials privately:

```text
CATC_SITE_EXECUTION=isolated-hierarchy
CATC_SITE_PRIVATE_BASELINE=isolated-design
CATC_SITE_TASK_RECOVERY=isolated-acknowledged-tasks
CATC_LIVE_REQUESTS_PER_SECOND=2
go test -race -tags live ./internal/controlleradapter/catalystcenter \
  -run '^TestLiveCatalystCenterPrivateSiteDesign$' -count=1 -v
```

The existing endpoint/username/password variables are also required and must not
be printed. For operator manifests, combine the source/key configuration in
`examples/configs/catalyst-center/site-private-creator-execution.yaml` with the
same-ConfigMap image asset convention in
`examples/configs/catalyst-center/floor-image-execution.yaml` and the pinned
floor `planned_access_points` structure in
`examples/configs/catalyst-center/planned-ap-apply.yaml`.

[Source hashes](site-design-sources.json) and the
[exact 93-resource inventory audit](site-design-ledger.log) record this phase
without claiming full resource support. [Strict documentation validation](site-design-docs.log)
also passes.

## Remaining work

Complete native consumer protection, public prune/delete and atomic destructive
settlement remain open. Site snapshots alone grant no deletion authority. Bulk
creator/update lineage, key/source-mode migration, image readback, AP replacement
and per-AP ownership are separate work. Installed actors, actual manager-mounted
key projection, mixed-version rollout and physical reservation coordination
remain unqualified. Full 93-resource pinned-model execution parity is incomplete.
