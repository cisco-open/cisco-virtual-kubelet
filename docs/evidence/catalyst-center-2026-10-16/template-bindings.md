# Controller-data binding metadata and deployment boundaries

The pinned module/provider pass variable `binding` as an optional opaque string.
The current upstream [CommonSettings example](https://netascode.cisco.com/docs/data_models/catalyst_center/templates/template/#example-11-template-variable-with-binding-and-selection-attributes)
uses a JSON string with runtime site/device placeholders, a filter and a selection
type without a static choice map. The pinned field mapping is recorded in the
existing [provider/module inspection](template-variable-metadata-upstream.json).
The live fixture follows that shape; this is not a new CVK binding schema.

## Native result

The [initial cycle](template-binding-native-initial.log) proves the existing
controller path already stores and commits the nonempty binding, changes its
filter, converges without another write, clears the binding, and restores it.
It passes in 181.08 seconds including exact generated cleanup.

The suspected static-choice validation gap did **not** occur: the appliance adds
an empty choice map when the source omits it. The existing validator accepts
omitted required values with a nonempty binding. No duplicate provider resolver,
CommonSettings fetcher, JSON rewrite or validation exception was necessary.

The [final cycle](template-binding-native-final.log) uses the shared fixture and
also checks that arbitrary explicit values cannot bypass the native choice map.
It records only choice counts, never binding strings, runtime values or rendered
configuration. Both native drafts and committed versions have empty choice maps
in these observations. Clearing explicitly sets `binding: ""`, makes the variable
optional and retains an empty SINGLE_SELECT choice map; this does not claim that
omitting selection metadata converts a dropdown to unrestricted text.

Every mutating transition uses the existing compiler, one-use sender, complete
task-tree observer, captured UUID/parent and draft/committed verifier. Four
transitions require draft update plus commit; the repeated filter requires zero
writes and an unchanged version. Exact generated template/project cleanup follows.
The final cycle passes in 182.82 seconds, including exact cleanup. No template
is deployed to a device.

Changing the stored filter from RADIUS to TACACS proves metadata execution only.
It does not prove either filter resolves configured controller data or authenticates
a device. Runtime site/device values remain null in authored binding metadata;
CVK does not populate or publish them.

## Shared execution, recovery and privacy checks

- [91 focused race subcases](template-binding-focused.log) cover omitted bound values,
  explicit choice membership, requiredness after clearing, malformed native
  representations, public restarted no-op reconciliation, native snapshot MACs,
  one-use submission and regular deployment submission/recovery fixtures. They
  pass in 4.312 seconds.
- Four binding edits—filter, runtime scope, raw string formatting and removal—
  remain significant to keyed native snapshots and cause pre-submit refusal
  with zero POSTs and the existing guard-bound no-dispatch proof. No private
  fingerprint or old receipt is normalized or rebased.
- Regular deployment fixtures submit only explicit parameters: the controller
  binding is not copied into the request. A late binding change blocks dispatch;
  accepted recovery observes the pinned version without reselecting latest or
  replaying the request. Product HTTP and physical authority are simulated here.
- [Eight real private Kubernetes API cases](template-binding-api.log) use the
  real CRDs and Kubernetes 1.35.0. The bound-source case survives three restarted
  reconciliations as InSync, exposes no binding content in config/controller
  status and acquires no ownership of unchanged imported Retain data. Product
  HTTP is simulated; this is not installed worker or real Lease qualification.
  The eight cases pass in 9.360 seconds including API startup/teardown.
- [Shared software-version fixture checks](template-binding-shared-fixture.log)
  cover existing users after changing the fake native response to deep-copy the
  desired intent. Native-added choice metadata can no longer edit the desired
  test object and hide a comparison defect.
- The existing template [example compiler](template-binding-focused.log) checks
  `examples/configs/catalyst-center/template-binding-execution.yaml` with the
  other manifests. [Source hashes](template-binding-sources.json),
  [strict docs](template-binding-docs.log) and the
  [93-resource scope audit](template-binding-ledger.log) accompany the work.

The production binding, transport, configuration journal, ownership, recovery,
parameter validator and ND/direct-device/SWIM foundations did not need changes.
The addition qualifies and demonstrates the existing shared paths rather than
adding another implementation of controller-data resolution.

## Remaining scope

Actual controller-data resolution, rendered output and deployment to a physically
admitted target still need live qualification with applicable site/controller
settings. Other binding providers/entities, bound defaults/static selections,
onboarding/composite binding metadata and installed actors remain pending.
The [SECTIONDIVIDER gap](template-divider-contract.md), public template destructive
lifecycle, scheduling and the rest of full pinned-model parity are unchanged and
remain open. This evidence does not mark reporting or metadata execution as full
deployment support.
