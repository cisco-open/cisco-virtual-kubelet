# Production Readiness Plan

This page tracks the work required to move the current NX-OS runtime slice from
beta to production-grade, while keeping Cisco Virtual Kubelet aligned with the
Network as Code model stripes and the existing per-device Virtual Kubelet
architecture.

## Current merge scope

The current NX-OS branch is a runtime parity foundation, not a full parity
claim. It adds:

- NX-OS app-hosting through NX-API CLI.
- `NXOSConfig` over the common config runtime.
- NX-API REST/DME config transport.
- Initial Fetch -> Diff -> Apply -> Verify writers for `system`, `vlan`, and
  `interface_ethernet`.
- A common REST helper under `internal/configengine/transport`.
- NetAsCode device-centric envelope resolution for global, device group,
  device, variable, model-template, and interface-group scopes.

The branch should be described as beta until the gates below pass repeatedly on
branch-built images.

## Pre-merge gates

These items gate merging a branch that changes runtime behavior:

| Gate | Required evidence |
|---|---|
| Unit and race tests | `go test ./...` and `go test -race ./...` pass. |
| Generated artifacts | `make deepcopy-gen manifests` produces no unexpected drift. |
| Build and smoke | GitHub `build-and-smoke` passes, including envtest and Helm checks. |
| Existing IOS-XE labs | Cat8kv and Cat9k lab checks pass so NX-OS work does not regress IOS-XE. |
| NX-OS read-only live check | DME login, DME system fetch, and NX-API CLI endpoint succeed against the virtual NX-OS node. |
| Branch-image NX-OS deployment | The exact branch image is deployed to the lab cluster. |
| NX-OS app-hosting lifecycle | A test pod reaches running state through install, activate, start, then is stopped, deactivated, uninstalled, and removed. |
| NXOSConfig live write | A disposable VLAN or equivalent low-risk object is applied through DME, verified by Fetch/Diff, and cleaned up. |
| DeviceOperation CLI | A read-only NX-OS command succeeds through `DeviceOperation` and records status/output. |
| Review state | Any stale `CHANGES_REQUESTED` review is cleared or explicitly superseded by reviewer approval. |

The NX-OS lab gates can be run through the shared wrapper:

```bash
NXOS_HOST=<device> \
NXOS_USERNAME=<user> \
NXOS_PASSWORD=<password> \
scripts/nxos-lab-smoke.sh
```

Optional mutating and Kubernetes checks are opt-in:

```bash
RUN_LIVE_NXOS_CONFIG_WRITE=1 \
RUN_NXOS_DEVICEOP_SMOKE=1 \
RUN_NXOS_APPHOSTING_SMOKE=1 \
NXOS_K8S_NAMESPACE=<namespace> \
NXOS_K8S_DEVICE=<ciscodevice-and-node-name> \
NXOS_APP_IMAGE=<nxos-app-hosting-package> \
NXOS_HOST=<device> \
NXOS_USERNAME=<user> \
NXOS_PASSWORD=<password> \
scripts/nxos-lab-smoke.sh
```

## Production hardening phases

### Phase 1: NX-OS REST/DME safety

Goal: make the non-transactional behavior explicit and operationally safe.

- Keep NX-OS transport kind as neutral `rest`.
- Never advertise transactions or confirmed-commit for NX-API REST/DME.
- Report non-transactional fallback clearly in status/events when
  `spec.transactional` or `spec.confirmTimeoutSeconds` is requested.
- Apply managed families in deterministic order.
- Verify after each family and stop on the first failed verify.
- Run `writeStartup` only after every managed family verifies.
- Add compensating delete/prune support only where Fetch can prove ownership.
- Classify transport failures as retryable, auth, validation, or permanent
  DME errors so controller-runtime retry behavior is predictable.

### Phase 2: RBAC hardening

Goal: shrink production blast radius without breaking the per-device worker
model.

- Split Helm RBAC into profiles: controller, per-device runtime,
  config-writer, diagnostics, and lifecycle operations.
- Prefer namespaced Roles for pod, config, operation, and artifact surfaces.
- Keep ClusterRoles only where Kubernetes requires cluster scope, such as
  Nodes and cluster-scoped CRDs.
- Feature-gate high-risk verbs for write-class operations and software
  lifecycle.
- Document the default profile and the strict profile separately. The current
  chart exposes `rbac.profile=default|strict`; strict mode withholds reserved
  volume reads plus write-class gNOI/software-upgrade CRD permissions unless
  their runtime gates are enabled.
