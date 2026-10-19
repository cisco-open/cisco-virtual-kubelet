# Pinned site-default export

The new explicit offline export feeds resolved site geometry into the existing
public site lifecycle. It reuses the shared strict defaults decoder and artifact
writer; there is no new transport, controller executor, ownership mechanism,
CRD or runtime default injection. Network and credential exporters retain their
existing API through a provenance type alias. Their policy stamps now also bind
the extracted shared defaults decoder.

Pinned expressions are retained unmodified as `nacmodel/upstream/0.5.0/cc_sites.tf`,
SHA-256 `8d7f003049677a761a8206e1c1a8fa49f5871886fef61c5e46316c433b88cba9`,
under the existing upstream license/notice. The resolver respects the difference
between singular and bulk area selection and the numeric/null floor fallback.
It validates all supported overrides even when unused or shadowed, then checks
the complete resolved output with the current product parser.

Focused race tests, migration CLI privacy/refusal tests and the full temporary
API public create/prune/recreate lifecycle have passed. Native qualification and the broader shared regression have completed successfully.
The full model remains incomplete. Image/AP assets, multi-file/managed-site
selection and unqualified installed default-export behavior remain out of scope.

## Native qualification

One isolated area → building → floor hierarchy was driven through the ordinary
public reconciler using default-resolved source revisions and a generated private
comparison key. The cycle created three sites, pruned them leaf first, recreated
all three with replacement UUIDs and authenticated new-parent bindings, then
pruned all three again. It used six Creates and six Deletes, recovered twelve
original acknowledged tasks from Unknown, retained three archived cohorts and
finished with twelve action rows, one owner and no active operation. The typed
site inventory exactly matched its initial baseline. The generated hierarchy is
absent; no switches or existing sites were changed.

The five omitted floor defaults were resolved before each nonempty source
revision. Empty prune revisions resolved to zero resources and preserved
historical ownership. No runtime defaulting or fabricated native receipt was
used. The native run uses simulated Kubernetes persistence; a separate real
API-server run proves storage/schema and public lifecycle behavior. They do not
prove installed worker execution of this new exporter path.

The [native log](site-defaults-export-native.log),
[temporary API log](site-defaults-export-api.log),
[focused race log](site-defaults-export-race.log) and
[machine evidence](site-defaults-export.json) retain scope and provenance.
Private key projections and API credentials are not copied into this repository.
The completed private checkpoint is historical and must not be replayed.

## Final gates

- Broad regression: 19411 passing test events across 16 packages, no failures.
- Focused race tests: 38 passing events across the adapter and migration tool; the shared parser package had no matching focused test name and is covered by its complete parser and broad regression gates.
- Actual temporary API-server public lifecycle: passed.
- Native default-resolved lifecycle: passed under race checks, twelve distinct original tasks and restored typed baseline.
- Native mutation allowlist: passed with mocked transport; production and migration binaries built.
- Strict documentation, source fingerprint, formatting, example private-file/provenance and 93-row coverage checks: passed.

The full pinned model remains incomplete. This qualification covers explicit
already-selected geometry-only default export, not multi-file resolution,
managed-site/image/AP composition, installed execution of this exporter path,
whole nested schema validation or all operational/provider capabilities.
The shared decoder extraction changes offline export policy stamps; existing
runtime model parsing and native task/ownership contracts are unchanged.

Subsequent [design-default composition](site-defaults-design.md) adds planned AP
and explicit ConfigMap image snapshots. The geometry-only source hashes and
qualification boundary above remain historical; installed and complete-model
parity are still not claimed.
