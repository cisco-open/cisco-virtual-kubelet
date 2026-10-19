# Catalyst Center source composition

`controller-merge` constructs an offline Catalyst Center source from explicit
ordered files. It follows NetAsCode 0.5.0's pinned
[merge expressions](https://github.com/netascode/terraform-catalystcenter-nac-catalystcenter/blob/a435555fe828f03cf3311d52fe1ab6572a247916/merge.tf)
and the minimum compatible utils provider 2.0.0
[merge implementation](https://github.com/netascode/terraform-provider-utils/blob/a383caa57652edc7952f3bd596f9a75719480643/internal/provider/merge.go).
The module permits newer utils versions; CVK records its explicit algorithm
baseline rather than claiming equivalence to every future provider release.

This command does not resolve defaults or create runtime authority. It preserves
all controller sections and user defaults; later section exporters validate and
resolve their supported inputs. Normal reconciliation still consumes explicit
resolved snapshots through existing ownership, task recovery and transport.

## Order and merge rules

- Directories are processed in the order supplied. Each directory's recursive
  `.yml` and `.yaml` paths are sorted lexicographically.
- Explicit `--yaml-file` inputs follow, in their supplied order. Files may contain
  YAML or JSON. An optional final positional model follows all files; `-` selects
  standard input explicitly. Flags may be repeated.
- Maps merge recursively; later non-null scalar values replace earlier values,
  including `false`, zero and empty strings. Null updates to existing map keys
  are ignored. Nulls inside a newly inserted map remain present.
- Lists of primitives concatenate, preserving duplicates. List maps merge into
  the first matching destination only when at least one primitive key is shared
  and **all shared primitive values match**. Matching is not name-only. If either
  list already contains matching map duplicates, the lists concatenate intact.
  Conflicting declarations are not silently repaired; subsequent strict parsers
  can reject their duplicate identities.

Read at most 128 single-document inputs and 1 MiB total. Directory symlinks and
nonregular YAML entries are refused. Inputs use the existing strict single-document,
mapping contract by default. The explicit `--resolve-yaml` source mode described
below resolves supported references and tags before strict runtime parsing.
Nonfinite numbers, recursive/undefined references, canonical duplicate keys,
unsupported compound tags and over-limit expansions remain refused. The runtime adapter and source library do not read the process environment;
the offline CLI reads only explicitly authorized bindings. No model value
causes a file or URL read. These
input-language limits remain explicit gaps relative to the provider's full YAML
loader, not evidence of complete source-model parity.

## Separate model and user defaults

Build the migration tool, then run the example using the same ordered sources:

```bash
go build -o /tmp/cvk-netascode-migrate ./tools/cvk-netascode-migrate
/tmp/cvk-netascode-migrate controller-merge \
  --model-version 0.5.0 --output /tmp/catc-merged-model.json \
  --component model \
  --yaml-directory examples/configs/catalyst-center/model-merge
/tmp/cvk-netascode-migrate controller-merge \
  --model-version 0.5.0 --output /tmp/catc-merged-defaults.json \
  --component defaults \
  --yaml-directory examples/configs/catalyst-center/model-merge
/tmp/cvk-netascode-migrate controller-export-sites \
  --model-version 0.5.0 --output /tmp/catc-resolved-model.json \
  --defaults /tmp/catc-merged-defaults.json /tmp/catc-merged-model.json
```

Compare the two merge reports' `inputSnapshotSHA256` values before using their
components together. They must match. This binds ordered raw and effective input
digests, source mode and policy, including any resolved environment values.
`orderedInputsSHA256` continues to identify source-file bytes alone and cannot
detect a changed environment binding. Each component is derived from the full
input merge, not from a separate filtered merge. `--component all` is the default
and preserves both root objects in one audit artifact. That combined unresolved
artifact is not directly a runtime source. The defaults component is refused
when no Catalyst Center defaults were supplied; no defaults are invented there.

All outputs are new mode-0600 files. Existing files and symlinks are not overwritten.
Standard output contains only non-value provenance: ordered input digests,
component, implementation pin and output digest. Neither input paths nor model
leaves are printed. `resolved` and `configurationParity` remain false for merging.
The site example resolves three sites, one planned AP and five missing defaults.
It makes no controller API calls.

For images, keep the asset index and BinaryData in the existing explicit
ConfigMap snapshot when replacing its model key with the resolved output. Use
[bound site selection](catalyst-center-site-defaults.md#managed-site-selection-with-retained-custody)
for managed-site scope. No image path gains filesystem or network access.

## Execution qualification

Shared model and CLI tests cover all eleven section envelopes, map/list/null
semantics, preserved defaults, input precedence, deterministic component
provenance, refusal and private artifacts. Separate public reconciliation and
real temporary Kubernetes API tests exercise merged hierarchy and image/AP
fragments through defaults, bound selection, existing private custody and
original-task recovery. Native qualification and remaining limits are recorded
in [composition evidence](../evidence/catalyst-center-2026-10-19/model-source-merge.md).

Installed multi-file source production, all input-language features, complete
leaf schema/catalogue validation and the remaining physical/bulk/destructive
resource flows are not established by offline merging. The
[93-resource ledger](catalyst-center-resource-parity.md) remains authoritative
for individual execution gaps.

## Explicit YAML source resolution

`--resolve-yaml` enables offline anchors, independent alias copies, merge-key
precedence and scalar tag handling using CVK's existing YAML library. No second
runtime parser is added. Maps merged through `<<` preserve the first inherited
value, while explicit keys override inherited keys. Scalar environment tags and
quoted strings starting with `!env ` follow the pinned utils behavior. Unknown
local scalar tags remain literal tagged strings; timestamps and binary scalars
remain strings. Subsequent section parsers still enforce their field types.

Environment access requires an additional `--env NAME` for **each** authorized
variable. There is no implicit process-environment access in the adapter or
source library. Missing, empty or unlisted bindings refuse before any output is
created. Bound values remain strings and are not recursively reinterpreted as
new tags. Variable names and values are omitted from provenance; only the used
binding count and snapshot digests are reported. Output remains mode0600.

```bash
export CVK_SOURCE_PARENT='Global/CVK-YAML-Design/Building'
/tmp/cvk-netascode-migrate controller-merge \
  --model-version 0.5.0 --output /tmp/catc-yaml-model.json \
  --component model --resolve-yaml --env CVK_SOURCE_PARENT \
  --yaml-file examples/configs/catalyst-center/model-merge-yaml/source.yaml
/tmp/cvk-netascode-migrate controller-merge \
  --model-version 0.5.0 --output /tmp/catc-yaml-defaults.json \
  --component defaults --resolve-yaml --env CVK_SOURCE_PARENT \
  --yaml-file examples/configs/catalyst-center/model-merge-yaml/source.yaml
/tmp/cvk-netascode-migrate controller-export-sites \
  --model-version 0.5.0 --output /tmp/catc-yaml-resolved.json \
  --defaults /tmp/catc-yaml-defaults.json /tmp/catc-yaml-model.json
```

Check matching `inputSnapshotSHA256` in both merge reports. The example is offline
and creates no controller definition. Model paths remain logical data; source
authoring cannot open files or URLs through an image/template/env value.

The pinned utils 2.0.0 reference runner generates the independent conformance
fixtures used by these tests. CVK still refuses ambiguous duplicate keys,
nonfinite values and unsafe expansion. Standard compound/extension tags and
provider/parser edge cases beyond those qualified fixtures are not claimed as
full YAML-loader equivalence. Runtime `ParseDocument` remains unchanged and refuses raw aliases. Environment
bindings and source-mode interpretation run only in the offline importer; use
the explicit resolved snapshot for runtime execution.
See [source-resolution qualification](../evidence/catalyst-center-2026-10-19/yaml-source-resolution.md).