- Add RBAC audit tests that fail when strict-profile gates are removed or
  wildcard permissions appear.

### Phase 3: NX-OS NetAsCode family expansion

Goal: expand coverage through small vertical slices, each with real
Fetch -> Diff -> Apply -> Verify behavior.

The first supported families are:

| Family | Current supported fields | Notes |
|---|---|---|
| `system` | `hostname` | `system.mtu` is not supported in the first slice. |
| `vlan` | `vlans[].id`, `vlans[].name` | VNI/VXLAN leaves are deferred. |
| `interface_ethernet` | `id`, `description`, `shutdown`, `mtu` | Switchport, L3, protocol, and port-channel leaves are planned. |

Recommended next waves:

1. L2 access/trunk: `interface_switchport`.
2. L3 primitives: `vrf`, `interface_vlan`, `interface_loopback`,
   `static_route`.
3. Aggregation: `interface_port_channel` and LACP dependencies.
4. Management: `ntp`, `logging`, `snmp_server`.
5. Routing and policy: `ospf`, `bgp`, prefix lists, route maps, ACLs.
6. Fabric/VXLAN: VLAN VNI, NVE, EVPN, and BGP EVPN as a coordinated wave, not
   isolated writers.

Each family requires:

- NetAsCode field coverage entry.
- DME DN and payload mapping.
- Fetch parser for observed state.
- Writer diff tests for apply, no-op, invalid input, and unsupported fields.
- Fake DME transport fixtures.
- Optional live read/write test guarded by environment variables.
- Documentation of supported and rejected fields.

### Phase 4: Neutral transport maturity

Goal: keep protocol mechanics shared while platform behavior stays in platform
adapters.

The shared layer owns:

- REST request construction.
- TLS defaults.
- Redaction.
- Retry and backoff.
- Rate limiting hooks.
- Pagination and task-polling interfaces.
- OpenTelemetry span/metric hooks.

Platform adapters own:

- NX-OS DME login/session behavior.
- FMC domain selection and deployment tasks.
- ISE ERS/OpenAPI authentication and pagination choices.
- Catalyst Center task polling and intent APIs.
- Meraki organization/network scoping.
- IOS-XR NETCONF/gNMI candidate and confirmed-commit behavior.
- SONIC/OpenConfig translation if a real NetAsCode SONIC stripe is adopted.

### Phase 5: Per-device scale and operations

Goal: keep horizontal scaling aligned with Virtual Kubelet instead of forcing a
single central aggregator.

- Preserve per-device VK workers as the primary runtime unit.
- Use topology labels and worker capability status for placement, upgrades, and
  debugging.
- Add per-device concurrency limits and session locks for device-facing calls.
- Surface metrics for DME request latency, retry count, apply count, verify
  failures, session login failures, and app-hosting lifecycle duration.
- Emit OpenTelemetry spans around config family apply/verify and pod lifecycle
  operations.
- Keep any future orchestrator optional: it may plan topology-aware waves, but
  execution should remain in per-device workers unless a platform requires
  controller-centric execution.

## Future platform sequence

The same runtime structure should be reused, but transport choices differ:

| Platform | Runtime shape | First transport | Notes |
|---|---|---|---|
| IOS-XE | device-centric | RESTCONF/NETCONF/gNMI | Existing broadest implementation. |
| NX-OS | device-centric | REST/DME plus CLI diagnostics/app-hosting | Current beta slice. |
| IOS-XR | device-centric | NETCONF/gNMI | Best candidate for YANG/ygot parity and confirmed commit. |
| SONIC | device-centric | OpenConfig/gNMI | Treat as OpenConfig unless a real NetAsCode SONIC stripe exists. |
| FMC/FTD | controller-centric | REST | FTD policy/config should be FMC-managed intent. |
| ISE | controller-centric | REST | Model ERS/OpenAPI pagination, tasks, and system sections in an adapter. |
| APIC/NDO/Catalyst Center/Meraki | controller-centric or solution-centric | REST | Share neutral REST mechanics, keep platform workflows in adapters. |

## Production declaration checklist

NX-OS can be called production-ready only when all of the following are true:

- Branch-image lab deployment is repeatable.
- App-hosting lifecycle is tested against a real NX-OS target.
- At least one DME write family is live-tested with cleanup in CI.
- RBAC strict profile exists and is documented.
- Non-transactional DME behavior is visible in status/events.
- Supported/unsupported NetAsCode fields are documented per family.
- Transport and reconcile metrics are available for DME, app-hosting, and
  DeviceOperation.
- Rollback/compensation behavior is documented for every supported write
  family.
