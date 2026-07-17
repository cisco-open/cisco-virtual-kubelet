# Network as Code — Configuration and Drift Detection

!!! warning "Beta"
    `IOSXEConfig`, `NXOSConfig`, and the Network as Code feature set are **Beta**
    (`v1alpha1`). The family API, CRD schema, and drift semantics are
    functional and integration-tested. Schema fields may change between
    releases. Evaluate in non-production environments before broader rollout.

Cisco Virtual Kubelet extends Kubernetes with declarative device configuration
management. You express network intent as plain YAML (`IOSXEConfig` or
`NXOSConfig` CRs), and CVK reconciles the device to match — detecting drift,
applying changes, and verifying observed state after apply. IOS-XE has the
broadest family coverage and revision/apply-log history; NX-OS starts with a
per-device NX-API slice for `system`, `feature`, `feature_set`, `vlan`, and
`interface_ethernet`.

## Concepts

### Families

A *family* is one independently managed slice of device configuration:
`vlan`, `bgp`, `ospf`, `aaa`, `prefix_list`, and so on for IOS-XE, and
`system`, `feature`, `feature_set`, `vlan`, and `interface_ethernet` for the
initial NX-OS slice. Each
family has its own writer that knows how to fetch, diff, and patch the relevant
device state.

Each `IOSXEConfig` or `NXOSConfig` lists the families it owns via
`managedFamilies`. CVK only touches those families; everything else on the
device is left untouched.

### Intent hierarchy

CVK resolves configuration through a layered hierarchy before any YANG
operation reaches the device:

```
IOSXEConfigDefaults      (cluster-wide baseline)
  ↓
IOSXEDeviceGroupConfig   (group of devices, e.g. all access switches)
  ↓
IOSXEInterfaceGroupConfig (shared interface templates, e.g. trunk ports)
  ↓
IOSXETemplate            (Jinja2 / Go text-template fragments)
  ↓
IOSXEConfig              (per-device final intent)
```

Lower layers override higher ones. The resolver merges all applicable
layers and produces a single intent tree that is handed to family writers.

### Drift detection

After each successful apply, CVK fetches the live state of each managed
family from the device and compares it to the resolved intent. Divergence
triggers a `Drifted` phase. The `driftPolicy` field controls what happens:

| `driftPolicy` | Behaviour |
|---|---|
| `report` | Log the diff and surface it in `status`; do not patch. |
| `revert` | Re-apply the intended family config immediately. |
| `pause` | Stop reconciliation until the CR is changed or the pause is removed. |

`driftDetectInterval` sets how frequently CVK polls for drift (default
`5m`).

---

## Quick start

### 1. Write your first IOSXEConfig

```yaml
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEConfig
metadata:
  name: access-vlans
  namespace: default
spec:
  deviceRef:
    name: cat9300-floor1
  managedFamilies:
    - vlan
  driftPolicy: report
  writeStartup: true
  source:
    inline:
      vlan:
        vlans:
          - id: 100
            name: DATA
          - id: 200
            name: VOICE
          - id: 300
            name: MGMT
```

```bash
kubectl apply -f access-vlans.yaml
```

Watch the reconciliation progress:

```bash
$ kubectl get iosxeconfig -w
NAME           DEVICE            PHASE    DRIFT   AGE
access-vlans   cat9300-floor1    InSync   none    12s
```

### 2. Inspect the apply log

```bash
$ kubectl get iosxelog -l config.cisco.vk/config=access-vlans
NAME                        DEVICE            FAMILIES   RESULT    AGE
access-vlans-20260530       cat9300-floor1    vlan        Applied   12s

$ kubectl describe iosxeconfigapplylog access-vlans-20260530
...
Status:
  Applied At:   2026-05-30T10:00:12Z
  Duration:     0.8s
  Op Count:     3
  Result:       Applied
  Revision Ref:
    Name:  access-vlans-v1
```

### 3. Introduce drift manually and observe

SSH to the device and rename VLAN 100 from `DATA` to `something-else`.
Within the next `driftDetectInterval` cycle:

