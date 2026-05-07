# route_map

Status: **implemented** — CVK writes the managed leaves listed below on every reconcile.

- Shape: `keyed_list`
- Key field(s): `name`
- netascode inner key: `route_maps`
- Depends on: [prefix_list](prefix_list.md), [access_list_standard](access_list_standard.md), [access_list_extended](access_list_extended.md)
- netascode portal: [https://netascode.cisco.com/docs/data_models/iosxe/device/route_map/](https://netascode.cisco.com/docs/data_models/iosxe/device/route_map/)

## YANG paths

- `/Cisco-IOS-XE-native:native/route-map`


## Managed leaves

The writer reads and writes the following leaves. Leaves outside this
set present on the device are preserved (additive merge).

- `description`
- `entries`
- `route-map-without-order-seq`
