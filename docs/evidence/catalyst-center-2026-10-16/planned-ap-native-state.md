# Complete planned-AP native observations

## Implementation

The planned-AP inventory reader now retains both the typed projection and the
complete native object from each response row. It keeps the existing complete
pagination, identity and type validation and performs no additional inventory
request. The public typed readback helper remains compatible.

The existing exact-number JSON decoder is factored into a neutral helper.
Template parameter-order normalization stays inside the template wrapper and is
never applied to opaque AP fields. Unknown properties, nested objects, null versus
absent values, array order and exact JSON numbers remain significant. These
private objects stay in memory; no native attributes or unkeyed native-state hash
are added to status.

Submission compares complete observations of every declared AP, including
unchanged siblings, through the existing one-use preflight guard. Late opaque
changes therefore cannot hide behind matching typed settings. The existing
observed-identity publisher also compares complete native views across its two
verification reads and keeps an unstable observation pending. A later stable
observation may publish the identity receipt without replaying the native write;
it still does not establish exclusive creator ownership.

Updates additionally reject unmapped native fields instead of silently omitting
them from the bounded `bulkChange` body. Matching no-op objects remain observable.
The lab returns a boolean `antenna.elevationAccelerometer` extension that the old
typed model omitted. It is now included in preflight observation, validated as a
boolean when updating, and omitted from the request. Its preservation is checked
against the complete native response during live qualification. This does not
claim that every sensor-flag value or transition has been qualified.

No transport, journal, CRD, secret projection or physical reservation protocol is
added. ND, direct-device upgrades and SWIM keep their existing implementations.
The existing optional private site key and AP observed-identity receipt continue
to compose through the same executor and section owner.

## Fixture and private API qualification

- [287 focused checks](planned-ap-native-state-focused.log), comprising 276
  subcases and eleven standalone checks, pass in 14.236 seconds. They include
  complete-state drift on an unchanged declared sibling, nested changes, exact
  integer precision above 2^53, null/absence, opaque array order, stable key order,
  unmapped update fields, sensor type validation, private status exclusion and
  existing AP/site/template-native regressions.
- [25 real private Kubernetes API cases](planned-ap-native-state-api.log) pass
  in 45.623 seconds with Kubernetes 1.35.0 and actual repository CRDs: nine design
  composition cases, eight identity-fence cases and eight native-state cases.
  These cover unmapped-state refusal, certified preflight retirement, sensor
  omission and native drift between the two publication reads, in both normal
  and private-key modes. Unstable publication remains Verifying without a
  receipt or another native write; stable observation later records the identity.
  The product API and faults are simulated. The log includes the envtest harness's
  controller-runtime logger-setup notice; all cases and the process pass.
- The two publication-fault cases were added after the broad regression run and
  are included in both final focused and private-API results. The fault injection
  initially targeted the wrong test handler; its failed injection assertion was
  corrected before these final runs. No production fix or weakened comparison
  was needed for those cases.

## Native qualification

The [combined native cycle](planned-ap-native-state-live.log) passes in 163.55
seconds including exact generated cleanup. It exercises three site creates, an
initial floor update, two image uploads, AP creation, an AP X-coordinate edit,
a later keyed geometry update and one same-AP corrective edit. Complete native
AP objects are checked after image revision, AP edit and geometry correction,
with only the requested X value changed in the expected response. Native UUIDs,
radios, antenna extension and all other returned fields remain unchanged.

The design phase still emits six exact mutations and three final no-op
reconciliations emit none. All eight acknowledged tasks recover through retained
Unknown without resubmission. Final status has three site creator rows, one latest
site row, one image row and one observed AP identity row. The exact AP UUID is
removed before the generated hierarchy is removed child-first.

Native HTTP/tasks are real; Kubernetes persistence, worker restarts, projected key
files and acknowledged-task uncertainty are simulated. This is map/site design
qualification, not operational AP/WLC provisioning or an upgrade. Raw-state fault
injection is covered by fixtures/private API tests, not destructive lab edits.

[Broad configuration/template/site/design/journal/SWIM regressions](planned-ap-native-state-broad.log)
pass in 232.091 seconds. [ND/shared controller/direct-upgrade foundations](planned-ap-native-state-foundations.log)
pass, partly cached. [Catalog checks](planned-ap-native-state-catalog.log) pass
separately after updating its partial capability description.

Reproduce the native run using the existing private credential runner and
environment in [private design qualification](site-private-design.md), selecting
`TestLiveCatalystCenterPrivateSiteDesign`. No private response values should be
printed or copied into status or qualification artifacts.

[Source hashes](planned-ap-native-state-sources.json), the
[93-resource scope audit](planned-ap-native-state-ledger.log) and
[strict documentation validation](planned-ap-native-state-docs.log) accompany this
qualification. Scope coverage is not complete execution parity.

## Remaining work

These are complete in-memory observations. Subsequent opt-in
[keyed observed/latest AP state](../catalyst-center-2026-10-17/planned-ap-keyed-state.md)
is qualified separately, with a geometry-handoff gate and no creator/Delete
authority. The observed-identity receipt grants no exclusive creation proof or
deletion authority. Radio/type replacement, explicit adoption policy, consumer
protection and public delete/prune remain open. Broader native model/band/radio
combinations, mixed-version rollout and installed manager/worker qualification
remain pending. Opaque arrays are compared conservatively in native order.
Full pinned 93-resource model parity remains incomplete.
