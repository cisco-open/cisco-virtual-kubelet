# CRD Reference

Cisco Virtual Kubelet is operated primarily through Kubernetes custom
resources. This page maps the CRDs shipped by the chart to their purpose,
controller boundary, and common operator workflow.

## CRDs At A Glance

```bash
$ kubectl api-resources | grep -E 'cisco|iosxe|deviceoperations'
NAME                         SHORTNAMES      APIVERSION                  NAMESPACED   KIND
ciscodevices                 cvk             cisco.vk/v1alpha1          true         CiscoDevice
iosxeconfigs                 iosxecfg        config.cisco.vk/v1alpha1   true         IOSXEConfig
iosxeconfigdefaults          iosxedefaults   config.cisco.vk/v1alpha1   false        IOSXEConfigDefaults
iosxedevicegroupconfigs      iosxegroup      config.cisco.vk/v1alpha1   true         IOSXEDeviceGroupConfig
iosxeinterfacegroupconfigs   iosxeifgroup    config.cisco.vk/v1alpha1   true         IOSXEInterfaceGroupConfig
iosxetemplates               iosxetpl        config.cisco.vk/v1alpha1   true         IOSXETemplate
iosxeconfigbundles           iosxebundle     config.cisco.vk/v1alpha1   true         IOSXEConfigBundle
iosxeconfigrevisions         iosxerev        config.cisco.vk/v1alpha1   true         IOSXEConfigRevision
iosxeconfigapplylogs         iosxelog        config.cisco.vk/v1alpha1   true         IOSXEConfigApplyLog
iosxediagnostics             iosxediag       config.cisco.vk/v1alpha1   true         IOSXEDiagnostic
iosxetelemetries             iosxetel        config.cisco.vk/v1alpha1   true         IOSXETelemetry
deviceoperations             devop           ops.cisco.vk/v1alpha1      true         DeviceOperation
iosxeoperationalactions      xeop            ops.cisco.vk/v1alpha1      true         IOSXEOperationalAction
iosxesoftwareupgrades        xeupgrade       ops.cisco.vk/v1alpha1      true         IOSXESoftwareUpgrade
```

| Kind | Scope | Primary use |
|---|---:|---|
| `CiscoDevice` | Namespaced | Declares one managed device and drives the per-device VK deployment. |
| `IOSXEConfigDefaults` | Cluster | Fleet-wide baseline configuration merged into device intent. |
| `IOSXEDeviceGroupConfig` | Namespaced | Shared configuration for selected devices. |
| `IOSXEInterfaceGroupConfig` | Namespaced | Shared configuration for selected interfaces on selected devices. |
| `IOSXETemplate` | Namespaced | Reusable parameterized configuration fragments. |
| `IOSXEConfig` | Namespaced | Per-device Network as Code intent, drift detection, apply, and rollback. |
| `IOSXEConfigBundle` | Namespaced | Fans one config template out across selected devices. |
| `IOSXEConfigRevision` | Namespaced | Immutable resolved-intent history used for rollback and audit. |
| `IOSXEConfigApplyLog` | Namespaced | Per-apply audit entries with family and diff metadata. |
| `IOSXEDiagnostic` | Namespaced | Read-only command capture, one-shot or scheduled. |
| `IOSXETelemetry` | Namespaced | gNMI subscriptions mapped to OpenTelemetry signals. |
| `DeviceOperation` | Namespaced | Auditable read-only operational requests, including gNOI probes. |
| `IOSXEOperationalAction` | Namespaced | One-shot write-class gNOI actions with confirmation and events. |
| `IOSXESoftwareUpgrade` | Namespaced | Multi-phase gNOI software install, activate, verify, and rollback. |

## CiscoDevice

`CiscoDevice` is the inventory and lifecycle root. The manager watches it,
creates or updates a per-device VK deployment, and that VK registers a virtual
node that can host Kubernetes pods through IOS-XE App Hosting.

Use it when you want Kubernetes to see a Cisco device as a schedulable node.
Important fields include `spec.driver`, `spec.address`,
`spec.credentialSecretRef`, `spec.transport`, `spec.configPrereqs`, and
`spec.opsPolicy`.

```yaml
apiVersion: cisco.vk/v1alpha1
kind: CiscoDevice
metadata:
  name: cat9300-4
  labels:
    site: lab
    role: access
spec:
  driver: XE
  address: 198.51.100.104
  port: 443
  username: cisco
  credentialSecretRef:
    name: cat9300-4-credentials
  transport: restconf
  maxPods: 16
  xe:
    networking:
      interface:
        type: AppGigabitEthernet
        appGigabitEthernet:
          mode: trunk
          vlanIf:
            dhcp: true
            vlan: 300
            guestInterface: 0
```

