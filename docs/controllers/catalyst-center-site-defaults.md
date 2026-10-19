# Catalyst Center site defaults

`controller-export-sites` resolves missing site geometry before normal manifest
execution. It uses the shared strict defaults decoder and pinned NetAsCode 0.5.0
bundle and validates output with the existing site parser and execution checks.
The runtime parser remains strict; no default resolver is added to reconciliation.
Planned AP declarations can accompany the geometry. Floor images require an
explicit ConfigMap snapshot containing the model, asset index and BinaryData.

Build the migration tool and export a new private file:

```bash
go build -o /tmp/cvk-netascode-migrate ./tools/cvk-netascode-migrate
/tmp/cvk-netascode-migrate controller-export-sites \
  --model-version 0.5.0 --output /tmp/catc-sites-resolved.json \
  examples/configs/catalyst-center/site-defaults-source.yaml
```

The output file is mode 0600. Existing paths and output symlinks are refused.
Standard output contains provenance, counts and digests rather than model values;
errors do not include source leaves. Optional `--defaults` accepts an explicit
site-only override file, such as
`examples/configs/catalyst-center/site-defaults-overrides.yaml`.

Use the resulting model as the source of the existing sites owner, with
`modelSource.format: netascode-catalyst-center`, `modelVersion: 0.5.0`,
`resolved: true`, and the emitted `exporter` value. This command makes no API
calls, creates no CR, and grants no adoption or deletion authority. Site task
recovery, private comparison keys and lifecycle receipts retain their existing
contracts. Do not introduce a second competing sites owner.

## Combined floor image and planned AP source

Read one explicit offline ConfigMap snapshot:

```bash
/tmp/cvk-netascode-migrate controller-export-sites \
  --model-version 0.5.0 --output /tmp/catc-design-resolved.json \
  --source-configmap examples/configs/catalyst-center/site-defaults-design-snapshot.yaml \
  --model-key model.yaml
```

The snapshot must be one `v1/ConfigMap` YAML or JSON document. Use
`--source-configmap -` for standard input; this form has no positional model
input. The shared loader resolves each `image_path` through
`cvk-floor-image-assets.json` and that same ConfigMap's BinaryData, applying its
normal count, total-byte, filename/MIME and duplicate/collision checks. Model
paths remain logical names and grant no local-file or URL reads. Unknown snapshot
fields, invalid unused indexed assets and missing selected bytes are refused.

The private output is the **resolved model**. Store it in the selected Data key
while preserving the ConfigMap's asset index and BinaryData, and reference that
ConfigMap from the existing sites owner. Planned AP and image declarations remain
in the output. Provenance reports their counts, asset validation and opaque
input/asset snapshot digests separately from the resolved model digest; it prints
no leaf values or image bytes.

AP coordinates are checked against the resolved declared floor dimensions using
the existing rounding/unit rules. Creation-only requirements, native model and
antenna catalogue checks, current floor/AP identities, private baselines and
write authority remain with normal runtime preflight. A null `image_path` retains
the pinned module's no-image-selection semantics and stays null in the output.
An offline digest describes the supplied input; it does not replace runtime
ConfigMap UID/RV binding or fresh asset/native validation.

## Expression semantics

