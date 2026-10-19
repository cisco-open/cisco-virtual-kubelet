# Empty selections and native variable deployment compatibility

The definition reset work exposed a mismatch in the shared deployment validator:
an explicitly empty `selectionValues` map was rejected as a malformed native
response. Thus an optional variable could be successfully cleared and committed,
but CVK could not prepare deployment even when that variable was omitted.

## Implementation

The existing selection reader now accepts an explicit empty map with a valid
SINGLE_SELECT or MULTI_SELECT type. It retains an empty allowed-value set. This
allows omitted optional variables; it does **not** convert an empty dropdown into
an unrestricted free-text parameter. Supplied nonempty values and even an empty
string fail membership validation. An empty list is allowed only by the existing
optional MULTI_SELECT cardinality rule. Required declarations still require a
value, binding or native default. Existing native defaults are not copied into a
request or claimed to be validated through device execution.

Missing/null/non-map choice collections, unknown types, malformed default
selections, default selections outside the choices and duplicate values remain refused.
The parser still bounds choice/default collections. Names, choices and defaults
remain private; errors contain no values.

This is one change in the existing shared parameter reader. Regular dependencies,
composite member validation and inventory preflight reuse that reader. There is
no alternative transport, new journal, CRD change, physical admission bypass,
receipt migration or ownership adoption.

## Qualification

- [213 focused race subcases](template-selection-compat-focused.log) pass in
  4.186 seconds and cover empty and
  populated choices, requiredness, cardinality, default selections, types,
  malformed representations, native dependency snapshots and regular submission/
  recovery. A changed choice map invalidates the prepared request before POST;
  accepted recovery uses the captured version without replay or selecting latest.
  All product HTTP is simulated in these tests.
- [Two manifest cycles](template-selection-compat-manifest.log) exercise regular
  and composite inventory declarations through the existing compiler, journal and
  reconciler restart flow. Optional cleared selections do not synthesize request
  parameters. Kubernetes persistence, manager/worker reservations and product HTTP
  are fixtures; these are not installed worker or real Lease proofs.
- [Native qualification](template-selection-compat-native.log) uses a generated
  project/template and the existing compiler/task/version loop. Ten variable
  transitions cover typed/updated defaults, equivalent strings, clearing and
  repeated cleared no-op, restoring/removing choices, MULTI_SELECT, repeated
  MULTI_SELECT no-op and switching back to SINGLE_SELECT. Both draft and committed
  native declarations pass read-only parameter validation with omitted defaults/
  optional values and explicit valid values; cleared maps refuse explicit values.
  Multi-selection validates two declared values together. Each mutating transition
  verifies its task trees, captured UUID/parent and version progression; no-op
  transitions retain the version. Exact generated cleanup is included.
  The complete generated cycle passes in 268.49 seconds.
  No deploy request or device operation is submitted to the lab.
- [Broad regressions](template-selection-compat-regression.log) and
  [shared foundation/ND/software lifecycle checks](template-selection-compat-foundations.log)
  accompany the change. [Strict docs](template-selection-compat-docs.log),
  [source hashes](template-selection-compat-sources.json) and the
  [93-resource scope audit](template-selection-compat-ledger.log) are recorded.

The pinned model/provider mappings are unchanged; see the existing
[upstream source inspection](template-variable-metadata-upstream.json). This
change does not add provider-only ranges or default-selected-value inputs to the
pinned module's variable model.

## Remaining scope

Actual deployment of cleared or multi-select variables, rendered device-state
proof, scheduling and installed worker qualification remain pending. The native
cycle proves controller metadata execution and read-only deployment validation,
not appliance deployment acceptance or switch convergence. Composite metadata
mode changes, onboarding variables, additional datatypes and other optional-field
reset combinations still need dedicated live qualification. Template public
prune/delete/recreation and full pinned-model parity remain unfinished.
