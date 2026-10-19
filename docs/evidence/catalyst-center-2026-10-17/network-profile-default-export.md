# Explicit pinned network-profile default export

`controller-export-network` is an offline preprocessing command beside the existing
migration/report tooling. It does not add default expansion to runtime providers,
change their source contract, or use another transport, operation journal or
credentials channel. The output is a network-profile model, not a whole-module
Terraform export or a deployment request.

## Exact supported expressions

The unmodified 0.5.0 `defaults/defaults.yaml` and Apache-2.0 license are retained in
`nacmodel/upstream/0.5.0`. The exporter checks the immutable module version,
revision and defaults SHA before use. Scope is `network_settings.network` and
`network_settings.telemetry` plus existing site references. Unsupported sections,
credential/allocation definitions and ambiguous input are refused, not discarded.

- Scalar selection happens before fallback. Defaults for an absent/null NTP,
  DHCP, DNS-domain, timezone or banner field cannot create an extra resource.
- When the original profile selects DNS by a non-null domain, its missing
  `dns_servers` may use a declared network default. Explicit null does not fall
  back, matching Terraform `try()` rather than truthy coalescing.
- Missing telemetry attributes receive pinned defaults or a supported sparse
  user override. Explicit false/null values remain distinct. No absent list is
  fabricated as empty.
- Builtin collector selection suppresses external address/port fields, matching
  the module's conditional expressions. External selection requires resolved
  endpoint values; supported missing endpoint values can use user defaults.
- Explicit null collector selection and null default overrides are refused until
  their complete upstream expression/merge behavior is qualified. They are never
  silently turned into Builtin or an incomplete external collector.

Overrides use `defaults.catalyst_center.network_settings.{network,telemetry}`.
Only known, non-null profile defaults are accepted. This is one already-selected
stripe and one sparse override layer, not a reimplementation of arbitrary
`provider::utils::yaml_merge`, directory ordering or managed-site filtering.

## Private artifact and provenance

The CLI validates the materialized model with the existing strict network
compiler before creating a new mode-0600 output file. Existing files and symlinks
are refused. Errors and stdout never echo payload values. Stdout contains source,
override and output digests, module revision, defaults digest and an embedded
resolution-policy source digest. The policy identifier can populate the existing
`modelSource.exporter`; it is a policy-source identity, not a whole binary hash.

`executionSyntaxValidated` is true only after the actual offline compiler passes.
`leafSchemaValidated` and `configurationParity` remain false. A defaults digest,
module commit, policy digest or successful compilation is not a schema digest.
No schema attestation, customer source revision or mutation permission is invented.

The checked-in source/override examples are
`examples/configs/catalyst-center/network-defaults-source.yaml` and
`network-defaults-overrides.yaml`. They demonstrate an optional DNS fallback,
explicit false and a default NTP value that does not activate an unselected
resource. The output is not applied to the example's existing site.

Immutable upstream references, license and current source/example fingerprints:
[source manifest](network-profile-default-export-sources.json).

## Qualification

- [Offline model and migration-tool tests](network-defaults-offline.log): pinned
  defaults, predicate/fallback precedence, null/false, conditional collector
  suppression, external fallback, unsupported scopes, aliases/duplicates,
  limits, private files, no overwrite/symlink writes, safe errors and override-file
  integration; existing IOS XE migration/report behavior remains covered. PASS:
  model package 1.623 seconds, migration tool 1.957 seconds.
- [Private Kubernetes API](network-defaults-api.log): exported telemetry defaults
  and final policy metadata use actual CRD/config/controller/status storage through
  complete, original Unknown recovery, replacement, target replacement refusal and
  history refusal. Native API responses and actors are fixtures. No schema digest
  is synthesized from the defaults. PASS: all five cases, 10.197 seconds.
- [Native exported-profile lifecycle](network-defaults-native.log): two six-setting
  public revisions on a generated empty hierarchy. Initial input omits five
  telemetry booleans and preserves an explicit false; the offline exporter
  materializes pinned values before the existing compiler. Original no-dispatch
  retirements, original task-tree recovery, latest receipts and no-op use the same
  public runtime. This qualifies exported telemetry defaults and common native
  execution; it is not all-default/all-resource or installed-worker qualification.
  PASS: final policy identity, twelve original task recoveries, twelve one-use
  no-dispatch refusal retirements and six latest rows (168.344 seconds).
  Generated hierarchy `Global/CVK-Q-20261017-044817-000000000` cleanup and final absence verified.
  Setup contains three site creates and one floor update; those and cleanup
  deletes are separate from the twelve setting changes.
- [Current CLI example provenance](network-defaults-cli-provenance.json) and
  [artifact checks](network-defaults-cli-qualification.json): actual private CLI
  output, exact current policy/output hashes, 0600 permissions, correct selection
  and DNS fallback, explicit false, compilation true and schema/parity false.
- [Expanded native-runtime regression](network-defaults-regression.log): PASS,
  73.764 seconds; network, telemetry, task/receipt, null/reset, AAA, credentials,
  IPAM, reports and wired dependency contracts.
- [ND/GNOI/SWIM/controller foundation regression](network-defaults-foundations.log).
- [Strict documentation build](network-defaults-docs.log).

Native tests use real Catalyst Center task APIs and readback with generated empty
sites. Kubernetes storage, actor restarts, fault injection and Unknown checkpoints
are simulated. Setup/cleanup site tasks are separate from setting updates.

## Remaining scope

Other sections/resources, full canonical schema validation, arbitrary/multi-file
merge semantics, managed-site/global selection, explicit null collector/default
merge cases, file/template expansion, deployed-model import and complete exporter
parity remain pending. Unsupported runtime values still fail their existing
validators. Installed workers, permanent native baselines, field ownership,
destructive lifecycle and full 93-resource execution parity remain incomplete.