```bash
$ kubectl get iosxeconfig
NAME           DEVICE            PHASE     DRIFT    AGE
access-vlans   cat9300-floor1    Drifted   [vlan]   5m

$ kubectl describe iosxeconfig access-vlans
...
Status:
  Family Statuses:
    - Family:  vlan
      Phase:   Drifted
      Drift Summary: |
        observed vlan 100 name: "something-else"
        desired  vlan 100 name: "DATA"
  Phase:  Drifted
Events:
  Type     Reason   Age   Message
  ----     ------   ---   -------
  Warning  Drifted  30s   family vlan: 1 leaf divergence (driftPolicy=report)
```

Switch to `driftPolicy: revert` to have CVK automatically remediate:

```bash
kubectl patch iosxeconfig access-vlans \
  --type=merge -p '{"spec":{"driftPolicy":"revert"}}'
```

---

## Multiple families

You can manage several families in a single CR:

```yaml
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEConfig
metadata:
  name: cat9300-floor1-network
  namespace: default
spec:
  deviceRef:
    name: cat9300-floor1
  managedFamilies:
    - vlan
    - spanning_tree
    - snmp_server
    - logging
  driftPolicy: revert
  writeStartup: true
  source:
    inline:
      vlan:
        vlans:
          - id: 100
            name: DATA
          - id: 200
            name: VOICE
      spanning_tree:
        mode: rapid-pvst
        extend:
          system-id: true
      snmp_server:
        community:
          - name: public
            ro: true
        location: "Floor 1 IDF"
        contact: "noc@example.com"
      logging:
        buffered: 65536
        trap: informational
```

Each family is reconciled independently. A failure in one family does not
block others.

---

## IOSXEConfigBundle — fan-out to many devices

When the same config should apply to a group of devices, use
`IOSXEConfigBundle` instead of creating one `IOSXEConfig` per device:

```yaml
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEConfigBundle
metadata:
  name: access-layer-vlans
  namespace: default
spec:
  selector:
    matchLabels:
      tier: access
  template:
    spec:
      managedFamilies:
        - vlan
      driftPolicy: revert
      writeStartup: true
      source:
        inline:
          vlan:
            vlans:
              - id: 100
                name: DATA
              - id: 200
                name: VOICE
```

The `IOSXEConfigBundle` controller watches `CiscoDevice` objects matching
the selector and creates one `IOSXEConfig` per device. Changes to the
bundle template propagate to all child CRs automatically.

Label your devices:

```bash
kubectl label ciscodevice cat9300-floor1 tier=access
kubectl label ciscodevice cat9300-floor2 tier=access
```

```bash
$ kubectl get iosxeconfig -l config.cisco.vk/bundle=access-layer-vlans
NAME                            DEVICE            PHASE    DRIFT   AGE
access-layer-vlans-cat9300-floor1   cat9300-floor1   InSync   none    2m
access-layer-vlans-cat9300-floor2   cat9300-floor2   InSync   none    2m
```

---

## Rollback via IOSXEConfigRevision

Each successful apply snapshots the resolved intent as an
`IOSXEConfigRevision`. List them:

```bash
$ kubectl get iosxeconfigrevision -l config.cisco.vk/config=cat9300-floor1-network
NAME                          CONFIG                       AGE
cat9300-floor1-network-v1     cat9300-floor1-network       1h
cat9300-floor1-network-v2     cat9300-floor1-network       30m
cat9300-floor1-network-v3     cat9300-floor1-network       5m
```

Roll back by patching the `IOSXEConfig` to reference an older revision:

```bash
kubectl patch iosxeconfig cat9300-floor1-network \
  --type=merge \
  -p '{"spec":{"rollbackTo":"cat9300-floor1-network-v2"}}'
```

The reconciler re-applies the snapshotted intent and creates a new
revision reflecting the rolled-back state.

---

## Intent hierarchy in practice

### IOSXEConfigDefaults

Cluster-wide defaults apply to every device unless overridden:

```yaml
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEConfigDefaults
metadata:
  name: global
spec:
  source:
    inline:
      snmp_server:
        location: "Data Centre"
        contact: "noc@example.com"
      logging:
        buffered: 32768
```

### IOSXEDeviceGroupConfig

Per-group overrides for a set of devices:

```yaml
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEDeviceGroupConfig
metadata:
  name: campus-access
spec:
  selector:
    matchLabels:
      tier: access
  source:
    inline:
      spanning_tree:
        mode: rapid-pvst
```

