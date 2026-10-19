# Network settings: durable application receipts

New public NTP, DHCP, DNS, timezone, banner and telemetry mutations now require a
durable application receipt before their journal can clear. Each logical setting
retains one latest executed application, replaced atomically after a later proven
Update. This adds task/source/owner/site custody to existing execution and
[acknowledged-task recovery](network-settings-task-recovery.md). It is not a full
native-state baseline or exclusive creator ownership.

## Representation and shared architecture

The existing network parser, native observer, transport, journal, source/owner
bracket and atomic action-receipt writer are reused. New mutations carry
`network-setting-update:v1:<logical-key>:<predecessor-digest>`. The logical key
hashes only the public site path and setting kind. The predecessor hashes public
bookkeeping with the active journal excluded; setting values never enter it.

A successful application uses the adapter-specific result identity
`network-setting-applied:v1:<logical-key>:<commit-digest>`. The existing immutable
operation retains its original nonce, task, native site UUID, source revisions
and owner. The commit digest binds the complete bookkeeping snapshot, normalizing
only this row's result identity to avoid a self-reference. It detects receipt,
owner or companion-history loss while the operation is active. Historical seals
are not permanent signatures of future history, nor native-value fingerprints.

`EffectReceipts` remains reserved for related objects changed by a native
operation and stays empty for these application records. No common schema,
foundation API, state store or transport is added. An early representation that
used that field was corrected during review before this final qualification.

## Execution, recovery and settlement

The entire selected setting cohort is checked for receipt capacity before the
first setting mutation. The shared limit remains 256 action applications; this
is a bounded implementation, not unlimited model scale. Replacing an existing
setting consumes no additional slot and is tested at full capacity. A retained
application binds its target UUID: a replaced same-path site cannot silently
pass mutation or no-op convergence as the previous target.

Fresh submission guards bind native state and predecessor bookkeeping. An
original no-dispatch proof can retire only while that bookkeeping remains
unchanged. A failed native read cannot hide simultaneous custody loss and erase
the predecessor fence. Changed or corrupt history stays retained for review.

Receipt publication requires the original successful root and complete task
tree, stable desired native readback from the same site, and unchanged complete
source/owner/journal evidence. The shared writer atomically advances Verifying to
Verified and replaces/appends exactly the intended application row. A fresh read
must retain the receipt and complete commit cohort. Missing results, malformed
seals, duplicate logical keys or pruned companion history cannot clear the slot.
Generic Verified transitions cannot bypass this producer contract. A lost status
acknowledgement resumes from the stored receipt without repeating the native call.

Legacy bare setting operations retain their previous completion contract and
are not retroactively assigned receipts. Already-converged imported values and
no-op source edits do not manufacture a task, refresh a receipt or establish
creator ownership. Later changes keep one latest proven mutation per logical
setting; they do not archive every source revision or authorize native destruction.

## Verification

- [Final race regression](network-settings-applications-regression.log), 80.034 seconds: 102 new
  lifecycle cases across six setting types cover receipt creation, original
  Unknown recovery, bounded replacement, target replacement, schema/logical
  duplicates, legacy compatibility, generic-transition refusal, missing result/
  seal/companion history, lost acknowledgements and history changes before and
  after dispatch, including a simultaneous preflight read failure. Six imported
  no-op cases plus whole-cohort capacity and public JSON-normalization checks
  pass. Prior network execution/recovery/no-dispatch, SDA, RMA and bulk network
  checks run alongside them.
- [Actual private Kubernetes API matrix](network-settings-applications-api.log):
  all 102 cases pass in 67.622 seconds, exercising actual controller/config identities, generation/revision,
  status persistence and the current CRD. Identical receipt keys are rejected
  by the API; distinct result strings for a duplicate logical key are rejected
  by the adapter. Controlled native responses and writer faults are fixtures;
  installed actor admission is not claimed.
- [Final native two-revision lifecycle](network-settings-applications-native.log)
  passes in 162.759 seconds on the empty generated hierarchy
  `Global/CVK-Q-20261017-032334-000000000`. Twelve original setting tasks
  recover from simulated Unknown phases; twelve synthetic read-only preflight
  refusals retire/replan before native writes. Two real source revisions retain
  exactly six latest application records with stable targets, new tasks and
  advancing source generation. No-op reconciliation sends no additional writes.
  Three hierarchy Creates and one floor Update are setup actions; exact generated
  hierarchy cleanup is verified. Kubernetes storage and worker interruption/
  restarts are simulated, while native tasks and readback are real. External
  collectors, existing sites, device provisioning and rendered device behavior
  are outside this qualification.
- [Initial attempt](network-settings-applications-initial-native.log) stopped
  after the first six settings, before any second-revision native call, because
  the test harness reused a stale config resourceVersion. [Exact cleanup](network-settings-applications-cleanup.log)
  matched all three retained site UUIDs to original successful create tasks,
  checked unchanged geometry/settings and empty device assignments, then verified
  deletion/absence. The corrected final run starts fresh. This cleanup is a
  supervised generated-fixture operation, not public destructive lifecycle support.
- [Typed composition and AAA regression](network-settings-applications-composition.log),
  6.356 seconds, covers direct, section, global-credential and AAA wrapper
  selection plus existing projected-secret AAA/network rotation and ordering
  fixtures. The same settings executor is reused; AAA credential mutations do
  not acquire metadata receipt authority. Live projected-secret composition and
  installed actors remain unqualified.
- [Shared foundation checks](network-settings-applications-foundations.log)
  cover controller/group behavior, ND, device workers, diagnostics, lifecycle,
  upgrades and GNOI; unchanged packages may use cached results.
- [Current source/pinned module fingerprints](network-settings-applications-sources.json)
  and [strict documentation build](network-settings-applications-docs.log).

## Remaining work

Receipts prove completed controller mutations and verified desired readback at a
specific source revision. They do not retain a complete private native baseline,
per-field ownership mask, exclusive site creator proof or deletion authority.
Native defaults/reset/inheritance combinations, source prune/destruction,
cross-owner handoff, projected-secret composition with installed workers,
physical/rendered-device behavior and scale beyond the shared receipt limit
remain open. Taskless uncertainty remains fenced. SDA semantic verification and
the rest of full pinned NetAsCode parity are still incomplete.
