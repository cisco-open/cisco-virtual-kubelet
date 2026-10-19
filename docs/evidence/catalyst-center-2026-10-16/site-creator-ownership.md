# Singular site creator ownership

## Implementation

Singular area/building/floor Create now retains an original-operation creator
receipt using the existing action-application collection and atomic status
persistence helper. There is no new transport, journal, CRD field or independent
controller framework. Bulk creates and updates of imported Retain objects do not
acquire creator ownership through this feature.

At Verifying the original complete task tree must succeed, and the original root
task must expose a canonical nonempty UUID in its result data. The exact current
native site must have that UUID, authored kind/path, the originally captured
parent UUID and converged selected attributes. The task and typed target/ancestry
observations repeat around current source checks. A changed view refuses the
receipt. Arbitrary progress text, raw native bodies and site attributes are not
retained in public status.

The versioned `site-created:v1` receipt contains only public identity: kind, a
hash of the public site path, explicit inline/ConfigMap source-mode discriminator
and created UUID, plus the unchanged Verified original operation and verification
time. ConfigMap source identity/version remains in that operation. Losing both
source fields cannot silently turn a ConfigMap receipt into inline ownership.
The creator-history snapshot is deep-copied so mutable timestamps in a caller's
status object cannot rewrite the comparison expected during dispatch/verification.

The existing dispatch authority guard reserves shared receipt count/byte capacity
before sending. Duplicate target paths, created UUIDs or operation aliases,
malformed/pruned receipts, foreign ownership and changed creator history fail
closed. `Verified` and the receipt persist in one update; the existing helper
rereads both. Generic settlement of a site Create without its exact original
receipt is refused, including when parent/result fields are absent. Generic
journal fixtures now use opaque logical resource names to test journal behavior
without pretending to supply a product-specific native site creator proof.

The shared site/design executor checks declared creator identities immediately
after the complete site inventory read, before either convergence or image/AP
placement planning. Missing or replaced owned objects cannot be recreated or
adopted merely because their visible attributes match. Source removal with Retain
does not destroy the retained record. These identity receipts are not private
latest-applied attribute baselines or deletion authorization.

## Verification

- [Creator authority checks](site-creator-authority.log) pass in 4.859 seconds:
  33 subcases and two standalone checks cover receipt schema/source-mode loss,
  malformed/duplicate/aliased identities, parent/task/time/owner boundaries,
  storage count/byte limits, changed history, immutable snapshots and generic
  settlement without proof. Six of the subcases exercise public create/no-op
  followed by external removal or same-state UUID replacement; four restarted
  reconciliations retain the receipt, send no new write and refuse InSync.
- The initial [site/floor/journal focused set](site-creator-focused.log) passes
  in 32.654 seconds after supplying real creator-result shapes in fixtures.
  It predates the final always-on site/design no-op fence and snapshot deep-copy;
  final authority/private-API/broad/native logs qualify those changes separately.
- [72 real private Kubernetes API cases](site-creator-api.log) cover 20 dispatch,
  46 acknowledged task/result/source/owner/readback cases and six external
  missing/replaced creator identity cases. They use real CRDs on Kubernetes
  1.35.0 with simulated product HTTP. Valid creator receipts must survive full
  API status round trips; missing or foreign task results retain Verifying.
  The complete API set passes in 95.172 seconds including API startup/teardown.
  This does not qualify installed actors or real device reservations.
- [Final native cycle](site-creator-native.log) passes in 41.30 seconds including
  exact cleanup. A full manifest creates an isolated area/building/floor hierarchy,
  and all three original-task creator receipts survive restarted public
  reconciliation. Independent task result reads bind exact kind/path/parent.
  Floor width then changes without replacing creator lineage. All four
  acknowledged tasks recover from injected Unknown status without resubmission.
  Four local pre-submit read faults cause four certified retirements followed by
  exactly three creates and one update. Readback/no-op and child-first generated
  fixture cleanup pass. Native HTTP/tasks are real; persistence, worker restarts,
  read faults and uncertainty injection are simulated. No physical device, PnP
  claim or SWIM action is performed.
- [Broader configuration/template/site/floor/journal/SWIM regressions](site-creator-broad.log)
  and [ND/controller/direct upgrade foundations](site-creator-foundations.log)
  accompany the work. Some unchanged foundation results are cached.

The six replacement/missing cases initially found that `sitePlannedAPExecutor`
only called the singular planner when attribute drift existed. The ownership
check now runs before that branch. The final passing authority/API suite proves
that identical attributes no longer hide a foreign UUID; this was a production
fix, not a relaxed test assertion.

Reproduce the native run with credentials loaded privately by the existing runner:

```text
CATC_SITE_EXECUTION=isolated-hierarchy
CATC_SITE_SINGULAR_MANIFEST=isolated-hierarchy
CATC_SITE_TASK_RECOVERY=isolated-acknowledged-tasks
CATC_SITE_NO_DISPATCH=isolated-preflight-reads
CATC_LIVE_REQUESTS_PER_SECOND=2
go test -race -tags live ./internal/controlleradapter/catalystcenter \
  -run '^TestLiveCatalystCenterSiteSingularManifest$' -count=1 -v
```

The existing CATC endpoint/username/password variables are also required and must
not be printed. [Final source hashes](site-creator-sources.json),
[exact 93-resource scope audit](site-creator-ledger.log) and
[strict documentation build](site-creator-docs.log) accompany this qualification.

## Remaining work

Creator lineage is implemented for singular creates. Private latest-applied
baselines, complete dependency/consumer protection, public prune/delete and
atomic destructive settlement remain pending. The bounded live test cleanup is
not public manifest-driven deletion support. Older retained creates without a
usable original task-result UUID stay fenced;
no inferred ownership migration is performed. Bulk creator lineage, installed
workers/schema rollout, real actor/reservation coordination and the remaining
resource-level gaps are separate work. Full pinned-model parity is incomplete.
