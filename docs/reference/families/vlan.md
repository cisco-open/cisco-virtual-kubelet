# vlan

Status: **implemented** — CVK writes the managed leaves listed below on every reconcile.

- Shape: `keyed_list`
- Key field(s): `id`
- netascode inner key: `vlans`
- netascode portal: [https://netascode.cisco.com/docs/data_models/iosxe/device/vlan/](https://netascode.cisco.com/docs/data_models/iosxe/device/vlan/)

## YANG paths

- `/Cisco-IOS-XE-native:native/vlan/Cisco-IOS-XE-vlan:vlan-list`


## Managed leaves

The writer reads and writes the following leaves. Leaves outside this
set present on the device are preserved (additive merge).

- `name`
- `shutdown`

