# Credential definition and site-assignment selection

`controller-export-credentials` now accepts the shared managed-site/global/exact
selection options. It reuses the credential defaults exporter, pinned site
selector, strict network reference parser and ordinary assignment compiler.
Global/single-state modes retain credential definitions. Per-site mode emits only
selected assignments and leaves definition creation absent. Other model sections,
geometry, invalid excluded declarations and null assignment removal are refused.
No runtime writer, transport, Secret resolver or ownership protocol was added.

Final regressions pass **53 families/269 subcases** across credential execution,
selection, existing defaults and CLI behavior. The selected six-slot public
manifest/restart/removal-guard flow passes with an actual private Kubernetes API
(**10.000s**); its native service is simulated. An initial installer callback
signature compile error was corrected before API execution.

Native qualification passes all **six credential families** in **61.812s**. Fixture
setup creates/rotates isolated definitions and a new empty area. For each family,
the selected consumer source contains no definitions and exactly one named
assignment; the excluded reference is not resolved or applied. The existing
assignment executor submits, independently reads back and reaches no-op.
Supervised cleanup disables that area's slot, removes the generated definition
and finally removes the empty area. All six original credential identity/count
baselines remain preserved. These fixture setup/cleanup mutations are separate
from selected assignment execution; there is no claim of zero global mutations
across the entire fixture run.

ND (**5.076s**), GNOI/SWIM (**6.485s**) and production build pass. CLI demonstration
writes one selected assignment and zero definitions to a new mode-0600 file whose
hash matches provenance. The binary was not installed. Native qualification uses
the assignment executor directly; public reconciliation is separately API-tested.
No installed selector-driven, physical authentication, parallel per-site authority,
implicit Global-slot fallback or full nested schema/model parity is claimed.
The existing global network-settings owner remains required. Geometry/default
merging, inheritance removal and destructive credential lifecycle remain pending.

See [operator contract](../../controllers/catalyst-center-credential-defaults.md),
[source/provenance/scope record](credential-selection-validation.json),
[regression](credential-selection-regression.log),
[private API](credential-selection-api.log),
[native lifecycle](credential-selection-native.log),
[ND](credential-selection-nd.log),
[upgrades](credential-selection-upgrades.log) and
[build](credential-selection-build.log).
