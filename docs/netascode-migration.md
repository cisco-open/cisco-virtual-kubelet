# NetAsCode Migration to CVK

This guide describes the production path for moving an IOS-XE device from a
Terraform NetAsCode workflow to CVK `IOSXEConfig` reconciliation.

## Contract

CVK consumes canonical IOS-XE NetAsCode data as intent. It does not consume
Terraform state, Terraform provider resources, or unexpanded inheritance. For a
production migration, export a resolved NetAsCode model first and import the
per-device configuration into CVK.

The imported `IOSXEConfig` should carry:

- `spec.modelSource.format: netascode-iosxe`
- `spec.modelSource.resolved: true`
- `spec.modelSource.exporter`: the source pipeline/tool
- `spec.modelSource.sourceRevision`: the customer Git SHA, plan ID, or other
  immutable source marker
- `spec.driftPolicy: report` for the initial parallel run

The resolver rejects `spec.modelSource.resolved: false`. That is intentional:
CVK owns reconciliation and YANG translation, while the NetAsCode pipeline owns
model expansion.

## Generate a Migration Candidate

Inspect a resolved NetAsCode file:

```bash
make netascode-migrate ARGS="--device edge-01 path/to/resolved.nac.yaml"
```

Emit a report-mode `IOSXEConfig`:

```bash
make netascode-migrate ARGS="emit-cr \
  --device edge-01 \
  --name edge-01-nac \
  --namespace network \
  --target-yang-version 1718 \
  --model-version 1.2.3 \
  --exporter 'terraform-iosxe-nac-iosxe write_model_file' \
  --source-revision 4fd62c1 \
  path/to/resolved.nac.yaml" > edge-01-iosxeconfig.yaml
```

Useful safety flags:

- `--strict`: fail if the source contains families CVK does not manage.
- `--drop-unsupported`: omit unsupported families from `spec.source.inline`.
- `--drift-policy report`: default; observe without mutating.
- `--drift-policy revert`: enforce intent after the cutover is approved.
- `--atomic-replace`: make selected families authoritative when used with
  transactional apply.
- `--confirm-timeout-seconds 30`: use confirmed-commit protection on NETCONF.

For full `iosxe.devices[]` files containing more than one configurable device,
`--device` is required. This avoids accidentally merging several customer
devices into one Kubernetes object.

## Cutover Runbook

1. Freeze Terraform applies for the target device or target families.
2. Export a resolved NetAsCode model from the existing pipeline.
3. Generate the candidate `IOSXEConfig`.
4. Apply the CR with `driftPolicy: report`.
5. Watch status:

   ```bash
   kubectl get iosxecfg -n network edge-01-nac -o wide
   kubectl describe iosxeconfig -n network edge-01-nac
   kubectl get iosxeconfig -n network edge-01-nac -o yaml
   ```

6. Review `.status.drift` and `.status.familyStatus`.
7. Remove the selected families from Terraform ownership.
8. Change CVK to `driftPolicy: revert` for those families.
9. Keep `CONFIG_YANG_VALIDATION=warn` during first deployment, then move to
   `strict` after release/family validation coverage is certified.

## Ownership Rules

`spec.managedFamilies` is the ownership boundary. CVK reads the whole resolved
configuration but only plans and applies families in that list. During a
migration, start with a narrow family list and expand it after each family is
clean.

Never run Terraform and CVK as active writers for the same family on the same
device. Use Terraform to retain ownership of unsupported or not-yet-cut-over
families until CVK has matching writer and validation coverage.

## Release Handling

`spec.targetYangVersion` may pin the YANG release used by CVK writers. If it is
empty, the per-device reconciler selects the release from the device software
version map. The NetAsCode data model remains stable; release differences are
handled inside CVK writers, override tables, and the YANG validation boundary.

## Catalyst Center coverage assessment

For the controller-centric model, use the same tool's `controller-report`
subcommand. It reports section coverage without contacting Catalyst Center or
emitting configuration values. See [Catalyst Center parity](controllers/catalyst-center-parity.md)
for the pinned model, usage, exit codes and configuration implementation sequence.
The IOS-XE migration and reconciliation workflow above is unchanged.

### Check Catalyst Center template references

Add `--check-template-references` to `controller-report --model-version 0.5.0`
to validate the template identity and composite-member graph. This checks duplicate
project/template identities, ambiguous or missing members, repeated members and
unsupported composite nesting. It follows the pinned module's resolution rules:
a unique bare member name resolves globally; an ambiguous composite member name
resolves within that composite's project. The reusable resolver also requires
explicit `project#template` identity for pre-existing external references.