### IOSXETemplate (Jinja2)

Reusable templates with parameterisation:

```yaml
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXETemplate
metadata:
  name: standard-vlans
spec:
  template: |
    vlan:
      vlans:
        {% for v in vlans %}
        - id: {{ v.id }}
          name: {{ v.name }}
        {% endfor %}
  parameters:
    vlans:
      - id: 100
        name: DATA
      - id: 200
        name: VOICE
```

Reference the template from an `IOSXEConfig`:

```yaml
spec:
  source:
    templateRef:
      name: standard-vlans
      parameters:
        vlans:
          - id: 100
            name: USERS
          - id: 300
            name: IOT
```

---

## Transactional apply

For families that support NETCONF confirmed-commit, set
`spec.transactional: true`. This wraps all family operations in a single
NETCONF transaction that is automatically rolled back if the timer expires
before a confirm is sent:

```yaml
spec:
  transactional: true
  source:
    inline:
      ospf:
        processes:
          - id: 1
            router-id: "10.0.0.1"
```

Use transactional mode when the risk of partial apply leaving the device
in an inconsistent state is unacceptable.

---

## Startup config persistence

`writeStartup: true` causes CVK to call the IOS-XE `copy running-config
startup-config` equivalent after every successful apply, ensuring the
intent survives a device reload. Leave it `false` (default) during
iterative development to keep changes transient.

---

## NX-OS quick start

Use `NXOSConfig` for NX-OS devices. It uses the same common status and
drift-policy fields as `IOSXEConfig`, but the first runtime slice is scoped to
`system`, `feature`, `feature_set`, `vlan`, and `interface_ethernet` over
NX-API REST/DME.

`NXOSConfig` accepts a direct resolved family map. For compatibility, a native
CVK-authored source that omits `modelSource` may also use a full `nxos:`
NetAsCode envelope; CVK can resolve `global`, matching `device_groups`, the
selected `devices` entry, model `templates`, `variables`, and
`interface_groups`. That compatibility resolver is not the strict import path.
When `modelSource.resolved: true` is declared, the source must already be
flattened per-device canonical data and retain `interfaces.ethernets`; CVK only
normalizes that section to its internal `interface_ethernet` writer family.

```yaml
apiVersion: config.cisco.vk/v1alpha1
kind: NXOSConfig
metadata:
  name: nexus9300v-system
  namespace: default
spec:
  deviceRef:
    name: nexus9300v-01
  managedFamilies:
    - system
    - interface_ethernet
  driftPolicy: report
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
      interfaces:
        ethernets:
          - id: 1/1
            mtu: 9216
            shutdown: false
            description: CVK uplink
```

```bash
$ kubectl get nxosconfig
NAME                  DEVICE          PHASE    DRIFT    AGE
nexus9300v-system     nexus9300v-01   InSync   report   30s
```

When `modelSource` is present, NX-OS reconciliation fails closed unless the
payload identifies an exact supported conformance contract. The initial
contract identifies Network as Code module `0.3.0` at revision
`706c1b390b7c23f8950714788129b1c51233de6a`, the normalized NX-OS schema
subtree at qualified schema snapshot
`9e45ad51227a2e534c5ded8f3258c4feb9a53c5d`, and was qualified with NX-OS
provider `0.13.1` and utils provider `2.0.0`. These exact provider versions are
the official tested baseline; the module constraints themselves permit a
range. `schemaDigest` is the SHA-256 of `jq -cS '.properties.nxos' schema.json`
including the command's trailing LF. It is a compatibility label, not a
payload-integrity digest or a substitute for schema validation. `exporter`
must identify an immutable `name@version` or `name@digest`, and
`sourceRevision` identifies the immutable customer intent revision. Native
CVK-authored payloads may omit `modelSource`; declaring it opts the CR into
strict flattened-source and model/device contract validation before
configuration fetch or write.

The supported slice is guarded by checked-in golden artifacts generated from
the pinned Terraform plan and NX-OS provider DME bodies. Conformance compares
device path, DME class ancestry, object identity, attributes, and values while
ignoring only empty `attributes` containers and operation batching. In
particular, VLAN payloads do not add a provider-external `pcTag`, and Ethernet
payloads retain the provider-derived `adminSt`, `layer`, and
`userCfgdFlags` values.

