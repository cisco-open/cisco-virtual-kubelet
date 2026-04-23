# class_map

Status: **implemented** — CVK writes the managed leaves listed below on every reconcile.

- Shape: `keyed_list`
- Key field(s): `name`
- netascode inner key: `class_maps`
- Depends on: [access_list_extended](access_list_extended.md), [access_list_standard](access_list_standard.md)
- netascode portal: [https://netascode.cisco.com/docs/data_models/iosxe/device/class_map/](https://netascode.cisco.com/docs/data_models/iosxe/device/class_map/)

## YANG paths

- `/Cisco-IOS-XE-native:native/policy/Cisco-IOS-XE-policy:class-map`


## Managed leaves

The writer reads and writes the following leaves. Leaves outside this
set present on the device are preserved (additive merge).

- `description`
- `match`
- `match-type`