```bash
$ kubectl get cvk
NAME        DRIVER   ADDRESS          PHASE   AGE
cat9300-4   XE       198.51.100.104   Ready   42m

$ kubectl get nodes -l cisco.io/device=cat9300-4
NAME        STATUS   ROLES   AGE   VERSION
cat9300-4   Ready    agent   41m   v1.30.0-vk
```

## Configuration CRDs

The configuration CRDs work together. Defaults, groups, interface groups,
templates, and per-device source are resolved into canonical intent. The config
driver plans the change, applies managed families, verifies drift, and records
history.

Typical flow:

```text
IOSXEConfigDefaults
  + IOSXEDeviceGroupConfig
  + IOSXEInterfaceGroupConfig
  + IOSXETemplate
  + IOSXEConfig / IOSXEConfigBundle
  -> resolved intent
  -> managed family writers
  -> RESTCONF, NETCONF, or gNMI transport
  -> IOS-XE running config
  -> status, revision, and apply log
```

### IOSXEConfigDefaults

`IOSXEConfigDefaults` is cluster-scoped baseline intent. Use it for fleet-wide
configuration that should apply before namespace or device-specific overrides.

```yaml
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEConfigDefaults
metadata:
  name: enterprise-baseline
spec:
  configuration:
    ntp:
      servers:
        - address: 192.0.2.10
    aaa:
      new_model: true
```

### IOSXEDeviceGroupConfig

`IOSXEDeviceGroupConfig` selects devices by explicit references or labels and
contributes shared intent. Use it for site, role, or platform configuration.

```yaml
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEDeviceGroupConfig
metadata:
  name: lab-access
spec:
  deviceSelector:
    matchLabels:
      site: lab
      role: access
  configuration:
    logging:
      hosts:
        - 192.0.2.30
```

### IOSXEInterfaceGroupConfig

`IOSXEInterfaceGroupConfig` applies shared intent to matching interfaces on
matching devices. Use it for repeated switchport, VLAN, trunk, or management
patterns.

```yaml
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEInterfaceGroupConfig
metadata:
  name: app-hosting-uplinks
spec:
  deviceSelector:
    matchLabels:
      role: access
  interfaceSelector:
    - type: GigabitEthernet
      namePattern: "1/0/[1-4]"
  configuration:
    interface_ethernet:
      interfaces:
        - description: "Kubernetes app-hosting access"
          enabled: true
```

### IOSXETemplate

`IOSXETemplate` stores reusable fragments. Templates keep common intent in one
place and let `IOSXEConfig` inject values from the target device or namespace.

```yaml
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXETemplate
metadata:
  name: snmp-site-template
spec:
  parameters:
    - name: site
      type: string
      required: true
  configuration:
    snmp_server:
      location: "{{ .site }}"
```

### IOSXEConfig

`IOSXEConfig` is the main per-device declarative configuration CRD. Use it for
managed YANG families, drift detection, dry-run style planning, transaction
mode, source version selection, and rollback.

```yaml
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEConfig
metadata:
  name: cat9300-4-network
spec:
  deviceRef:
    name: cat9300-4
  managedFamilies:
    - dhcp
    - vlan
  driftPolicy: report
  writeStartup: true
  source:
    inline:
      vlan:
        vlans:
          - id: 300
            name: APP_HOSTING
      dhcp:
        pools:
          - name: app-hosting
            network: 10.30.0.0
            mask: 255.255.255.0
            default_router: 10.30.0.1
```

```bash
$ kubectl get iosxecfg
NAME                 DEVICE      PHASE    DRIFT   AGE
cat9300-4-network    cat9300-4   InSync   none    17m
```

### IOSXEConfigBundle, Revision, and ApplyLog

`IOSXEConfigBundle` fans one `IOSXEConfig` template out across selected
devices. `IOSXEConfigRevision` records resolved-intent history for rollback and
audit. `IOSXEConfigApplyLog` records apply attempts, family outcomes, and diff
metadata without making the main status object unbounded.

Use the generated family reference when building configuration payloads:
[Family Reference](reference/families/README.md).

## Diagnostics and Telemetry

`IOSXEDiagnostic` runs read-only IOS-XE commands. It can run once, on a
schedule, or inside a maintenance window. Output can stay inline or spill to
ConfigMaps for larger captures.

