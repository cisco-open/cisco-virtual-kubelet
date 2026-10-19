# Optional keyed planned-AP state

This qualification finished on October 17 in Europe/Berlin; the native logs use
UTC timestamps on October 16.

## Contract and implementation

`cisco.vk/catalyst-center-private-planned-aps: "true"` explicitly enables keyed
AP state for a sites-only configuration using the existing `PrivateComparisonKey`
at `/sites`. No transport, journal, CRD, secret store or physical reservation
protocol is introduced. Existing configurations without the opt-in retain their
previous behavior. Existing/unkeyed objects are not silently adopted or
backfilled; new APs establish their own observed-state records.

The distinct `planned-ap-private-observe:v1` producer binds logical identity,
source mode and scoped key identity. Its atomic `planned-ap-identity:v2` result
retains the observed AP UUID and a domain-separated HMAC of complete native AP
state and floor identity. The immutable original operation retains owner/source,
controller/config generations, original task, floor UUID and times. The task does
not return a created-AP UUID in the lab; this remains **observed-state evidence**,
not proof of exclusive creation and not deletion authority.

Only modeled coordinate-number and MAC representations normalize according to
the existing typed contract. Opaque numbers, fields, null/absence and array order
remain significant. Floor attributes are owned separately. Private native values
stay in memory; status contains public identities and keyed fingerprints, never
an unkeyed hash of native attributes.

New private Updates bind the exact prior receipt digest and expected complete
result MAC before dispatch. The expected result changes only the bounded known
writer fields while retaining other native state, including the observed antenna
extension. Preflight checks the prior native baseline and repeats existing
source/owner/journal/key observations. Complete original task trees, exact
floor/AP identities, desired fields, two native observations and the pre-bound
result MAC precede atomic publication. An unrelated post-submit native change
keeps the operation Verifying without replacing the baseline or replaying the
native request.

One `planned-ap-applied:v1` row replaces the current latest state; the original
keyed observation stays unchanged. Count/byte capacity and exact prior digests
are checked before dispatch and publication. Existing shared receipt persistence
and readback are reused. Generic Verified release requires matching retained
proof, including when current-operation fields are partially pruned. Malformed,
orphaned, aliased, duplicate, stale or mismatched receipts refuse execution.
Key/source-mode loss or disabling the opt-in cannot downgrade retained state.
Taskless uncertainty is not replayed. Retained known tasks use the existing
declared-resource/full-tree observer.

The current row retains its prior digest but replaces the prior row; this is not
a complete archived update chain or a physical-action settlement receipt.

## Geometry boundary

Native floor edits can move existing AP coordinates. This strict opt-in mode
therefore refuses floor updates affecting any retained keyed AP on that floor
before mutation, including APs removed from current source under Retain. It does
not silently rebase those APs. A task-bound geometry-to-AP baseline handoff is
still required for that combined flow. This gate does not alter the previously
qualified unopted private site/image/AP geometry behavior.

## Verification

- [319 focused checks](planned-ap-keyed-focused.log), comprising 307 subcases and
  twelve standalone checks, pass in 20.365 seconds. They include eleven keyed
  native/scope cases, eight private receipt boundaries, expected-result/prior/
  capacity/settlement checks, twelve keyed public cases and earlier AP/site/template
  native regressions. Keyed Create/Update pre-submit refusals also exercise the
  existing one-use proof/CAS retirement path without native replay. Modeled representation equivalence is distinguished from
  opaque-number/null drift and native coordinate changes.
- [37 real private Kubernetes API cases](planned-ap-keyed-api.log) pass in
  67.356 seconds against Kubernetes 1.35.0 and repository CRDs. Twelve keyed cases
  join the prior 25 design/identity/native-state cases. They cover create/two
  updates, retained-task recovery, image/sensor composition, drift, geometry
  protection with and without current AP source, downgrade, marker loss and
  unrelated post-submit changes. Product HTTP and faults are simulated. The
  initial installer reset the opt-in annotation; the explicit nonempty-baseline
  assertion caught that test setup error before it could masquerade as keyed
  qualification. The final installer fixture reapplies the annotation.
- [Native keyed cycle](planned-ap-keyed-native.log) passes in 164.99 seconds with
  exact generated cleanup. Three site creates and an initial floor edit precede
  AP creation. The design phase executes two image uploads, one AP create and
  two same-UUID AP X updates: five exact mutations. Seven acknowledged tasks
  recover through Unknown without resubmission. Complete native AP state and the
  antenna extension remain unchanged apart from requested coordinates. One
  immutable keyed observation and one latest row survive updates; total status
  stays at seven rows. Three final no-ops emit no writes. A later floor edit is
  refused with zero native effects, then the source is restored. The exact AP
  UUID and hierarchy are removed with verified readback.
- Native HTTP/tasks are real; Kubernetes persistence, restarts, projected-key
  files and acknowledged-task faults are simulated. There is no operational AP
  provisioning, software upgrade or installed worker/manager qualification.
- [Configuration/template/site/design/journal/SWIM regression](planned-ap-keyed-broad.log)
  passes in 236.456 seconds. [ND/shared controller/direct-upgrade foundations](planned-ap-keyed-foundations.log)
  pass, partly cached. The [two operator example contracts](planned-ap-keyed-examples.log)
  and [descriptive capability catalog](planned-ap-keyed-catalog.log) pass separately
  after their additions. The two keyed producer-specific no-dispatch cases were
  added after the broad run and are included in the final focused/private-API
  results; runtime code is unchanged.

Use `examples/configs/catalyst-center/planned-ap-private-state.yaml` with the
operator-managed projected key described there. To reproduce the native run,
use the existing private credential runner with:

```text
CATC_SITE_EXECUTION=isolated-hierarchy
CATC_SITE_PRIVATE_BASELINE=isolated-design
CATC_AP_NATIVE_BASELINE=isolated-keyed
CATC_SITE_TASK_RECOVERY=isolated-acknowledged-tasks
CATC_LIVE_REQUESTS_PER_SECOND=2
go test -race -tags live ./internal/controlleradapter/catalystcenter \
  -run '^TestLiveCatalystCenterPrivateSiteDesign$' -count=1 -v
```

The existing endpoint and credentials are required and must not be printed.

[Source hashes](planned-ap-keyed-sources.json), the
[exact 93-resource inventory audit](planned-ap-keyed-ledger.log) and
[strict documentation validation](planned-ap-keyed-docs.log) accompany this phase.

The old temporary upstream directory was found incomplete during the final audit.
The 14 upstream `cc_*.tf` files were restored directly from the immutable pinned
commit, and all 93 resource types were compared independently with the ledger.
[Archive/file hashes and inventory](planned-ap-keyed-upstream-audit.json) record
that recovery rather than assuming the old cache was intact.

## Remaining work

Geometry handoff, exclusive creator/adoption policy, consumer protection, public
delete/prune and installed actors remain open. The pinned provider requires
replacement for radio and floor changes. AP type has no equivalent schema plan
modifier, and the categorical CVK refusal is subsequently removed for compatible models
in [type-update qualification](planned-ap-type-updates.md). Broader model and
radio side-effect qualification remains pending. See
the [pinned provider schema](https://github.com/CiscoDevNet/terraform-provider-catalystcenter/blob/v0.6.1/internal/provider/resource_catalystcenter_planned_access_point_position.go).
Broader models/bands/radio combinations, MAC/Z transitions, opaque-state handling
across controller versions, key/source-mode migration and mixed-version rollout
remain unqualified. Full pinned 93-resource parity is incomplete.
