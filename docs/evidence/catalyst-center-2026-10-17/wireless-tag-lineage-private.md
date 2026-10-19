# Private incarnation lineage for wireless tags

The follow-on [recreation execution stage](wireless-tag-recreate-execution.md)
now connects the planner and atomic writer. This report retains the earlier
private-proof qualification boundary.

## Implementation

V5 now distinguishes `recreate`, ordinary `write`, `members` and `delete`.
Recreate remains Create-only; ordinary writes and member reductions are
Update-only; deletion remains Delete-only. Canonical native selectors and the
existing producer/key/journal identities retain their meaning. V1-v4 decoding,
MAC snapshots and journal interpretation are unchanged.

Private comparisons wrap the existing native/dependency baseline with the v5
incarnation digest and version. Changing or dropping that digest changes the
MAC even when native UUIDs and fields are identical. The wire encoding is pinned
by exact-byte tests. Policy tags retain their complete parent RF/SSID graph;
site tags retain their existing direct dependency bindings.

The private write wrapper now preserves an existing incarnation through normal
updates and membership reduction. Removed scopes remain independently encoded
and checked; the extra digest counts against the existing 512-character bound.
Final custody/source checks retain the existing one-use no-dispatch capability.

Deletion authenticates the same incarnation in both the scope seal and final
absence/tombstone effect. Repeated post-absence observations reconstruct the
original lineage, and subsequent recreation-absence checks consume that proof.
The retained-history reader requires every latest application/tombstone to keep
its creator's lineage and rejects orphan lineage results, downgrades and changed
incarnations. Archives remain in the existing action-application store.

## Focused qualification

The [local race gate](wireless-tag-lineage-private-local.log) passes in 10.784 s;
the [temporary Kubernetes API gate](wireless-tag-lineage-private-api.log) passes
in 27.831 s. Each includes:

- 84 original-history and 84 archive-chain scenarios, including explicit latest
  lineage removal/replacement refusal and eight valid archived-status round trips.
- 34 HTTP-fixture dependency/readback cases: the previous seventeen v4 cases and
  seventeen v5 counterparts across write, pre-delete and post-delete observation.
- A planned lineage-preserving policy-zone reduction with exact affected scope
  and a final custody refusal proving no native dispatch occurred.
- Twelve mode/action combinations, four MAC-domain/encoding cases, the existing
  24 recreation envelope cases and tag custody/deletion/SWIM regression checks.

[Exact gates and source fingerprints](wireless-tag-lineage-private-validation.json)
separate these completed checks from the full legacy lifecycle commands below.
The [initial failed scope gate](wireless-tag-lineage-private-scope-initial-failed.log)
found that the newly added digest displaced the affected-site scope in decoding.
The decoder now validates the lineage envelope and reads the preceding scope;
that failed command is not counted as passing evidence.

## Full legacy lifecycle qualification

The [complete local command](wireless-tag-lineage-legacy-local.log) passed all
**140 original lifecycle cases** in 270.058 s: 34 membership, 60 pruning and 46
receipt cases. Its archive/history helper scenarios are additional nested checks,
not counted again in that original-case total.

The [complete temporary Kubernetes API command](wireless-tag-lineage-legacy-api.log)
also passed all 140 original cases in 707.580 s: 34 membership, 60 pruning and 46
receipt cases. The [case-counted audit](wireless-tag-lineage-legacy-audit.json)
verifies both terminal aggregate results. These commands qualify the private-proof
source snapshot before the subsequent recreation planner/writer activation.

## Remaining execution boundary

The planner still fences recreation. No new recreate operation or archive was
submitted to Catalyst Center. Native observations above use HTTP fixtures; the
previous [v4 lab workflows](wireless-tag-bindings.md) remain historical evidence.

Complete-producer planning, bounded capacity reservation, final absence checks,
atomic predecessor archival plus new-creator publication, original-task recovery
and repeated manifest-driven lab recreation must still be integrated and
qualified. Installed workers, larger/mixed cohorts, migration/handoff/retirement
and full 93-resource lifecycle parity remain open. No other provider, foundation
transport or direct-device upgrade implementation changed.