`IOSXETelemetry` declares gNMI Subscribe streams and maps notifications into
OpenTelemetry metrics and logs. Use it for streaming MDT from a device through
the per-device VK. See [Telemetry](telemetry.md) and
[Telemetry Cardinality](telemetry-cardinality.md).

## Operation CRDs

Operation CRDs are separated by trust level. `DeviceOperation` and
`IOSXEDiagnostic` are read-only. `IOSXEOperationalAction` and
`IOSXESoftwareUpgrade` require explicit runtime enablement and should receive
separate RBAC grants.

| Trust level | CRDs | Notes |
|---|---|---|
| Read-only | `DeviceOperation`, `IOSXEDiagnostic` | Safe for diagnostics and lower-trust automation when namespace RBAC is scoped appropriately. |
| Upgrade | `IOSXESoftwareUpgrade` | Requires `gnoi.enableSoftwareUpgrade` or `CISCO_VK_ENABLE_IOSXE_SOFTWARE_UPGRADE`. |
| Break-glass/write-class | `IOSXEOperationalAction` | Requires `gnoi.enableWriteClass` or `CISCO_VK_ENABLE_WRITE_CLASS_GNOI`. |

### DeviceOperation

`DeviceOperation` is the auditable asynchronous path for read-only operations.
Supported kinds are `ShowCommand`, `ConfigDiff`, `PacketCapture`, `GNOIPing`,
`GNOITraceroute`, `GNOITime`, `GNOIFileGet`, `GNOIFileStat`, `GNOICertGet`,
`GNOICanGenerateCSR`, `GNOIRebootStatus`, and `GNOIOSVerify`.

```yaml
apiVersion: ops.cisco.vk/v1alpha1
kind: DeviceOperation
metadata:
  name: cat9300-4-gnoi-time
spec:
  deviceRef:
    name: cat9300-4
  operation:
    kind: GNOITime
  ttlSecondsAfterFinished: 600
```

### IOSXEOperationalAction

`IOSXEOperationalAction` is for write-class one-shot gNOI actions. It supports
`Reboot`, `CancelReboot`, `KillProcess`, `FilePut`, `FileRemove`, and
`FactoryReset`.

Every action requires `spec.confirm` to equal the target device name, the spec
is immutable after creation, and a `Running` action is not dispatched a second
time after controller restart.

```yaml
apiVersion: ops.cisco.vk/v1alpha1
kind: IOSXEOperationalAction
metadata:
  name: cat9300-4-reload
spec:
  deviceRef:
    name: cat9300-4
  confirm: cat9300-4
  action:
    kind: Reboot
    reboot:
      method: COLD
      delaySeconds: 0
      message: "maintenance reload"
```

### IOSXESoftwareUpgrade

`IOSXESoftwareUpgrade` drives the gNOI software-upgrade lifecycle. Use exactly
one image source: URL with `sha256`, `configMapRef`, or `localPath`. For a
staged image on flash, include `localPathSHA256` when the device supports gNOI
File.Get hash verification.

```yaml
apiVersion: ops.cisco.vk/v1alpha1
kind: IOSXESoftwareUpgrade
metadata:
  name: cat9300-4-to-17-18
spec:
  deviceRef:
    name: cat9300-4
  targetVersion: 17.18.02
  strategy: Reload
  rollbackOnFailure: true
  imageSource:
    localPath: flash:cat9k_iosxe.17.18.02.SPA.bin
    localPathSHA256: 8f1b9e2d1d9b6d0e000000000000000000000000000000000000000000000000
  rebootTimeoutSeconds: 1800
```

For the full operational model, see
[gNOI and Software Lifecycle](gnoi-software-lifecycle.md) and the
[Operations Runbook](operations.md).

## Safety and RBAC Notes

Keep the read and write surfaces distinct:

- Grant `DeviceOperation` and `IOSXEDiagnostic` to read-only automation.
- Grant `IOSXESoftwareUpgrade` only to upgrade operators.
- Grant `IOSXEOperationalAction` only to break-glass or tightly controlled
  maintenance namespaces.
- Keep `gnoi.enableWriteClass` and `gnoi.enableSoftwareUpgrade` disabled by
  default in Helm values until the device, RBAC, and maintenance workflow are
  ready.

For live debugging, start with:

```bash
kubectl get cvk,iosxecfg,devop,xeop,xeupgrade -A
kubectl describe cvk <device-name>
kubectl logs deploy/<device-name>-vk --tail=200
kubectl get events --sort-by=.lastTimestamp
```
