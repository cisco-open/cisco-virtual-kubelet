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

Each family is opt-in per `NXOSConfig` via `spec.managedFamilies`; the engine
only reconciles families you list, and applies drift detection and revision
tracking the same way it does for IOS-XE.

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
    resolved: true
  source:
    inline:
      nxos:
        devices:
          - name: nexus9300v-01
            configuration:
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

- [Network as Code](../netascode-config.md) — intent shape, drift detection, and revisions shared across IOS-XE and NX-OS
- [CRD Reference](../crds.md#nxosconfig) — full `NXOSConfig` schema
- [Production Readiness](../production-readiness.md) — NX-OS runtime-parity scope
