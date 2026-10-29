# Maintain the CVK installation

Use this guide when upgrading or rolling back the CVK Helm release. It is for
cluster operators who manage CRDs, chart values and controller deployments.
The expected result is a compatible set of CRDs, chart and controller images,
with existing devices still reconciling. To change the operating system on a
device, use the [IOS-XE upgrade and downgrade runbook](gnoi-iosxe-upgrade-runbook.md).

## Before you start

- Confirm the Kubernetes context, Helm release and release namespace. Examples
  use release `cvk`, namespace `cvk-system`, and target chart `2026.11.1`.
  Check the [Releases page](https://github.com/cisco-open/cisco-virtual-kubelet/releases)
  for publication and artifacts before running them. If November is not listed,
  `2026.10.0` remains the previous published baseline. Substitute your
  installation's names.
- Read the target release's migration notes and verify the chart's provenance
  through your normal release-verification process. Keep its chart and image
  versions aligned. Features introduced in `v2026.11.1` require the November
  CRDs, admission policies, chart, manager and workers together.
- Back up both CRD definitions and the custom resources in the `cisco.vk`,
  `config.cisco.vk` and `ops.cisco.vk` API groups, across all relevant namespaces.
  Retain your existing Helm values, previous chart and recovery procedure.
  Protect backups: device configuration and operation output can be sensitive.
- Have permission to update cluster-scoped CRDs and the namespaced Helm release.
  Review changes to stored resources and runtime gates before the maintenance
  window. Do not overlap controller migration with disruptive device work.

## Upgrade the CRDs and chart

Helm installs CRDs on initial installation but does not upgrade them. Apply the
selected chart's CRDs **before** upgrading the controller. Run the following
steps from a fresh working directory so an older unpacked chart cannot be
mistaken for the selected release.

### 1. Pull the chart and save installation state

```bash
helm pull oci://ghcr.io/cisco-open/charts/cisco-virtual-kubelet \
  --version 2026.11.1 --untar

helm get values cvk --namespace cvk-system -o yaml \
  > cvk-values-before-upgrade.yaml
kubectl get customresourcedefinitions.apiextensions.k8s.io -o yaml \
  > cvk-crds-before-upgrade.yaml
```

These commands save values and schemas, **not the custom-resource instances**.
Complete and verify the resource backup described above before continuing.
Prepare `cvk-values-upgrade.yaml` from your existing overrides, reviewing them
against the target chart's defaults and migration notes. Preserve intentional
image, credentials, topology, RBAC and runtime-gate settings; do not assume a
chart downgrade or upgrade will migrate them automatically.

### 2. Review and apply this release's CRDs

```bash
kubectl diff --server-side --force-conflicts \
  --field-manager=cvk-crd-upgrade \
  -f cisco-virtual-kubelet/crds/
```

Exit status **1** means differences were found; it is expected when the schema
changes. An error exit must be investigated before applying. Review the exact
diff, including validation and defaulting changes. `--force-conflicts` transfers
field ownership where the original Helm manager conflicts; it applies only to
the objects in the specified directory. Never point it at unrelated manifests.

After reviewing the diff, apply the same directory and wait for registration:

```bash
kubectl apply --server-side --force-conflicts \
  --field-manager=cvk-crd-upgrade \
  -f cisco-virtual-kubelet/crds/
kubectl wait --for=condition=Established --timeout=60s \
  -f cisco-virtual-kubelet/crds/
kubectl get -f cisco-virtual-kubelet/crds/
```

November refreshes 18 existing CRD manifests; it adds no new CRD kinds.
Four have substantive schema changes: `CiscoDevice`, `NetworkController`,
`IOSXESoftwareRollout` and `IOSXESoftwareUpgrade`. The other 14 have generator
metadata updates. `IOSXESoftwareRollout` already exists in October.
Apply the complete CRD directory from the pinned November chart before its
controller upgrade; retaining the October definitions is insufficient.

### 3. Upgrade and verify the controller

```bash
helm upgrade cvk oci://ghcr.io/cisco-open/charts/cisco-virtual-kubelet \
  --version 2026.11.1 \
  --namespace cvk-system \
  -f cvk-values-upgrade.yaml
kubectl rollout status deployment/cvk-cisco-virtual-kubelet-controller \
  --namespace cvk-system
kubectl get ciscodevices.cisco.vk --all-namespaces
```

The Deployment name above assumes the default chart naming and release `cvk`;
use your actual name if `nameOverride` or `fullnameOverride` is set. A completed
manager rollout is the first check: also inspect device conditions, worker
rollouts and existing workload health. See [Observability](observability.md)
for controller and worker evidence. Keep backups until the rollback window has
passed.

## What to expect after a CRD upgrade

- Existing custom resources remain stored. New optional fields can be absent
  or receive schema defaults; do not assume every new field has a useful value.
- A new required field can make the next write to an older object fail
  validation. Review and migrate affected resources before relying on the new
  controller.
- The APIs remain `v1alpha1`. Renames, removals and other structural changes may
  require manual migration; the version name is not a compatibility guarantee.

If a manager starts without `NetworkController` and `NetworkControllerConfig`
CRDs during migration from an older release, it preserves the existing device
reconcilers but skips network-controller registration for that process. Apply
the missing CRDs and restart the manager Deployment to enable those controllers;
installing CRDs alone does not update an already-running manager. Other required
missing CRDs can block startup.

A CRD does not supply an adapter missing from the running image. The `sdwan` and
`nexus-dashboard` adapters and `NetworkController.spec.deviceAdoption` are
introduced in `v2026.11.1`; use the matching November chart and image. Keep the
adapters' Alpha qualification and supported-operation limits in view.

## Roll back a release

A Helm rollback does not roll back CRDs or migrate stored custom resources.
First inspect the target release's schema and the objects written since the
upgrade. Resolve incompatible fields or versions before starting an older
controller. Reapplying an older schema can prune fields on subsequent writes;
it is not a data-restoration procedure. Never delete a CRD to roll it back,
because deleting the definition also deletes its custom resources.

Pull the exact previous chart into a separate directory and prepare values
compatible with it. The following assumes that the reviewed previous chart is
already unpacked at `previous-release/cisco-virtual-kubelet/` and that
`cvk-values-rollback.yaml` contains its reviewed overrides:

```bash
kubectl diff --server-side --force-conflicts \
  --field-manager=cvk-crd-upgrade \
  -f previous-release/cisco-virtual-kubelet/crds/
```

Only continue when the data/schema compatibility review is complete. Apply the
reviewed schemas, then restore the matching chart and verify recovery:

```bash
kubectl apply --server-side --force-conflicts \
  --field-manager=cvk-crd-upgrade \
  -f previous-release/cisco-virtual-kubelet/crds/
kubectl wait --for=condition=Established --timeout=60s \
  -f previous-release/cisco-virtual-kubelet/crds/
helm upgrade cvk ./previous-release/cisco-virtual-kubelet \
  --namespace cvk-system \
  -f cvk-values-rollback.yaml
kubectl rollout status deployment/cvk-cisco-virtual-kubelet-controller \
  --namespace cvk-system
```

Confirm device and workload health again. Restoring controller software does
not undo a device operation that was already accepted; inspect any in-flight
or quarantined [device mutations](device-actions.md#shared-device-mutation-lease)
separately.
