# interface_switchport

Status: **implemented** — CVK writes the managed leaves listed below on every reconcile.

- Shape: `keyed_list`
- Key field(s): `type, name`
- netascode inner key: `interfaces`
- Depends on: [interface_ethernet](interface_ethernet.md), [vlan](vlan.md)
- netascode portal: [https://netascode.cisco.com/docs/data_models/iosxe/interface/switchport/](https://netascode.cisco.com/docs/data_models/iosxe/interface/switchport/)

## YANG paths

- `/Cisco-IOS-XE-native:native/interface/GigabitEthernet/switchport`


## Managed leaves

The writer reads and writes the following leaves. Leaves outside this
set present on the device are preserved (additive merge).

- `mode`
- `access`
- `trunk`

