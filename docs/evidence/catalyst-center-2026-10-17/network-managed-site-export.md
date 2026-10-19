# Pinned managed-site selection for network-profile export

The existing offline `controller-export-network` command now accepts the pinned
module's three site-selection variables. It expands profile defaults, validates
all declarations, projects the selected network/telemetry bindings, removes their
unused profiles and compiles the resulting source with the existing runtime
compiler. Selection runs before deployment; providers receive resolved intent.
There is no new runtime option channel, transport, journal, CRD or controller.

## Selection contract

The implementation follows immutable 0.5.0 `main.tf` local.sites,
`missing_managed_sites`, variable validation, and `cc_network_settings.tf` site
and Global predicates. The [source manifest](network-managed-site-export-sources.json)
records those upstream files and current implementation fingerprints.

- Default selection includes all declared non-Global sites and a declared Global
  binding. Global settings alone select only a declared Global binding.
- With managed sites, regular mode uses literal Terraform `startswith`. A request
  for `Global/Lab` includes `Global/Lab2` and `Global/Lab/B` when declared. This
  behavior is preserved rather than silently changed to path-segment semantics.
- Exact mode selects the explicit site list and requires a nonempty list. Every
  requested path must exist as a declared non-Global site. Global is controlled
  separately; `["Global"]` and missing prefixes fail the upstream presence rule.
- Repeated managed paths retain the module's membership semantics. With both
  Global and managed sites requested, Global and the requested site selection
  are combined. Explicit false remains false.

This scope uses already explicit canonical network-settings site references,
not arbitrary whole-model sites or native discovery. Existing parser limits
apply, including 100 source site references. The CLI bounds the managed-site
argument to 64 KiB and the resolver to 1,000 entries.

## Validation, empty scope and custody

Every source profile and site reference is checked before selection. Invalid
fields, unresolved excluded references and duplicate site identities fail; they
cannot be hidden by filtering. Default expansion retains its pinned semantics
and existing refusal boundaries. Unsupported sections are not discarded.

A zero selection emits explicit empty `sites` and `network_settings` maps. The
shared parser now accepts this empty resolved cohort, while still refusing
orphaned profiles, undefined references and malformed declarations. Report and
Apply both count zero and require no native API request. Empty sources can be
exported again without inventing a profile or changing the output.

Existing Retain rules preserve native values and latest application receipts.
Changing source scope cannot settle Accepted/Unknown tasks, erase a corrupt
Verified receipt, bypass invalid history, or transfer configuration ownership.
Re-selection uses the existing submission, original-task recovery and atomic
latest-receipt replacement. No adoption, deletion, prune or receipt-migration
permission is conferred by export or provenance.

The output uses the existing exclusive new-file/mode-0600 contract. Stdout
contains only provenance, a selection-options digest and selected binding count.
The composite exporter policy identity covers default policy, site selector and
projection source. It is not a schema or binary attestation. Output compilation
sets `executionSyntaxValidated`; `leafSchemaValidated` and `configurationParity`
remain false. See [operator usage](../../netascode-migration.md#catalyst-center-managed-site-selection)
and `examples/configs/catalyst-center/network-site-selection-source.yaml`.

## Qualification

[Final local qualification](network-managed-site-export-qualified.log) includes
nine pinned selection cases, six export scope combinations, zero-result Report/
Apply with no native requests, idempotent empty export, validation of excluded
source, CLI scope/error/private-output checks and 36 retained lifecycle cases.
The [actual Kubernetes API qualification](network-managed-site-export-api.log)
runs those 36 lifecycle cases for NTP, DHCP, DNS, timezone, banner and telemetry:
re-selection, Accepted/Unknown source change, corrupt Verified completion,
corrupt retained history and missing native site after scope exclusion.

The [actual Catalyst Center cycle](network-managed-site-export-native.log) exports
one exact generated empty area from a source also declaring Global, an excluded
sibling and an excluded descendant. Existing guarded public reconciliation then
executes six setting updates, recovers their original acknowledged Unknown tasks,
retains six latest applications, and verifies no replay. Source exclusion then
selects zero resources: eight reconciliations issue zero native requests, all six
application receipts remain and independent reads confirm unchanged values.
A subsequent revision recovers six new original tasks and atomically replaces
the same six latest records. Generated hierarchy cleanup is independently
verified. No physical device upgrade, PnP claim, assignment or installed-cluster
repair occurs. Kubernetes storage/actors remain simulated.

That live run passed in 157.366 seconds. Final source additionally accepts
repeated managed-site membership and idempotent empty-export input; these
boundary extensions were qualified locally and against the temporary Kubernetes
API after the native cycle. The live cycle uses one unique managed path and
nonempty export input. Native prefix-selected multi-target mutation ordering,
actual Global-setting writes and installed workers are not qualified by it.

[Broader network/credential regression](network-managed-site-export-regression.log),
[shared ND/lifecycle/GNOI foundation checks](network-managed-site-export-foundation.log)
and [strict documentation build](network-managed-site-export-docs.log) accompany
the evidence. No shared provider structures were changed.

## Remaining full-goal work

Selection currently projects only network/telemetry profile bindings. Whole-model
managed-site dependency closure, sites without explicit network references,
other resource sections, complete leaf schema, multi-file merge/default semantics,
resource-specific prune/ownership/handoff, installed actors and physical
qualification remain open. Existing private-baseline migration and key rollover
limits remain unchanged. The ledger retains all 93 upstream resource types;
this execution milestone does not mark full pinned-model parity or merge readiness.
