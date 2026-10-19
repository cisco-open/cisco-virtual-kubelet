# Latest verified private template state

Privately baselined, task-created projects and regular/composite templates now use
explicit Update operations for project attributes, drafts and version commits.
This reuses the existing compiled executors, final dispatch guard, shared journal,
known-task observation and atomic receipt publication. No CRD, transport, provider
interface or credential store is introduced.

The immutable creator receipt remains separate. Each object has at most one
replaceable applied-state receipt bound to the original creator receipt digest,
UUID, parent, configuration identity, key fingerprint and exact current source.
The operation distinguishes project, draft and commit stages and retains source
mode, so stripping both source fields cannot silently release a ConfigMap action.
Both shared count and byte limits are checked before dispatch; replacement needs
no additional slot. Whole new creator sets reserve space for their typed commits.

Completion checks the complete original task tree and native declared state,
then captures two matching keyed native snapshots and repeats native/task proof.
For templates the snapshot includes full draft detail, complete version metadata
and every committed version body, preserving unknown fields and returned member
references. Project snapshots include its native detail and child references.
Unknown field values and private content are only inputs to HMAC-SHA256; they are
not copied into status or exposed as unkeyed content hashes.

The original controller/owner/source/journal snapshot is persisted by CAS with
Verified and the one replacement receipt; exact API readback must retain both.
Schema-pruned receipts, fingerprints or provenance cannot finish the operation.
Acknowledged Unknown tasks resume observation without submitting another request.
Live guard-bound preflight refusal can retire and replan; taskless uncertainty
remains fenced. Composite settling accepts the typed regular commit receipt or
its existing durable preparation floor.

Existing imported objects and legacy creator receipts gain no applied-state
ownership. Legacy in-flight operations keep their original completion contract.
Changing the projection key does not rebaseline an old creator under a new key.
An omitted/deleted historical row is never reconstructed from a matching name.

## Qualification

- [Public race tests](template-applied-tests.log): 65 flows across project Update,
  regular draft Update/commit and composite draft Update/commit. These include
  acknowledged Unknown recovery, key loss/restoration, changing native snapshots,
  wrong declared state, source/owner races, publication conflicts, lost responses
  and schema loss of receipts, source fields or fingerprints.
- Forty-two structural receipt cases bind original creator lineage, UUID/parent,
  owner, key, source mode, generation, timestamps and Verified state. Replacement
  preserves the original creator and bounds history.
- Eight history cases detect edits to older committed content, unknown version
  metadata and added versions, and reject foreign bodies/groups and duplicates. Exact
  numeric decoding distinguishes unknown integers above 2^53.

- [Real private API persistence](template-applied-api.log): all 92 cases pass
  against Kubernetes v1.35.0: 65 applied-state cases and 27 creator private-baseline
  regressions. Real CRDs, generated UIDs/resource versions and persisted status
  are used; native HTTPS responses are controlled, and composite floors are
  explicitly advanced by the fixture. This is not installed actor qualification.
- [Native public cycle](template-applied-native.log): three immutable creator
  baselines and six latest-applied snapshots match independent fresh native reads
  before subsequent effects. Both source revisions reach InSync. Nine original
  acknowledged tasks recover from Unknown without replay; nine proven preflight
  refusals, including three prepared composites, retire before fresh dispatch.
  Original creator receipts remain unchanged and template UUIDs are preserved.
  Exact generated project/template cleanup passes. The test takes 509.36 seconds.
  Native APIs, task trees and elapsed settling are real; Kubernetes persistence,
  restarts and injected refusal/recovery events are simulated. This cycle covers
  regular draft updates and composite member-version recommit, not composite
  draft Update. The subsequent exact-number decoder refinement is covered by the
  final race/API runs and a separate read-only native check.
- [Final configuration/template/wireless race regressions](template-applied-regression.log)
  and [shared ND/direct/SWIM foundations](template-applied-foundations.log) pass.
  Count/byte capacity cases cover overflow before dispatch and replacement at the
  shared receipt count limit without mutating existing history.
- [Pinned inventory](template-applied-ledger.log) retains exactly 93 upstream
  resource rows with no missing or extra types. This is a scope audit, not full
  implementation parity.

- [Final exact-number native observation](template-applied-exact-native.log):
  the final decoder and complete version reader observe an existing non-fixture
  template twice and produce matching keyed snapshots. No task, mutation or
  device deployment is performed. This is focused decoder/reader qualification,
  not an additional public lifecycle or installed actor test.
- [Strict documentation build](template-applied-docs.log) and whitespace checks
  pass.

## Remaining work

These are the latest verified per-mutation snapshots, not a continuously refreshed
copy of native state. Later project child changes invalidate the earlier project
snapshot; subsequent commits replace template applied state. Prune/delete must
still prove complete external references, safe version provenance, ordered child
removal and final current native equality. No destructive template lifecycle is
enabled here. Delete/recreation/tombstones, complete upstream schema/default/import
semantics and installed actor qualification remain pending. Native version Delete
must continue to respect the pinned provider's state-only behavior.

The subsequent [version ownership implementation](template-version-ownership.md)
archives strict commit receipts when latest state is replaced. Such replacement
now reserves an extra shared slot; non-version latest rows remain replaceable
without archive growth. This adds individual version provenance, while complete
external reference protection and public destructive lifecycles remain pending.
