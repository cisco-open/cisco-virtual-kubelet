# static_route

Status: **implemented** — CVK writes the managed leaves listed below on every reconcile.

- Shape: `keyed_list`
- Key field(s): `prefix`
- netascode inner key: `routes`
- Depends on: [vrf](vrf.md)
- netascode portal: [https://netascode.cisco.com/docs/data_models/iosxe/device/static_route/](https://netascode.cisco.com/docs/data_models/iosxe/device/static_route/)

## YANG paths

- `/Cisco-IOS-XE-native:native/ip/route`


## Managed leaves

The writer reads and writes the following leaves. Leaves outside this
set present on the device are preserved (additive merge).

- `mask`
- `fwd-list`
- `distance`
- `tag`
- `description`

