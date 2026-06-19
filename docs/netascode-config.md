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
per-device NX-API slice for `system`, `vlan`, and `interface_ethernet`.

## Concepts

### Families

A *family* is one independently managed slice of device configuration:
`vlan`, `bgp`, `ospf`, `aaa`, `prefix_list`, and so on for IOS-XE, and
`system`, `vlan`, and `interface_ethernet` for the initial NX-OS slice. Each
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
  -p '{"spec":{"revisionRef":{"name":"cat9300-floor1-network-v2"}}}'
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
`system`, `vlan`, and `interface_ethernet` over NX-API REST/DME.

`NXOSConfig` accepts either a direct resolved family map or a full
`nxos:` NetAsCode envelope. When a full envelope is supplied, CVK resolves
`global`, matching `device_groups`, the selected `devices` entry, model
`templates`, `variables`, and `interface_groups` before normalizing
`interfaces.ethernets` into the runtime `interface_ethernet` family. Template
types other than `model` are rejected; render CLI separately and submit only
resolved model intent.

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
  driftPolicy: report
  modelSource:
    format: netascode-nxos
    resolved: true
  source:
    inline:
      nxos:
        global:
          variables:
            uplink_mtu: 9216
        interface_groups:
          - name: routed-uplinks
            configuration:
              mtu: ${uplink_mtu}
              shutdown: false
        devices:
          - name: nexus9300v-01
            configuration:
              system:
                hostname: nexus9300v-01
              interfaces:
                ethernets:
                  - id: 1/1
                    interface_groups:
                      - routed-uplinks
                    description: CVK uplink
```

```bash
$ kubectl get nxosconfig
NAME                  DEVICE          PHASE    DRIFT    AGE
nexus9300v-system     nexus9300v-01   InSync   report   30s
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
