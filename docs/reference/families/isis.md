# isis

Status: **implemented** — CVK writes the managed leaves listed below on every reconcile.

- Shape: `keyed_list`
- Key field(s): `tag`
- netascode inner key: `processes`
- Depends on: [interface_ethernet](interface_ethernet.md), [interface_loopback](interface_loopback.md)
- netascode portal: [https://netascode.cisco.com/docs/data_models/iosxe/device/isis/](https://netascode.cisco.com/docs/data_models/iosxe/device/isis/)

## YANG paths

- `/Cisco-IOS-XE-native:native/router/Cisco-IOS-XE-isis:router-isis`


## Managed leaves

The writer reads and writes the following leaves. Leaves outside this
set present on the device are preserved (additive merge).

- `net`
- `is-type`
- `metric-style`
- `log-adjacency-changes`
- `passive-interface`
- `address-family`