CVK adds a fail-closed interface safety boundary around those defaults. A
strict import cannot implicitly convert an observed Layer-3 interface to
Layer-2, and it cannot bring up an observed shutdown interface unless
`shutdown: false` is explicit. Native CVK sources preserve omitted admin/layer
properties on description or MTU-only updates. These protections deliberately
narrow the upstream provider behavior to avoid accidental connectivity
changes.

### NX-OS runtime coverage

NX-OS uses the Network as Code `nxos` model shape, but CVK only writes the
fields listed below today. Unsupported fields fail closed instead of being
silently ignored. If `spec.managedFamilies` names a family outside this
supported table, the common runtime marks the CR `Failed` before contacting the
device; planned families remain documentation until their writer and verify
fixtures are present. `CiscoDevice.spec.configPrereqs` uses the same NX-OS
family gate when it creates an owned `NXOSConfig`, so unsupported explicit or
source-derived families fail at CiscoDevice reconciliation time.

| Family | Supported fields | Not supported in this slice |
|---|---|---|
| `system` | `system.hostname`, `system.mtu` | broader Ethernet defaults, boot, clock, NX-API, SSH, and platform subtrees |
| `feature` | NetAsCode feature booleans: `analytics`, `bash_shell`, `bfd`, `bgp`, `dhcp`, `evpn`, `fabric_forwarding`, `grpc`, `hsrp`, `interface_vlan`, `isis`, `lacp`, `lldp`, `macsec`, `netflow`, `ngmvpn`, `ngoam`, `nv_overlay`, `nxapi`, `ospf`, `ospfv3`, `pim`, `private_vlan`, `ptp`, `scp_server`, `security_group`, `service_acceleration`, `sflow`, `sftp_server`, `ssh`, `tacacs`, `telemetry`, `telnet`, `udld`, `vn_segment_vlan_based`, `vpc` | provider aliases such as `hmm`, `pvlan`, and `vn_segment`; disabling `nxapi`, `ssh`, `scp_server`, `sftp_server`, or `tacacs` through `NXOSConfig` is rejected to avoid management lockout |
| `feature_set` | `feature_set.fex`, `feature_set.mpls`, `feature_set.virtualization` | none in the NetAsCode boolean slice |
| `vlan` | `vlan.vlans[].id`, `vlan.vlans[].name` | VNI / VXLAN leaves such as `vni` or `vn_segment`; prune deletes are supported for CR-owned VLANs except VLAN 1 |
| `interface_ethernet` | `interfaces.ethernets[].id`, `description`, `shutdown`, `mtu` | switchport, IP/IPv6, channel-group, OSPF, PIM, ACL/NAT attachments; physical interface deletion/prune is intentionally unsupported |

Strict NetAsCode imports require Boolean values for every `feature.*` and
`feature_set.*` leaf, matching the pinned schema. Native CVK sources may still
use the writer's provider-state string compatibility forms. CVK also
deliberately refuses to disable `nxapi`, `ssh`, `scp_server`, `sftp_server`, or
`tacacs`; that management-lockout protection is a CVK safety extension to the
upstream module rather than identical behavior.

Create/update payload parity does not imply Terraform lifecycle parity. An
omitted CVK field normally means “leave unchanged,” while the Terraform
provider can emit unset markers or child deletes when an optional field is
removed from its state. Destructive removal support therefore remains
family-specific and fail-closed until ownership and live cleanup tests exist.

Planned NX-OS family waves are tracked in
[Production Readiness](production-readiness.md). The parity target is the
current Network as Code NX-OS data-model stripe, which is split into entity,
device, and interface sections.

Entity/source pattern coverage:

| Pattern | Status |
|---|---|
| Direct per-device family data, including canonical `interfaces.ethernets` | Required when `modelSource.resolved: true` declares strict imported provenance. |
| `devices`, `device_groups`, `global` | Native compatibility only when `modelSource` is omitted; CVK performs expansion. |
| `variables`, `templates` with `type: model`, `interface_groups` | Native compatibility only when `modelSource` is omitted; rejected in strict resolved imports. |
| `managed_devices`, `managed_device_groups` | Planned for a future source/orchestration layer; one `NXOSConfig` reconciles one selected device today. |
| `yaml_files`, `yaml_directories`, file templates, `write_model_file` | Deferred; render these outside the controller and submit resolved intent. |
| ordered `cli_templates` | Deferred for config reconciliation; use `DeviceOperation` for explicit NX-API CLI execution. |

