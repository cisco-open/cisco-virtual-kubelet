# SD-WAN controller deployment

The independent `sdwan` adapter uses the existing CVK `NetworkController` worker
and `NetworkControllerConfig` intent APIs. It calls SD-WAN Manager directly;
Terraform is used only by the external differential-test oracle.

This implementation is a **fixture-qualified increment, not full NetAsCode
parity**. The pinned upstream module has 226 resource declarations. 54 resource declarations (48 distinct resource types) have fixture-level live differential coverage on Manager 20.18.2.2:

| Fixture | Qualified resources | Qualification |
|---|---|---|
| `examples/configs/sdwan/banner.yaml` | Classic Cisco banner feature template | Create, read, update, cleanup, upstream equivalence, Kubernetes reconciliation |
| `examples/configs/sdwan/system-profile.yaml` | System feature profile, system banner parcel, unattached configuration group | Dependency order, create, read, cleanup, upstream equivalence, Kubernetes reconciliation |
| `examples/configs/sdwan/policy-objects.yaml` | All 24 classic policy-object collections | Create, read, update, cleanup, upstream equivalence |
| `examples/configs/sdwan/network-hierarchy.yaml` | Groups and regions at levels 0–2, and an unassigned site | Dependency order, create, read, cleanup, upstream equivalence |
| `examples/configs/sdwan/feature-profiles.yaml` | Eight other profile containers and implicit application-priority policy settings | Create, read, changed-value update, cleanup, upstream equivalence |
| `examples/configs/sdwan/system-features.yaml` | CLI config; system basic, BFD, global, OMP, NTP, logging, MRF, flexible port speed, performance monitoring | Create, read, update, cleanup, upstream equivalence |

The baseline is NetAsCode module v1.5.0 at
`5fbf037ea57368c7e6e77694c81330b6f7b16666` and provider v0.11.5 at
`d3194b612c3f24421ec1727b6fa6e9c0bc98b07f`. The provider is
`CiscoDevNet/sdwan`; `netascode/terraform-sdwan-nac-sdwan` is the module.
`internal/controlleradapter/sdwan/testdata/upstream-lock.json` records every
resource declaration and distinguishes fixture-qualified coverage from pending
coverage. Qualification applies to the fixture's attributes, not every attribute
of that resource type.

## Model contract

The public model remains the upstream `sdwan` document. The format is
`netascode-sdwan`, model version `1.5.0`. The compiler currently supports:

- `edge_feature_templates.banner_templates`: name, description, device types,
  login/MOTD literals and variables, including upstream device-type defaults.
- `feature_profiles.system_profiles`: profile name/description, banner, basic,
  BFD, global, OMP, NTP, logging, MRF, flexible port speed and performance monitoring.
  `feature_profiles.cli_profiles.config` supports native CLI parcels.
  Scalar and nested-list definitions for basic/global/OMP/NTP/logging are generated
  from pinned module mappings and provider defaults/value schemas. Regenerate with
  `scripts/sdwan/generate-parcel-definitions.py MODULE_CHECKOUT PROVIDER_CHECKOUT`.
  Generated metadata retains the upstream MPL-2.0 license in `definitions/LICENSE`.
- `configuration_groups`: name/description and references to declared system,
  transport, service, CLI or other profiles. Device associations are not enabled.
- Name/description mappings for the other eight profile collections, plus the
  automatically created application-priority settings parcel and visibility flags.
- All 24 classic `policy_objects` collections, including named SLA probe references.
  Policy names are limited to 32 characters because this Manager rewrites longer names.
- `network_hierarchy` groups/regions through levels 0–2 and unassigned sites with
  the 20.18 location/GPS model. Global collectors remain unsupported.

`deployment.yaml` combines the qualified fixtures under one config and five
managed sections. Use `deployment-config.yaml` with its resolved output instead
of installing individual configs that would compete for the same section Lease.
The combined fixture includes hierarchy prerequisites for region lists. For
`policy-objects-config.yaml`, resolve `policy-deployment.yaml` so that the region
prerequisite is included. `system-features-config.yaml` uses `system-features.yaml`
as an alternative feature-profile section owner. The
compiler allows up to 512 resources, bounded to 128 per section by journal capacity.

Unknown fields and unsupported sections fail before mutation. Credentials and
intent Secret injection are separate concerns: endpoint credentials work through
projected files; intent `secretRefs` are currently rejected rather than ignored.
No controller-specific API payloads appear in the public CRD.

Resolve the qualified defaults outside the worker before declaring the source
resolved. This retains upstream field names:

```bash
go run ./cmd/cvk-sdwan-resolve examples/configs/sdwan/banner.yaml > /tmp/sdwan-banner.json
go run ./cmd/cvk-sdwan-resolve examples/configs/sdwan/system-profile.yaml > /tmp/sdwan-system-profile.json
```

