# Nullable template metadata, scalar defaults and stable native parameter views

The templates compiler now accepts the pinned provider's nullable optional
parameter metadata and converts primitive defaults/choice values through the same
scalar helper already used for deployment parameters. Definition and deployment
validation share one data-type contract. This closes concrete resolved-model
compatibility gaps without another transport, CRD, secret store or journal.

## Model contract

The pinned [module parameter mapping](https://github.com/netascode/terraform-catalystcenter-nac-catalystcenter/blob/a435555fe828f03cf3311d52fe1ab6572a247916/cc_templates.tf#L392)
and [provider schema](https://github.com/CiscoDevNet/terraform-provider-catalystcenter/blob/01058637dd5ed4fca93e1b2a96dd7032aec2d3a1/internal/provider/resource_catalystcenter_template.go)
are recorded in the [source inspection](template-variable-metadata-upstream.json).

- Optional binding/default/description/display/hint metadata, boolean flags,
  selection type and the selection map may be explicit null. Null omits that
  attribute from the native payload; it does not define an independent field
  clearing protocol. Explicit empty defaults and empty maps remain distinct input.
- Default values and selection-map values accept primitive strings, booleans,
  integers and finite supported floats, producing native strings. Nested
  collections/objects and null entries inside a selection map are rejected.
  Defaults, choices and errors never publish parameter values into status.
- Variable data type is required and must be STRING, INTEGER, IPADDRESS,
  MACADDRESS or SECTIONDIVIDER. Optional selection type must be SINGLE_SELECT or
  MULTI_SELECT. Unknown types are rejected before prerequisite writes.
- Exact integer types and authored strings retain their precision. Integral
  floating-point values above the safe 2^53 range are refused rather than rounded;
  nonfinite values are refused. Quote extreme/high-precision values when exact
  text matters. This is not an arbitrary-precision numeric source decoder.
- Optional project/template descriptions and device series/type constraints also
  accept null. Null definition tag references are omitted as the module's filter
  does. Required language/software/device-family fields, null variable collections,
  null section tag collections and unknown fields remain rejected.

A runnable definition-only example is
`examples/configs/catalyst-center/template-variable-metadata-execution.yaml`.
Other defaults remain resolved upstream. No new source format or implicit
credential/value store is introduced.

## Native order defect found during qualification

The first native variable draft update succeeded, but a subsequent commit plan
was refused as stale before dispatch. Read-only inspection found that the
appliance permutes the three parameter records while `customParamsOrder=false`.
Sorting by unique parameterName made six complete observations identical; no
field/value or record identity needed to be removed. See the
[safe inspection](template-variable-order-inspection.log). This normalization
rule is an inference qualified against those lab observations, not a claim about
all appliance versions or unknown ordering modes.

CVK now uses one exact native JSON decoder for draft and committed views as well
as keyed snapshot input. Only an explicit false custom-order flag enables sorting
`templateParams` by unique nonempty parameterName. True, missing or null flags
preserve order. Malformed flags, records or duplicate identities refuse decoding.
Every record field, default, ID, choice, opaque subtree and native number remains
significant. Unknown integer fields retain JSON-number precision in submission
observations too; a change above 2^53 cannot disappear through float conversion.

Custom-order changes, parameter IDs, opaque/value edits and changed numbers still
refuse dispatch and change keyed comparisons. This does not apply software-version
empty/null convergence rules to keyed snapshots. Older mismatching fingerprints
remain fenced; no retained receipt is rewritten or silently rebased.

The generated fixture from the refused run was retained first, then removed after
original project/template NCTP Create-task UUID proofs, completed draft task,
expected seven-version history, exact identities/content, empty PnP/profile
consumer checks and verified deletion tasks/absence. See
[retained fixture cleanup](template-variable-retained-cleanup.log). The uncertain
request was not blindly replayed.

## Qualification

- [88 focused race cases](template-variable-metadata-tests.log) pass in 3.594
  seconds, covering nullable/resolved inputs, scalar conversion, required enums,
  precise integers, public no-op/refusal routes, optional definition fields,
  conditional ordering, keyed comparisons and pre-submit snapshot changes.
  Meaningful value/ID/opaque/custom-order/native-number edits remain refused;
  repeated submission of the spent write is denied.
- [Five real private Kubernetes API cases](template-variable-metadata-api.log)
  pass in 8.585 seconds on an isolated Kubernetes 1.35.0 API server with the real
  CRDs. Nullable definition/variable metadata and scalar defaults converge InSync;
  invalid type/selection declarations fail before native observation. Product HTTP
  is simulated; these are not installed actor or physical admission tests.
- [Complete native cycle](template-variable-metadata-native.log) passes in 276.25
  seconds. The shared metadata qualification loop rechecks all eight prior
  software-version transitions, then writes and commits three variables with
  numeric/boolean/decimal defaults and choices, changes those defaults, and proves
  equivalent string inputs/omitted nullable attributes cause no additional write
  or version. Every task tree, captured UUID/parent, draft/committed readback and
  version progression is verified. Exact template/project cleanup passes.
  These are real native compiler/task operations; no deployment or device upgrade
  is submitted, and an installed public worker is not implied.
- [Broad template/configuration/PnP/profile regressions](template-variable-metadata-regression.log)
  pass in 173.057 seconds; [shared controller/ND/software lifecycle checks](template-variable-metadata-foundations.log)
  pass. The new and existing template examples [compile](template-variable-metadata-examples.log).
- [Strict docs](template-variable-metadata-docs.log), whitespace/format checks and
  the [93-resource scope audit](template-variable-metadata-ledger.log) accompany
  the implementation. Scope rows are not a full execution-parity claim.

## Remaining scope

Follow-up [reset qualification](template-variable-resets.md) closes explicit
STRING default/choice clearing, restoration and individual choice removal for the
regular-template path. Choice maps now compare completely, so retained labels
cannot be mistaken for convergence. Other optional-field reset combinations
remain outside that bounded qualification.

Clearing/reset combinations for every optional parameter, full field/language/
platform combinations, arbitrary numeric source precision, composite/onboarding
variable live qualification and installed worker execution remain pending. The
native variable test uses INTEGER and STRING declarations with SINGLE_SELECT
choices; other shared enums have fixture coverage only in this phase.

Complete operational deployment/scheduling coverage and public template prune/
delete/recreation remain separate unfinished requirements. No destructive
permission, physical reservation shortcut, implicit adoption or automatic receipt
migration is added. The full pinned Catalyst Center parity goal remains active.
