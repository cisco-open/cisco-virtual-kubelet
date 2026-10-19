# Partial telemetry profiles and preserving submission

CVK now accepts a resolved telemetry profile with selected non-null fields rather
than requiring all booleans and lists. Explicit `false` and explicit empty server
lists remain selected. Omitted/null booleans and lists remain unmanaged; a profile
with no selected fields has zero resources and no native effect. Unknown keys,
invalid types, addresses and ports are refused before native submission.

Collector choice can be selected independently of device collection enablement.
A Builtin selection clears external collector fields from the request. External
collector address/port may be preserved from compatible known native state, but
missing values cannot be invented. Declared external fields require an explicit
resolved external collector choice; conflicting Builtin/external input is refused.

## Why the native request must be complete

The direct [probe](telemetry-partial-probe.log) found that a wired-only PUT changed
wired, wireless, syslog and application-visibility groups. A subsequent syslog-only
PUT completed its task but failed the strict native reader. Omitted groups are
therefore not safe PATCH semantics.

The executor now overlays selected source fields onto a deep copy of the complete
observed effective profile and validates all five rendered groups with the existing
strict observation parser. Missing companion state prevents a write. Unknown native groups, group fields or
collector fields are also refused: an unrepresentable field cannot be silently
dropped by a replacing PUT. Two fresh
pre-submit observations still bind the whole profile, actual inheritance chain,
site IDs, source, owner and receipt cohort. Unselected changes during that window
refuse dispatch and use the existing one-use no-dispatch retirement/replan path.
No independent transport, retry mechanism, resolver or operation journal is added.

Drift and selected-value completion compare only declared fields. Original task
roots/trees, source/owner/target bindings and one replaceable atomic application
receipt use the existing contracts. Matching imports/all-null intent neither write
nor mint ownership. Taskless ambiguity and source edits retain the original fence.
The full profile used for preservation is not exposed in status.

Immutable upstream references and current source/example fingerprints: [source manifest](telemetry-partial-preservation-sources.json).

## Qualification

- [Expanded regression](telemetry-partial-regression.log): thirteen new public cases
  cover wired, syslog, SNMP servers, collection enablement, collector selection,
  null fields, imports/all-null, missing companion state, an unselected-field
  pre-submit race, lost acknowledgement, acknowledged-source-to-null and unknown
  native-field refusal; includes
  existing network, DNS, empty resets, selection, task/receipt, AAA, IPAM,
  credential, reporting and wired dependency contracts. PASS: combined race
  suite, 73.366 seconds.
- [Private Kubernetes API](telemetry-partial-api.log): the same thirteen public cases
  store actual generations, ownership, journals and receipts with current CRDs;
  native responses and actor interruptions are fixtures. PASS: thirteen cases,
  11.893 seconds.
- [Native public cycle](telemetry-partial-native.log): a generated empty area with
  complete known telemetry seed, then three partial source revisions. Each checks
  all effective native fields/groups, original Unknown-task/root/tree recovery,
  latest receipt replacement, no-dispatch refusal retirement and repeated no-op.
  Kubernetes storage/restarts and injected GET faults are fixtures; Catalyst
  Center tasks and readback are real. PASS: three original public tasks, three
  no-dispatch refusal retirements, one latest application and independent complete
  effective-profile checks (51.303 seconds). Final hierarchy
  `Global/CVK-Q-20261017-042539-000000000` cleanup and absence verified. Setup
  includes three site creates, one floor update and one complete telemetry seed;
  these and cleanup deletes are separate from the three public partial updates.
- [Exact failed-probe cleanup](telemetry-partial-cleanup.log): original creator
  UUIDs, stable observed footprint, unchanged geometry, empty assignments, full
  delete tasks and final absence. It is supervised generated-fixture cleanup,
  not positive partial-profile convergence or public group destruction.
- [Foundation regression](telemetry-partial-foundations.log): ND, controller
  ownership, device operations, diagnostic, software lifecycle/upgrades and IOS XE
  GNOI/runtime packages pass (unchanged packages mostly cached).
- [Example compiler](telemetry-partial-example.log) validates
  `examples/configs/catalyst-center/telemetry-partial-apply.yaml`, which reuses the
  existing network-settings owner and requires known companion telemetry state.
- [Strict documentation build](telemetry-partial-docs.log).

The first preserving native harness read explicit groups as if they were always
present, although the appliance can optimize equal values into inheritance. That
harness failed before public mutations and its generated hierarchy was removed.
The final harness reuses the existing effective resolver, just like production.

## Explicit limitations

This is conservative resolved intent, not upstream default expansion. In
particular, omitted collector selection is preserved rather than synthesizing
the module expression's external collector choice; exact default/nullable
collector expansion remains pending. Unknown companion groups must first be
established through a complete profile. External collector/native data-plane
behavior and installed workers are not qualified by the Builtin lab fixture.

There is no permanent private baseline of unselected values. Pre-submit snapshots
and payload preservation are guarded, and the live test independently checks all
fields; a retained receipt does not prove that every unselected value stayed equal
to its original pre-submit baseline across an external post-dispatch edit/restart.
Full native baseline/field ownership, destruction/pruning, handoff, installed and
physical execution, broader schema/default parity and full pinned-model support
remain incomplete.