The report exposes `templateReferencesChecked` without printing project names,
template names or content. This check does not resolve template files, defaults,
appliance UUIDs or committed versions, and does not establish schema or execution
parity. `--require-config-parity` continues to return exit code 3 for incomplete
sections even when the reference graph is valid.

Use `--template-directory /path/to/templates` to additionally validate the
external `.j2`, `.vlt` and `.vtl` files used by the pinned NetAsCode module. This
implies the reference-graph check. Project-qualified filename stems take priority
over bare stems; CRLF is normalized to LF. Duplicate stems across extensions,
missing regular-template content, invalid text, symlink assets and more than
1 MiB of content are rejected. Composite templates require no body file.

The report adds `templateContentsChecked`; template bodies and filenames are
never printed. This is input qualification, not Jinja/Velocity rendering or a
version commit. Full parity remains false until the corresponding native
configuration and deployment capabilities are implemented and qualified.

## Catalyst Center network-profile defaults

Default expansion is an explicit offline step. Runtime providers continue to
consume selected resolved sources. The new command covers qualified network
profile expressions from the immutable 0.5.0 module; it refuses other sections
rather than labelling a partially expanded whole model resolved.

```sh
umask 077
go run ./tools/cvk-netascode-migrate controller-export-network \
  --model-version 0.5.0 \
  --defaults examples/configs/catalyst-center/network-defaults-overrides.yaml \
  --output /tmp/cvk-network-resolved.json \
  examples/configs/catalyst-center/network-defaults-source.yaml \
  > /tmp/cvk-network-provenance.json
```

Choose a fresh output path: files and symlinks are never overwritten. The payload
is written with mode 0600; stdout reports provenance without configuration values.
Review the artifact and existing configuration ownership before using it as a
`NetworkControllerConfig` source. Copy the reported exporter policy identity to
`modelSource.exporter`; preserve customer revision provenance separately. Do not
use the policy/defaults digest as `schemaDigest` or claim full schema validation.

Missing telemetry fields use pinned defaults, while explicit false/null stay
distinct. Defaults do not activate scalar resources skipped by the module's
original selection predicates. Explicit null collector choices, null override
values and unqualified scopes are rejected. Native dependency/ownership checks,
field-schema validation and qualification remain separate. See [network-profile
export qualification and limitations](evidence/catalyst-center-2026-10-17/network-profile-default-export.md).

### Catalyst Center managed-site selection

The same offline command can select validated network/telemetry site bindings
using the pinned module variables. For example:

```sh
go run ./tools/cvk-netascode-migrate controller-export-network \
  --model-version 0.5.0 --output /private/tmp/catc-lab-selected.json \
  --managed-sites '["Global/Lab"]' --manage-specific-sites-only \
  examples/configs/catalyst-center/network-site-selection-source.yaml
```

Without `--manage-specific-sites-only`, selection uses the pinned module's literal
`startswith`: `Global/Lab` also selects `Global/Lab2`. Every requested managed site
must exist as an explicit non-Global path in the input; a missing prefix or
`["Global"]` fails upstream's declared-site check. Exact mode requires a nonempty
managed-site list. Repeated requested paths retain the module's membership semantics.

`--manage-global-settings` includes a declared Global binding. With no managed
sites it selects Global only. With managed sites it combines Global with their
selection. With neither option, Global and all declared sites are selected.
Explicit `--manage-global-settings=false` retains those false/default semantics.
Other module sections and cross-resource dependencies are not filtered here.

All source profiles and references are validated before filtering; excluded
invalid declarations cannot disappear into an apparently valid export. The
export removes excluded site bindings and profiles referenced only by them,
then validates the result with the existing compiler. An empty selection emits
empty `sites` and `network_settings` maps, selecting zero runtime resources.
Retain preserves existing native values and application receipts; changing scope
does not settle an uncertain task, authorize deletion or transfer ownership.

Only digests, selected-site count and capability limits go to stdout. Selection
has a composite source-policy identity and options digest; this remains distinct
from schema validation and whole-model parity. Output uses the same new-file,
mode-0600 contract. [Selection evidence and remaining limits](evidence/catalyst-center-2026-10-17/network-managed-site-export.md)
describe the Kubernetes API and actual Catalyst Center qualification boundaries.
