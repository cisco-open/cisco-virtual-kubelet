# NX-OS Family Reference (Beta)

!!! warning "Beta"
    NX-OS `NXOSConfig` support is a Beta runtime slice. The schema is
    `v1alpha1`, family coverage is still expanding, and wire-format behaviour
    may change between releases. See
    [Production Readiness](../production-readiness.md) for the current scope and
    hardening roadmap.

Declarative NX-OS device configuration is driven by the `NXOSConfig` CRD over
NX-API REST/DME, using the shared Network as Code config engine. Unlike the
IOS-XE [Family Reference](families/README.md) — which is generated from vendored
YANG across many families — the NX-OS slice currently ships a focused set of
hand-written family writers.

## Supported families

| Family | Key | Covers |
|---|---|---|
| System | `system` | Device-level settings such as `hostname` |
| Feature | `feature` | Individual NX-OS feature enablement (e.g. `interface-vlan`, `lacp`) |
| Feature set | `feature_set` | Installable feature sets (e.g. `fex`) |
| VLAN | `vlan` | VLAN definitions (id, name) |
| Interface (Ethernet) | `interface_ethernet` | Ethernet interface config: description, MTU, admin state |

The strict NetAsCode path emits the pinned provider's derived Layer-2/admin
attributes, but refuses to convert an observed non-Layer-2 interface and
requires an explicit `shutdown: false` before bringing up an observed shutdown
port. Native CVK source preserves omitted admin/layer properties.

Each family is opt-in per `NXOSConfig` via `spec.managedFamilies`; the engine
only reconciles families you list and applies the shared drift-detection
contract. NX-OS revision history and rollback are not implemented yet.

For sources that declare `modelSource.format: netascode-nxos`, `feature` and
`feature_set` leaves must use the canonical Boolean types. Provider-state
strings such as `enabled`, `disabled`, `installed`, or `uninstalled` remain a
native-CVK compatibility extension only and are rejected at the strict import
boundary.

## Device release gate

NX-OS writes fail closed unless the live `show version` result matches an
admitted profile. Admission here means the release can exercise the parity
pipeline; it is not production certification of every DME mapping.

| Live release pattern | Maturity | Model contract | Evidence |
|---|---|---|---|
| `10.3(9)` plus an optional alphanumeric, `.`, `_`, or `-` suffix | Admitted Beta | `0.3.0` | CVK Nexus 9000v lab evidence; current automated credentialed run pending |
| `10.5(4)` plus an optional alphanumeric, `.`, `_`, or `-` suffix | Experimental, explicit opt-in | `0.3.0` | Upstream NetAsCode tested-version matrix and pinned static DME oracle; CVK live qualification pending |

Experimental profiles are rejected by default. A lab operator can opt in to
`10.5(4)` with Helm value `nxos.allowExperimentalReleases=true`, which sets
`CVK_NXOS_ALLOW_EXPERIMENTAL_RELEASES=true` on the controller and propagates
it to per-device pods. This gate is not a production certification; leave it
off until the live write/removal/rollback checklist passes for that release.

An empty version records `Ready=False, reason=DeviceVersionPending` and is
retried automatically. A syntactically invalid result records
`MalformedDeviceVersion`; a well-formed release outside the table records
`UnsupportedDeviceVersion`. Both rejected states remain `Pending` and cannot
reach a writer mutation. If `spec.targetYangVersion` is set, it must equal the
normalized live release profile; it cannot be used to select a different DME
shape than the running device.

## Example

```yaml
apiVersion: config.cisco.vk/v1alpha1
kind: NXOSConfig
metadata:
  name: nexus9300v-vlans
  namespace: default
spec:
  deviceRef:
    name: nexus9300v-01
  managedFamilies:
    - system
    - vlan
    - interface_ethernet
  driftPolicy: revert
  driftDetectInterval: 5m
  writeStartup: true
  modelSource:
    format: netascode-nxos
    modelVersion: 0.3.0
    schemaDigest: sha256:5d5482679fb28e751d34cdc49342f8434914a7714966ba8244923b95d678698d
    resolved: true
    exporter: example-customer-exporter@0123456789abcdef0123456789abcdef01234567
    sourceRevision: 0123456789abcdef0123456789abcdef01234567
  source:
    inline:
      system:
        hostname: nexus9300v-01
      vlan:
        vlans:
          - id: 3903
            name: CVK_LAB
      interfaces:
        ethernets:
          - id: 1/1
            description: CVK lab uplink
            shutdown: false
            mtu: 9216
```

## See also

- [Network as Code](../netascode-config.md) — intent shape, drift detection, and platform capability differences
- [CRD Reference](../crds.md#nxosconfig) — full `NXOSConfig` schema
- [NX-OS Configuration Recovery](../nxos-recovery.md) — non-transactional failure handling and compensation by family
- [Production Readiness](../production-readiness.md) — NX-OS runtime-parity scope