The exporter is bound to the immutable
[site expressions](https://github.com/netascode/terraform-catalystcenter-nac-catalystcenter/blob/a435555fe828f03cf3311d52fe1ab6572a247916/cc_sites.tf)
and the shared defaults bundle. Provenance reports their digests separately from
the export policy; none is a leaf-schema digest.

| Input | Export behavior |
| --- | --- |
| Missing floor width/length/height/RF model/units | Use pinned defaults: 30/30/3, Cubes And Walled Offices, feet |
| Explicit supported value | Preserve it ahead of an override or pinned default |
| Null numeric dimension with a default | Use the fallback, as upstream numeric multiplication inside `try()` does |
| Null RF model, units or parent | Refuse an unexecutable resolved model; do not substitute a missing-field default |
| Missing singular area parent | Refuse; the singular resource selection does not use the bulk fallback |
| Missing bulk area parent | With explicit `--use-bulk-api`, resolve literal Global |
| Missing floor number | Require explicit input or a supported override |

The bulk flag selects upstream expression semantics. Match it to the config's
`cisco.vk/catalyst-center-bulk-sites` annotation when using bulk execution; the
export command does not create or edit that config. Non-Global area parent
fallback overrides are refused because the upstream bulk count and resource-map
expressions use different fallbacks. Other unresolved types also remain refused.

Overrides are limited to floor geometry/number, building country/address/location
and the compatible Global area fallback. Invalid unused or shadowed overrides
are rejected before output. Duplicate keys, aliases, multiple documents,
unknown fields, unrelated sections and over-limit input are refused by the
shared decoders and existing execution parser.

## Scope and qualification

Input must be one sites-only model. Multi-file merging and other controller
sections are not resolved by this command. Managed-site selection requires the
bound ConfigMap output described below. Planned APs are syntax/extent checked; floor images use only explicitly
supplied same-ConfigMap snapshots. Native catalogue and complete remote-image
byte-readback validation are separate from offline export. The emitted syntax-validation result is separate from
leaf-schema validation, native identity observation and complete model parity.

The existing public lifecycle tests cover inline and ConfigMap revisions with
resolved defaults, original acknowledged-task recovery, leaf-first prune and
parent-first recreation. Temporary Kubernetes API persistence is tested
separately from native controller execution. See
[qualification evidence](../evidence/catalyst-center-2026-10-19/site-defaults-export.md)
for completed gates and remaining installed or device-dependent limits.

Combined image/AP/default sources now pass public local and real temporary-API
lifecycle/recovery tests. A separate native run qualifies image revisions, AP
create/update, later geometry correction, no replay and exact generated cleanup
with simulated Kubernetes persistence. See
[design composition evidence](../evidence/catalyst-center-2026-10-19/site-defaults-design.md).
Installed composition, native catalogue/schema parity and destructive AP/image
lifecycle remain unqualified by this exporter change.

## Managed-site selection with retained custody

Export the complete ConfigMap with selection bound to the same source revision:

```bash
/tmp/cvk-netascode-migrate controller-export-sites \
  --model-version 0.5.0 --output /tmp/catc-selected-configmap.json \
  --source-configmap examples/configs/catalyst-center/site-defaults-design-snapshot.yaml \
  --model-key model.yaml --output-configmap \
  --managed-sites '["Global/CVK-Default-Design/Building/Floor"]' \
  --manage-specific-sites-only
```

This private output is a complete ConfigMap, including its asset index and
BinaryData. Its model key contains the versioned `cvk_site_source` envelope:
`version: sites-selection-v1`, the full resolved NetAsCode `model`, and
`selection`. The inner model preserves the pinned upstream structure. Older
strict workers reject this envelope. Updating the model or selector changes the
same ConfigMap resource version; inline envelopes use the config generation.
Normal source freshness, task recovery and owner checks still apply.

The selector uses the shared pinned selection policy. `--managed-sites` accepts a
JSON array of declared hierarchy paths. Prefix selection follows literal
`startswith` semantics, so a `Lab` prefix also selects `Lab2`. Exact selection
requires `--manage-specific-sites-only`; it does not create ancestor declarations.
Selecting a floor alone therefore requires its native parents to exist.
`--manage-global-settings` with no managed sites selects Global settings only and
executes no site definitions. All declarations and image assets are validated
before selection, including excluded ones.

The existing sites owner remains global and unique. Selection narrows execution;
it grants no additional ownership or adoption authority. Excluded owned sites,
AP receipts and image receipts stay retained. Under `Prune: Delete`, an omitted
owned target can be retired only when its hierarchy is in the selected scope,
with the existing identity, consumer and private-baseline checks. Excluding an
image is not a declaration omission and must not retag its receipt. A genuine
omission within the selected scope keeps the existing omission lifecycle.

Provenance distinguishes full resolved counts from selected counts and records
both model and output-artifact digests. The default raw-model export remains
available without selection. Selection flags require bound ConfigMap output;
re-exporting a bound source preserves its selector unless explicitly replaced.
A raw-model export refuses a selected envelope rather than discarding scope.

Explicit multi-file construction is available before defaults resolution through
[controller-merge](catalyst-center-model-merge.md). The site exporter itself
still reads one selected model or one explicit ConfigMap snapshot.
