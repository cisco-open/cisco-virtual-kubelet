# policy_map

Status: **implemented** — CVK writes the managed leaves listed below on every reconcile.

- Shape: `keyed_list`
- Key field(s): `name`
- netascode inner key: `policy_maps`
- Depends on: [class_map](class_map.md)
- netascode portal: [https://netascode.cisco.com/docs/data_models/iosxe/device/policy_map/](https://netascode.cisco.com/docs/data_models/iosxe/device/policy_map/)

## YANG paths

- `/Cisco-IOS-XE-native:native/policy/Cisco-IOS-XE-policy:policy-map`


## Managed leaves

The writer reads and writes the following leaves. Leaves outside this
set present on the device are preserved (additive merge).

- `description`
- `class`
- `type`