## Deploy the qualified fixtures

Install a matching CVK image and Helm chart. The chart adds the dedicated
`cisco-virtual-kubelet-sdwan-worker` ClusterRole. Its namespace-scoped binding
grants status updates and section Lease get/create/update; it grants no Secret
API access, device mutation permission, or remote-deletion permission.

Create the `sdwan-lab` namespace and a same-namespace `sdwan-credentials` Secret
with `username` and `password` keys using your existing secret-management process.
Do not put credentials into the model or commit them to the repository.

```bash
kubectl create namespace sdwan-lab
kubectl apply -f examples/configs/sdwan/controller.yaml
kubectl -n sdwan-lab create configmap sdwan-banner-fixture \
  --from-file=model.yaml=/tmp/sdwan-banner.json
kubectl -n sdwan-lab create configmap sdwan-system-profile-fixture \
  --from-file=model.yaml=/tmp/sdwan-system-profile.json
kubectl apply -f examples/configs/sdwan/banner-config.yaml
kubectl apply -f examples/configs/sdwan/system-profile-config.yaml
kubectl -n sdwan-lab get networkcontrollers,networkcontrollerconfigs
```

The examples initially report drift. Select `spec.mode: apply` to deploy the
fixtures. Expect incremental `Applying` status followed by `InSync` after
read-back verification. Reconciliation preserves existing matching resources
without resubmitting their creation. An external object with the same name is a
collision, not implicit authorization to adopt it.

The lab manifest explicitly opts into insecure TLS because this endpoint's
self-signed certificate has no IP SAN. Other deployments should supply a valid
certificate and trust bundle. Sessions remain in process memory, rotate with
projected material, and have bounded lifetimes. Redirects are rejected; only GET
may refresh and retry after 401. Error bodies, cookies and tokens are not logged.

## Ownership and recovery

Ownership is per controller UID, global scope and managed section. One config
incarnation owns each section; separate configs cannot split the same section.
Each worker session holds a 90-second Lease. Before a remote mutation, the worker
persists a `Dispatched` receipt and rechecks the live endpoint/config/source and
Lease bindings. A successful response persists the remote ID before verification.

A settled Lease can transfer to a replacement worker for the same config after
expiry. An unresolved dispatch blocks takeover and automatic replay. Such a
failure requires investigation; an automatic uncertain-outcome recovery workflow
is not implemented. Status reports `OutcomeUnknown` or `SectionOwned` without
raw controller payloads. Never clear the journal merely to retry a timed-out POST.

`Retain` is the supported prune/deletion policy. Deleting intent retains remote
objects and its ownership journal; deletion does not transfer ownership to a new
config UID. Remote pruning, explicit adoption/import, ownership release and
device attachment/activation are still pending. The fixture test harness cleans
up only its own created IDs in reverse dependency order.

## Tests and upstream comparison

Normal tests do not contact the lab. Live tests require explicit environment
flags and credentials supplied outside Git. The test harness creates temporary
credential projections and removes them at completion.

```bash
go test ./internal/controlleradapter/...
go test -race ./internal/controlleradapter/... ./cmd/cisco-vk
# KUBEBUILDER_ASSETS must point to Kubernetes 1.35.0 test binaries.
go test -tags envtest ./internal/controlleradapter/sdwan -run TestEnvtest -v
```

For an upstream comparison, set `SDWAN_URL`, `SDWAN_USERNAME`, `SDWAN_PASSWORD`
and the appropriate TLS setting, then run:

```bash
python3 scripts/sdwan/oracle.py --terraform terraform --execute
python3 scripts/sdwan/oracle.py --terraform terraform --fixture-set system-features --execute
```

The script checks fixture names are absent, pins dependencies, checks the plan
contains exactly the selected fixture set (44 core objects or 12 system/CLI objects), applies upstream,
compares native observations, and destroys the upstream fixtures. It never
attaches devices. It stages hierarchy creation before region lists because the
upstream module does not express that dependency, and destroys region lists first. Run it without `--execute` for plan validation only. Do not run
the oracle while CVK is managing those same fixture names. Terraform state and
plans stay ignored; do not add them to Git.

## Remaining deployment parity

The remaining resource mappings, full upstream schema/default coverage, classic
device templates, routing and transport/service features, modern policy-object parcels and
policy definitions, global hierarchy collectors, topology/policy groups, settings, device variables,
attachment/deployment, asynchronous activation recovery, pruning and import are
not complete. Reproducible device-level qualification also needs explicit WAN-edge
targets and a restoration policy. Inventory visibility does not authorize choosing
an arbitrary existing router as the test target.

See [the qualification record](../evidence/sdwan-2026-10-14/README.md) for measured
results and limits.
