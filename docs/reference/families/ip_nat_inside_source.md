# ip_nat_inside_source

Status: **implemented** — CVK writes the managed leaves listed below on every reconcile.

- Shape: `singleton`
- Depends on: [access_list_extended](access_list_extended.md), [ip_nat_pool](ip_nat_pool.md)
- netascode portal: [https://netascode.cisco.com/docs/data_models/iosxe/device/ip_nat/](https://netascode.cisco.com/docs/data_models/iosxe/device/ip_nat/)

## YANG paths

- `/Cisco-IOS-XE-native:native/ip/nat/inside/source`


## Managed leaves

The writer reads and writes the following leaves. Leaves outside this
set present on the device are preserved (additive merge).

- `list`
- `static`
- `route-map`

