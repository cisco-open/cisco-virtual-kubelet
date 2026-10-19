# Manifest-driven wireless tag recreation

**Historical first execution revision:** fixture tests passed, but native
qualification exposed an incorrect fresh-identifier requirement. Catalyst Center
reuses the site-tag identifier after delete/create. The retained fixture was
cleaned up; the [corrected authenticated-incarnation stage](wireless-tag-recreate-stable-identity.md)
supersedes the pending/UUID assumptions below. This original native command
failed and is not counted as a passing workflow.

## Supported flow

Reintroducing a previously pruned tag now plans a real Create when the same
NetworkControllerConfig still owns the complete original producer history,
uses Apply with `prunePolicy: Delete` and the existing `/network_profiles`
PrivateComparisonKey projection, and has newer manifest/source input. Parent
UUIDs and required profile/site associations must remain available. No new
manifest field, CRD, provider, transport or Secret channel is introduced.

The planner reconstructs the entire deleted producer set, refuses partial or
mixed policy cohorts, verifies the original private tombstones and repeated
native absence, and reserves a new action slot. Old history is retained rather
than replaced, so the existing 256-row and 512-character limits still apply.
A source/key/owner or native change at final preflight retains the existing
one-use no-dispatch refusal behavior.

The existing native tag executor submits the singleton/bulk Create. Verification
uses the original acknowledged task and complete task tree, stable native
readback, v5 private incarnation comparisons, source/owner checks and resource
version fences. Only then does the receipt writer stage predecessor archival in
an independent copy, append the new creator, and publish both with Verified in
one existing status update. Readback validates the complete archive chain and
active receipt. Prior or retired UUIDs cannot become the new incarnation.

Subsequent writes, member reductions and deletion preserve the incarnation
through the existing private executor. The change does not grant creator rights
to imported/no-op matches or authorize parent/config deletion or owner handoff.

## Completed fixture qualification

The [local race gate](wireless-tag-recreate-execution-local.log) passes in
82.062 s. The [temporary Kubernetes API gate](wireless-tag-recreate-execution-api.log)
passes in 210.925 s. **Seventeen recreation cases pass on each surface**:
singleton and bulk producers cover success, ledger capacity, native reappearance,
key rotation, late preflight refusal, lost acknowledgement, failed task children,
missing task trees and a partial bulk producer. The inapplicable singleton partial
bulk case is explicitly skipped, not counted as a success.

Successful cases prove fresh UUIDs, two complete predecessor archives, exact
native mutation counts, original acknowledged-task recovery and final InSync/no-op.
Failure cases preserve the prior action history without partial archive/creator
publication or replay. Separate fixtures exercise production archive staging
without mutating its input, complete predecessor chains and status round trips.
The same gates retain lineage/action/readback checks; the API gate also includes
legacy protocol/custody and SWIM checks. The [dedicated compatibility gate](wireless-tag-recreate-execution-compatibility.log)
passes in 2.388 s. These native requests use HTTP fixtures, not Catalyst Center.

[Gate results, source fingerprints and active job handles](wireless-tag-recreate-execution-validation.json)
record exact boundaries. The earlier private-proof stage's complete 140-case
local and 140-case API commands also passed, but remain historical source
snapshots. Against the activated planner/writer, the [complete local command](wireless-tag-recreate-legacy-local.log)
now passes all 140 original lifecycle cases in 272.683 s. The complete temporary
API rerun remains active; its terminal result is pending. The [case-counted
legacy audit](wireless-tag-recreate-legacy-audit.json) records these separately.

## Native qualification running

One serial command is qualifying singleton and custom-zone cohort recreation
against Catalyst Center. It uses generated disabled SSIDs, unused profiles and
empty areas. Each workflow performs the original lifecycle, privately prunes
tags while preserving the remaining parent/site association, recreates both
complete producers, checks fresh UUIDs and archives, and finally deletes the
new incarnation before association removal and exact fixture cleanup.

The [partial audit](wireless-tag-recreate-native-partial.json) is explicitly
pending. Expected counts of 18 and 22 original tasks are not completed results.
The raw live log and original process handle are recorded in the validation
inventory; poll that run rather than starting a duplicate. Kubernetes actors in
this native harness are simulated; API persistence is qualified separately.
No device provisioning, software upgrade or physical operation is submitted.

The [first launch](wireless-tag-recreate-native-timeout-refused.log) terminated
at timeout validation before any lab operation: 70 m exceeded the harness's
existing 45 m workflow limit. The active run uses 45 m without weakening that
bound; the failed preflight is not counted as qualification.

## Still open

Terminal native recreation, fresh-incarnation pruning and restored baseline
proofs are pending, as is the complete legacy API rerun. Further work includes
repeated cycles with v5 updates/member transitions, larger/mixed cohorts and
scopes, archive/key/owner handoff and retirement, parent/config deletion,
association resurrection, installed actors and physical WLC/AP behavior.
Full pinned 93-resource lifecycle parity is unfinished.
