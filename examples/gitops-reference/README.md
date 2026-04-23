# GitOps reference for IOSXEConfig

This directory is a byte-for-byte-runnable example of managing an IOS-XE
device through the `config.cisco.vk/v1alpha1` CRDs, driven by Flux. Its
primary purpose is to show the porting shape from an existing
`netascode/terraform-iosxe-nac-iosxe` repository:

- the `.nac.yaml` file under `devices/edge-01/` is exactly the shape
  you would hand to the Terraform module today — it is read verbatim
  as a ConfigMap via `kustomize`'s `configMapGenerator`;
- `IOSXEConfig` points at that ConfigMap via `spec.source.configMapRef`;
- Flux reconciles both the CR and the ConfigMap together, so a change
  to the YAML becomes a change on the device without a Terraform plan.

**Phase-0 note.** The shipped config driver is a stub: the reconciler
records `status.phase: Pending` on each matched CR but does not write
to the device. This fragment exists so the CR shape is exercised
end-to-end — CI validates it, the schema admits it, the reconciler
picks it up — before Phase-1 adds the live write path.

## Layout

```
examples/gitops-reference/
├── README.md
├── kustomization.yaml              # top-level aggregator
├── defaults/
│   └── iosxeconfigdefaults.yaml    # cluster-scoped baseline
├── device-groups/
│   └── access-switches.yaml        # selector-matched shared config
├── templates/
│   └── standard-uplink.yaml        # parameterised reusable fragment
├── devices/edge-01/
│   ├── kustomization.yaml          # promotes data.nac.yaml to ConfigMap
│   ├── ciscodevice.yaml            # existing CVK node surface
│   ├── iosxeconfig.yaml            # desired-state CR
│   ├── data.nac.yaml               # netascode YAML — unchanged from source
│   └── secret.sops.yaml.example    # SOPS-encrypted credential placeholder
├── apps/nginx/
│   └── pod.yaml                    # sample IOx-hostable pod
└── flux/
    ├── gitrepository.yaml
    └── kustomization.yaml
```

## Quick start (Flux)

Applying this directory assumes a cluster with Flux already installed and
the CVK Helm chart deployed. With the CR scheme registered, Flux needs
only to reconcile the repo:

```sh
kubectl apply -k examples/gitops-reference/flux/
```

The `Kustomization` object in `flux/` points Flux at the rest of this
directory. `healthChecks` on the `IOSXEConfig` will keep Flux's `Ready`
condition `False` until the config driver reports `status.phase: InSync`
— a useful gate for CI pipelines that depend on device state.

## Quick start (no Flux)

Nothing in this fragment requires Flux at apply time; the objects are
plain CRs. For ad-hoc testing:

```sh
kubectl apply -k examples/gitops-reference/
```

## Credentials

`devices/edge-01/secret.sops.yaml.example` is a placeholder. Before
using this fragment against a real device:

1. Create the Secret in the target cluster out-of-band
   (or via `sops` + the Flux `sops` decryption provider), and
2. Remove the `.example` suffix only in your own fork — the in-tree
   file must stay as `.example` so no one mistakes it for a usable
   credential.

## What still needs engineer judgement at port time

- The `managedFamilies` list in `iosxeconfig.yaml` must match the
  families actually present in your `data.nac.yaml`. Extra entries
  are harmless; missing entries mean the driver will leave those
  families unmanaged on the device.
- `driftPolicy: report` is the safe default during a cutover from an
  existing pipeline. Flip to `revert` only after parallel-run
  confirms no spurious diffs.
- `transactional: true` requires NETCONF (Phase-2). Leave it `false`
  while running against the Phase-1 RESTCONF path.
