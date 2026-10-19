# Corrected recreation with stable native tag identifiers

## Lab finding and correction

The first native workflow exposed a false assumption: the lab Catalyst Center
reuses a site-tag identifier after verified delete/create. A different identifier
cannot be the definition of a new CVK-managed incarnation.

The [failed native run](wireless-tag-recreate-first-native-failed.log) completed
thirteen original metadata operations and acknowledged the recreate, then refused
to publish its receipt. An [independent read-only probe](wireless-tag-recreate-readonly-probe.log)
proved the recreate task and full tree had succeeded and the exact native source
readback matched. A separately gated, [supervised identity experiment](wireless-tag-recreate-identity-cycle.log)
used two new acknowledged operations, independently observed absence and proved
that the same identifier returned after delete/create. It did not replay the
original request.

The [retained fixture cleanup](wireless-tag-recreate-retained-cleanup.log) then
removed only the exact generated tag, profile association, profile, disabled SSID
and two empty areas. Fresh existing device/PnP consumer checks, original task
completion and independent site/profile/SSID baseline comparisons passed. No
physical operation or installed cluster change was submitted. Probe entry points
were subsequently separated so the read-only entry point is strictly non-mutating.

## Current execution semantics

Recreation still requires the same owner and parent, complete original producer
history, all verified member tombstones, newer manifest/source input, the existing
private comparison key and repeated native absence before submission. It uses
Apply with `prunePolicy: Delete`; no new manifest field or persistence store is
introduced.

An existing native identifier may recur **only for the same logical member in
the same parent/namespace**. It cannot alias another member. A new acknowledged
task must differ from every retained task, including archived predecessors.
Original task/tree completion, stable source/native readback and v5 private
lineage comparisons precede atomic archival and new-creator publication.

The incarnation digest authenticates the exact predecessor history. Its private
MAC remains different for different incarnations even if identifier and native
fields are identical. Updates and deletion preserve that lineage; dropping it,
reusing old effects/tasks, skipping tombstones or changing owner/key/source does
not gain authority. Ledger/resource-size bounds and no-replay fences remain.

## Completed current-revision qualification

The [local race gate](wireless-tag-stable-identity-local.log) passes in 87.591 s;
the [temporary Kubernetes API gate](wireless-tag-stable-identity-api.log) passes
in 227.693 s. Each includes **19 reconciler-driven recreation cases**, covering
both stable and fresh identifiers for singleton and bulk producers, plus capacity,
reappearance, key rotation, late preflight refusal, lost acknowledgement, failed
children, missing task trees and partial bulk refusal. The inapplicable singleton
partial-bulk case is skipped.

Each also includes 84 original-history and 89 archive scenarios: cross-member
aliasing and ancestor task reuse are explicitly rejected. Structural archive
fixtures use synthetic successor effects and are not native custody attestation;
the execution cases use actual private comparisons and original task recovery.
Additional lineage MAC/encoding, mode/action and 34 dependency/readback cases pass;
the API command also includes tag protocol/custody and SWIM compatibility gates.

[Exact gates, source fingerprints and active processes](wireless-tag-stable-identity-validation.json)
separate this revision from the earlier passing fixture snapshots that missed
the native identifier behavior.

## Native qualification and completed lifecycle gates

After confirmed cleanup, both serial native workflows passed with the corrected
implementation. The [complete log](wireless-tag-stable-native.log) and
[independent audit](wireless-tag-stable-native-audit.json) verify **40 distinct
original tasks** and complete unrelated baseline restoration:

- Singleton: **1142.66 s**, eighteen original tasks, two authenticated stable-ID
  recreations, two complete archived producer cohorts, four current private rows
  and four member proofs after final pruning.
- Bulk policy cohort: **2293.52 s**, twenty-two original tasks, three authenticated
  stable-ID tag members, two complete archived producer cohorts, five current
  private rows and six member proofs after final pruning.

Both verify exact membership and deletion scopes, complete original task trees,
producer digests and epoch continuity, old deletion before recreation and new
incarnation deletion before final association removal. All generated fixtures
were cleaned up. No physical/device operation was submitted. The previous
[singleton segment](wireless-tag-stable-native-single.log) and
[partial audit](wireless-tag-stable-native-partial.json) are historical snapshots;
they are superseded by the complete audit. The original command exited zero.

The native harness simulates Kubernetes actors/storage; temporary API persistence
is qualified separately. It uses generated disabled SSIDs, unused profiles and
empty areas, without device provisioning or physical operations. On a future
terminal failure it logs symbolic condition reasons and can retain private-file
public journal metadata and a source checksum. Credentials, comparison keys,
raw source configuration and native progress are excluded.

The [complete local lifecycle command](wireless-tag-stable-legacy-local.log)
passes all 140 original cases in 272.154 s (34 membership, 60 pruning and 46
receipt cases). The [complete temporary-API command](wireless-tag-stable-legacy-api.log)
also passes all 140 cases in 710.132 s. The [case-counted audit](wireless-tag-stable-legacy-audit.json)
verifies both terminal aggregate results. Pre-correction commands remain historical
snapshots.

Full pinned-model parity is unfinished. Native repeated v5 update/member cycles, larger/mixed scopes, ownership/key/archive
handoff and retirement, parent/config deletion, association resurrection,
installed workers and physical WLC/AP behavior remain open.

The subsequent [repeated-cycle stage](wireless-tag-repeat-recreation.md) passes
23 local and 23 temporary-API execution cases, including four complete two-cycle
cases. Its test-only additions and prepared live harness are separately
fingerprinted; the active native binary predates those additions.