If an owned source-derived `NXOSConfig` prereq CR is externally deleted during
teardown, the controller cannot reconstruct prior owned keys from the vanished
status. It records `PrereqTeardownObserved=True` with a warning event instead
of blocking device deletion indefinitely; operators should inspect the device
for any orphaned prereq state.

Full family parity is not claimed until each family has DME mapping,
Fetch -> Diff -> Apply -> Verify behavior, fake transport tests, and an
optional guarded live write test. The current target set is:

| Wave | Families |
|---|---|
| Management/base | `aaa`, `banner`, `cdp`, `clock`, `dns`, `lldp`, `logging`, `ntp`, `nxapi`, `snmp`, `ssh`, `system`, `udld` |
| L2 and interfaces | `arp`, `interface_ethernet`, `interface_loopback`, `interface_management`, `interface_port_channel`, `interface_subinterface`, `interface_vlan`, `spanning_tree`, `vlan`, `vpc` |
| L3 and routing | `bfd`, `bgp`, `dhcp`, `hsrp`, `ip_route`, `ipv6_route`, `isis`, `nd`, `ospf`, `ospfv3`, `pim`, `ptp`, `vrf` |
| Policy/security | `community_list`, `hypershield`, `ip_access_list`, `ip_prefix_list`, `ipv6_access_list`, `ipv6_prefix_list`, `key_chain`, `qos`, `route_map`, `security_group`, `span` |
| Fabric and telemetry | `analytics`, `evpn`, `fabric_forwarding`, `interface_nve`, `netflow`, `sflow`, `telemetry` |

### NX-OS REST/DME semantics

`NXOSConfig` writes through NX-API REST/DME and reports transport kind
`nxapi`. It does not use NETCONF candidate datastores, gNMI
transactions, or confirmed commit. That means:

- `spec.transactional` cannot make an NX-OS DME apply atomic.
- `spec.confirmTimeoutSeconds` cannot provide device-native auto-revert.
- CVK relies on Fetch -> Diff -> Apply -> Verify for every supported family.
- `writeStartup: true` is safe only after every managed family verifies.
- `revisionHistoryLimit`, `rollbackTo`, and declarative rollback are rejected
  for NX-OS today; recovery requires an explicitly reviewed compensating
  configuration or device-native operational procedure.

When a CR requests confirmed-commit behavior that the selected transport cannot
provide, CVK records a warning event and a stable status entry under
`status.transportFallbacks`, for example:

```yaml
status:
  transportFallbacks:
    - type: ConfirmedCommit
      reason: non-transactional reconcile
      message: spec.confirmTimeoutSeconds set but auto-revert path is unavailable on this transport
```

---

## Available configuration families

For a complete reference of all available IOS-XE families and their YAML
shapes, see the [Family Reference](reference/families/README.md). For YANG
version compatibility and the version override architecture, see
[YANG Version Support](yang-version-support.md).

Key families available today:

| Category | Families |
|---|---|
| L2 | `vlan`, `spanning_tree`, `interface_switchport` |
| L3 interfaces | `interface_loopback`, `interface_vlan`, `interface_ethernet`, `interface_port_channel` |
| Routing | `ospf`, `bgp`, `static_route`, `vrf` |
| Security | `aaa`, `access_list_standard`, `access_list_extended`, `prefix_list`, `ip_community_list`, `ip_as_path_access_list` |
| Crypto | `crypto_ikev2_profile`, `crypto_ipsec_profile`, `crypto_ipsec_transform_set` |
| Policy | `route_map`, `class_map`, `policy_map` |
| Management | `snmp_server`, `logging`, `ntp`, `banner`, `clock`, `system` |
| Multicast | `pim`, `ipv6_pim` |
| NAT | `interface_ethernet` (NAT ip access-group leaves) |
| App/Automation | `event_manager` |
