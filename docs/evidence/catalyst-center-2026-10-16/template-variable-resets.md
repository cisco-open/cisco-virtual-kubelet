# Explicit template default and choice resets

The pinned NetAsCode 0.5.0 module maps `default_value` and `data_values` to the
provider's optional string and map-of-strings fields. Empty values are sent when
explicitly declared; null attributes are omitted. The upstream source pins and
field mappings are unchanged from the [metadata source inspection](template-variable-metadata-upstream.json).

## Corrected execution behavior

Parameter records contain server-owned metadata and use managed-field comparison.
That comparison previously also treated `selectionValues` as a subset. Therefore
an empty choice map matched an old nonempty map, and removing a label could be
mistaken for convergence. The parameter-specific comparator now requires exact
choice-map equality, including labels and converted values. Native metadata
outside that map remains unmanaged. Optional omitted/null fields are not promoted
to reset requests.

The shared definition matcher is used by draft drift reporting, apply planning
and post-task verification for drafts and committed versions. A completed task
cannot verify a reset while old choices/defaults remain. A changed committed body
still requires a commit even if the draft already matches. Definition reports
currently inspect the draft only; the new tests explicitly distinguish that
reporting scope from committed apply convergence.

The existing compiler, transport, one-use submission guard, native task observer
and version flow perform the update. No alternate mutation API, CRD, journal,
ownership adoption, receipt migration, deletion permission or worker bypass is
introduced. Private native fingerprints retain the complete choice map and all
other fields; no new empty/null equivalence is applied to those fingerprints.

## Qualification

- [45 focused race subcases](template-variable-reset-focused.log) cover exact empty and
  nonempty maps, retained/removed/changed choices, native extra metadata, unmanaged
  choices, defaults and report/plan/post-task verification. Public no-op tests
  restart reconciliation three times and acquire no ownership of imported data.
- [Private Kubernetes API tests](template-variable-reset-api.log) use the real
  CRDs with Kubernetes 1.35.0 and simulated product HTTP. Six cases cover nullable,
  scalar, cleared and invalid declarations. The six cases pass in 9.162 seconds including API startup/teardown. This is not installed-worker proof.
- [Native cycle](template-variable-reset-native.log) uses one generated project
  and template. Seven variable transitions cover typed defaults, updated defaults,
  equivalent string input, explicit empty STRING defaults and empty choices,
  repeated cleared no-op, restoration and individual choice removal. Mutating
  transitions require a verified draft update and commit, with version advancement;
  no-op transitions require zero writes and unchanged version. Every task tree
  and captured target/parent is checked, followed by exact generated cleanup.
  The cycle passes in 212.12 seconds, including verified template/project removal.
  No template is deployed to a device and no switch operation is performed.
- [Regression checks](template-variable-reset-regression.log) cover templates,
  configuration, PnP and network/wireless profiles. [Foundation checks](template-variable-reset-foundations.log)
  cover the shared adapter, ND, controller configuration, software lifecycle and
  direct-device upgrade packages.
- [Strict documentation](template-variable-reset-docs.log) and the
  [93-resource ledger audit](template-variable-reset-ledger.log) accompany this
  bounded change. [Source hashes](template-variable-reset-sources.json) identify the
  tested implementation. The existing manifest [compiles](template-variable-reset-examples.log).
  Ledger rows are not evidence of complete resource parity.

Use the existing
`examples/configs/catalyst-center/template-variable-metadata-execution.yaml`.
For a STRING variable, `default_value: ""` clears the default; `data_values: {}`
clears all choices. Declaring a smaller nonempty `data_values` map removes the
omitted labels. Do not use null to request these resets.

## Remaining scope

Follow-up [selection compatibility](template-selection-compatibility.md) adds
empty-choice deployment validation and native SINGLE_SELECT/MULTI_SELECT mode
transitions with read-only parameter checks. It does not prove actual device
deployment or close the other reset combinations below.

Clearing INTEGER/IPADDRESS/MACADDRESS defaults, selection-type changes and other
optional metadata reset combinations still need native qualification. Full
language/platform/field combinations, composite/onboarding variables, public
destructive lifecycle, deployment/scheduling and installed worker execution
remain separate unfinished work. The full pinned-model goal remains active.
