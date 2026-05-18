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
