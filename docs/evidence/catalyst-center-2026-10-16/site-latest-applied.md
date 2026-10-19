# Private site latest-applied state and update recovery

## Implementation

Private singular site Update now uses the existing configuration apply plan,
one-use dispatcher, task observer and atomic action-receipt persistence. The
creator receipt stays immutable. One current `site-applied:v1` row per site
records the latest verified native footprint, original Update operation and a
digest linking the original creator receipt. No new transport, journal, CRD
field, key store or physical-device reservation path is introduced.

The `site-private-update:v1` producer binds kind/path identity, source mode,
controller/config/endpoint-scoped key identity and the exact prior receipt digest.
Native state must match the previous latest baseline, or the creator baseline
for the first Update. The existing public guard checks fresh source/owner/journal
identity. Count/byte capacity and the prior digest are checked before native
dispatch and again at publication. Unknown native edits refuse dispatch with the
existing certified no-dispatch capability; lost replies remain taskless and do
not acquire replay permission.

Completion requires the entire original task tree, the original task result UUID
matching the captured target, repeated exact target/parent/kind and selected-field
readback, and repeated full private native snapshots. Approved key/source checks
bracket observation. Verified and the new latest row persist atomically and are
reread together. Pending, failed, foreign or missing task evidence never falls
back to generic completion.

Replacing a current logical metadata row preserves creator lineage and bounds
status growth. The admission-time prior digest prevents a pending operation from
publishing against a changed prior row. After publication the old latest row is
removed; this is not a complete archived Update audit chain or a physical-action
reservation receipt. No physical settlement authority is gained from it.

Matching retained producer/result evidence also fences generic settlement if an
older schema drops fields from the current Verified operation. Malformed,
orphaned, duplicate or aliased rows fail closed. Private key/source-mode migration
and adoption are not inferred. Post-apply external drift retains the current
row instead of overwriting or rebasing native state.

The target snapshot retains complete native fields and exact JSON numbers;
ancestor identities remain separate from ancestor settings. A parent coordinate
update therefore does not rewrite the child's latest evidence. Raw private
values stay in memory; status contains identities and keyed fingerprints.

## Scale and verification

Baseline checks now reuse one validated raw inventory across all declared
private sites. Typed/raw identity consistency and complete paging checks remain.
Independent mutation observations still read fresh state; no inventory cache is
retained across reconciliations. This removes one full inventory fetch per site
without changing the shared transport or admitting stale mutation plans.

- [107 focused checks](site-latest-focused.log), comprising 102 subcases and five
  standalone checks, pass in 28.199 seconds. They include creator/private baseline
  regressions, latest-row lineage/schema/time/owner boundaries, prior-digest and
  count/byte capacity admission, partial-operation settlement, immutable history,
  two restarted public Updates, task/result/source/owner failures, certified
  preflight refusal, lost reply fencing and post-apply drift. A three-site test
  asserts one raw inventory request for a baseline check. The repository private
  site example remains parser/contract checked.
- [24 real private Kubernetes API cases](site-latest-api.log) pass in 67.971
  seconds including startup/teardown, using Kubernetes 1.35.0 and actual CRDs.
  Both building and floor cases execute creation/two Updates and bounded latest
  replacement, or retain the appropriate task/result/source/owner/unknown/drift
  fence. Product HTTP is simulated. This proves private API persistence and
  schema retention, not installed worker or manager key projection behavior.
- [Final native cycle](site-latest-native.log) passes in 104.58 seconds including
  exact cleanup. A full isolated hierarchy creates three native sites, changes
  floor width twice, then changes the parent building latitude. All three creator
  receipts stay unchanged; two floor Updates replace one current row, and the
  building update adds its own row without changing the child's row. Final
  bookkeeping has three creators plus two latest rows. Task result UUIDs match
  the original target identities; repeated native no-ops converge.
- That same native cycle injects six local pre-submit read failures, verifies
  six certified retirements with no native effects, then counts six exact real
  Create/Update mutations. All six acknowledged tasks recover through retained
  Unknown and restarted public reconciliation without resubmission. The generated
  hierarchy is removed child-first. Native HTTP/tasks are real; persistence,
  worker restarts, key projection files and uncertainty/read faults are simulated.
  No device, PnP claim or software upgrade is performed.
- [Configuration/template/site/design/journal/SWIM regression](site-latest-broad.log)
  passes in 222.513 seconds. [ND/controller/direct/software-upgrade foundation
  packages](site-latest-foundations.log) pass (some unchanged results cached).

Reproduce the native qualification with credentials loaded privately by the
existing live runner:

```text
CATC_SITE_EXECUTION=isolated-hierarchy
CATC_SITE_PRIVATE_BASELINE=isolated-latest-applied
CATC_SITE_TASK_RECOVERY=isolated-acknowledged-tasks
CATC_SITE_NO_DISPATCH=isolated-preflight-reads
CATC_LIVE_REQUESTS_PER_SECOND=2
go test -race -tags live ./internal/controlleradapter/catalystcenter \
  -run '^TestLiveCatalystCenterSitePrivateApplied$' -count=1 -v
```

The existing CATC endpoint/username/password variables are also required and must
not be printed. Use the updated repository example at
`examples/configs/catalyst-center/site-private-creator-execution.yaml`; its key
reference is operational metadata outside the unchanged pinned site model.
[Source hashes](site-latest-sources.json), [93-resource scope audit](site-latest-ledger.log)
and [strict docs](site-latest-docs.log) accompany the qualification.

## Remaining work

Dependency/consumer protection, public prune/delete and atomic destructive
settlement remain pending. Creator/latest snapshots alone grant no delete
permit; the live test's exact generated cleanup is not public lifecycle support.
The first task-bound post-write native observation is not an appliance-signed
historical body or proof that no third party ever wrote before capture.

Private floor-image/planned-AP composition is subsequently qualified in
[composition evidence](site-private-design.md). Bulk creator/update lineage, explicit
key/source-mode migration and adoption remain separate work. Installed workers,
actual manager-mounted key projection, mixed-version rollout and real actor/
reservation coordination still require qualification. Full pinned-model parity
is incomplete.
