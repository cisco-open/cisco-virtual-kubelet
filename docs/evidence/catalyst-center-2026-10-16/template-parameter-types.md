# Template datatypes and optional metadata qualification

The pinned module maps parameter datatype, default, binding, description, display
name, hint, required/not-parameter/array flags and selections into the provider's
template parameter records. The existing [pinned source inspection](template-variable-metadata-upstream.json)
records the five datatype enums. This phase exercises native representations;
acceptance by the parser or a simulated API is not native support proof.

## Divider gap discovered

Two generated-template updates exposed a SECTIONDIVIDER interoperability gap:

- With the divider absent from template content, the task succeeded but native
  readback omitted the record. All four scalar records matched their requested
  managed metadata. See the [initial run](template-parameter-types-native-initial.log)
  and [safe shape inspection](template-parameter-types-inspection.log).
- With a content reference, readback retained its name but converted its datatype
  to STRING and replaced its flags and display metadata. The existing strict
  matcher again refused verification. See the [referenced probe](template-parameter-types-native-referenced-divider.log)
  and [safe differences](template-parameter-types-referenced-inspection.log).
- A single supervised PUT on the second generated fixture with
  `customParamsOrder=true` completed, but still returned STRING rather than the
  requested divider. See the [custom-order probe](template-divider-custom-order-probe.log).
  This was API research on a captured generated object, not a public CVK flow.

The [documented API schema](https://developer.cisco.com/docs/catalyst-center/2-3-7-4/update-template/)
includes SECTIONDIVIDER and customParamsOrder, but does not explain a working
divider representation for this lab. Its published release differs from the lab;
schema presence is not treated as evidence of working execution. These observations
do not prove that every supported API or appliance version rejects dividers.

Neither missing records nor datatype conversions were normalized away. Both
fixtures were retained first, then removed after original NCTP Create-task UUID
proofs, completed update/probe tasks, exact identities/content/parameter sets,
expected two-version history, empty PnP/profile consumer checks, completed Delete
tasks and final absence. See the [first cleanup](template-parameter-types-retained-cleanup.log)
and [second cleanup](template-parameter-types-referenced-cleanup.log).

SECTIONDIVIDER remains **unqualified**, including its native ordering/grouping
requirements. The separate probe stays available behind
`CATC_TEMPLATE_SECTION_DIVIDER=isolated-probe`; it must not be counted as a positive
support test while its verification fails.

Follow-up [native contract research](template-divider-contract.md) tests explicit
order and grouping fields without weakening dispatch or readback guards. All
three dispatched candidates still failed to preserve the divider. The opt-in
qualification remains failed; all generated fixtures were removed.

## Scalar metadata cycle

The independent [scalar cycle](template-parameter-types-scalars-native.log) uses
the existing compiler, single-use dispatcher, complete task-tree observer and
draft/committed version readers. Five transitions cover:

1. INTEGER, IPADDRESS, MACADDRESS and STRING with explicit empty binding, display/
   description/hint metadata and true/false not-parameter flags.
2. Changed display metadata, a required INTEGER and an address array.
3. Explicit empty defaults and display metadata, with required/array flags reset.
4. Repeated cleared intent with zero writes and unchanged committed version.
5. Restored defaults and metadata, followed by fresh-compiler convergence.

Actual native draft and committed declarations also pass read-only parameter
validation with omitted defaults/optional values, integer input, IPv6 input,
dotted MAC input and a two-address array. Fractional integers, invalid addresses/
MACs and supplying constant/undeclared parameters remain refused. No defaults or
bound values are synthesized into a deployment request. These checks do not
submit deployment or prove rendered device state. The full scalar cycle passes
in 181.39 seconds, including verified exact template/project cleanup.

Use `examples/configs/catalyst-center/template-parameter-types-execution.yaml`.
It intentionally omits the unqualified divider and contains inert comments only.

## Other verification

- [68 focused public/parser/report/apply/example subcases](template-parameter-types-focused.log)
  cover the existing metadata behavior plus the five-enum model in restarted
  public no-op reconciliation. These pass in 3.535 seconds; imported Retain data
  acquires no ownership.
- [Seven private API cases](template-parameter-types-api.log) use Kubernetes
  1.35.0 with the real CRDs and simulated product HTTP. This includes all five
  declared types, but does not claim SECTIONDIVIDER works natively. All seven
  cases pass in 9.526 seconds including API startup and teardown.
- [Example compilation](template-parameter-types-examples.log),
  [source hashes](template-parameter-types-sources.json),
  [strict docs](template-parameter-types-docs.log) and
  [93-resource scope audit](template-parameter-types-ledger.log) accompany the work.

No production transport, foundation, ownership rule, CRD, journal or ND/direct
device/SWIM code is changed in this phase. Native API mismatch continues to fence
verification. The earlier regression evidence remains separate from the focused
new qualification; no new installed-worker or physical-admission proof is claimed.

## Remaining scope

Resolving the divider representation is an open implementation/qualification
requirement. Nonempty controller-data bindings, not-parameter flag transitions,
other language/platform/field combinations, onboarding and composite variable
metadata, actual deployment/rendered state, scheduling and installed actors remain
pending. Public template prune/delete/recreation and full pinned-model parity
remain unfinished. This phase does not narrow the overall 93-resource objective.
