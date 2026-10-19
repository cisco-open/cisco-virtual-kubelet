# Wired bulk provisioning: resolved model context and site selection

This advances `catalystcenter_provision_devices` against NetAsCode module 0.5.0
(`a435555fe828f03cf3311d52fe1ab6572a247916`), especially `main.tf` site selection
and `cc_device_provision.tf` grouping. It does **not** close the resource's
public execution or physical-device convergence gates.

## Implemented

The existing private bulk lifecycle now accepts `inventory` with optional
`sites` dependency context in the same resolved `catalyst_center` source. Only
`inventory` is managed. Private projections reuse the existing inventory and
complete site/floor-image/planned-AP validators. Invalid context, unknown
sections, undeclared device sites, duplicate keys and unsupported attributes
are refused; source bytes are not rewritten.

Embedded site declarations are authoritative. An additional caller-supplied
site set must match exactly; native inventory cannot widen it. Inventory-only
private callers retain the explicit resolved-site-set contract. `Global` is
not implicitly added to the model's declared site set.

Bulk module inputs remain annotations, separate from the NetAsCode stripe:

| Annotation suffix after `cisco.vk/` | Meaning |
| --- | --- |
| `catalyst-center-bulk-devices` | Existing strict `true`/`false` bulk selection |
| `catalyst-center-managed-sites` | Existing JSON array of canonical declared site paths |
| `catalyst-center-bulk-site-provisioning` | Existing selected root used for grouping |
| `catalyst-center-bulk-manage-specific-sites-only` | Strict boolean; select exact managed paths rather than their descendants; requires a nonempty managed-site set |
| `catalyst-center-bulk-manage-global-settings` | Strict boolean; when true with no managed sites, select no site-bound device work |

The two new options require bulk selection, including when explicitly false.
Both default to false. Managed-site existence is checked even when an explicit
bulk root overrides grouping. The root itself must belong to the effective
selected site set. Root precedence skips overlapping-prefix grouping ambiguity,
but no longer skips declaration validation. Path matching preserves CVK's
existing segment boundary rule: `Global/Lab` cannot select `Global/Lab2`.
This is deliberately stricter than the module's raw string-prefix expression.
Mixed PROVISION/REPROVISION groups and deterministic chunks of at most ten
continue to use the existing planner and transport.

Floor-image dependency bytes use the existing index/BinaryData mechanism on
the **same ConfigMap** as the model. No filesystem paths or URLs are opened.
The same UID/resourceVersion fences asset-only changes. Dependency loading is
source-driven so removing or invalidating a submission option cannot discard
assets needed by retained acknowledged recovery.

Dispatch reconstruction and receipt declaration checks now use the same
source compiler. Original operation IDs, source bindings, group membership,
native/physical identities, shared permits and convergence requirements remain
in force. No alternate lease, journal, worker or provider was introduced.

## Verification

- [Bulk/source/site regression log](wired-bulk-model-regression.log): race-enabled
  bulk lifecycle, shared configuration source/journal, floor-image, site and
  planned-AP suites pass. The composed private cycle covers both POST and PUT
  with embedded context, preparation, dispatch, native task observation,
  simulated independent device proof, shared receipt publication, settlement
  and restart without replay. It also retains inventory-only coverage.
- [Public refusal log](wired-bulk-model-public-gate.log): valid combined-source
  selection still reports `BulkExecutionUnavailable`; invalid or disabled
  module options report `InvalidBulkOptions`. No native calls, owners,
  operations or application receipts are created.
- [Foundation regression log](wired-bulk-model-foundations.log): adapter registry,
  complete ND adapter, shared controller configuration, software lifecycle and
  software upgrade package suites pass. An initial invocation used nonexistent
  lifecycle/upgrade directory names and was corrected before this final run.
- [Native qualification log](wired-bulk-model-native.log):
  `TestLiveCatalystCenterResolvedWiredBulkRefusal` passes in 102.83 seconds
  (package 105.195 seconds), using the restored lab tunnel and fresh token
  authentication. All three live switch identities are read, an explicit
  inventory/sites fixture compiles without an external declaration set, and
  shared planning/preparation runs with simulated Kubernetes persistence.
  Missing permit and repeated preparation are refused. Public recovery refuses
  a deliberately pruned/untrusted simulated journal. Native provisioning
  records remain unchanged. A transport guard blocks every configuration
  mutation; attempted configuration writes total **zero**.

The native source fixture is deliberately constructed from a read-only catalog
snapshot for this qualification. Production does not synthesize model sites
from appliance state. The native test does not claim installed Kubernetes
workers, API-server persistence, accepted provisioning or real device proof.

## Remaining gates

The subsequent [worker-evidence stage](wired-bulk-worker-evidence.md) now reuses
the existing DeviceOperation envelope for complete, task-bound observations.
It deliberately does not supply the missing rendered-intent semantic verifier.

Public bulk preparation/submission remains unavailable. Independent, fresh,
operation-bound device configuration evidence is still required before enabling
the automatic group cycle; a successful task, provisioned-device record or Node
Ready condition cannot replace that proof. Installed actor qualification,
accepted native POST/PUT execution, deletion flags and ownership, template
composition and broader device/default semantics also remain pending. The
93-resource ledger and whole-section parity flags remain partial.
