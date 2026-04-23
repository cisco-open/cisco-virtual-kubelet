# radius_server

Status: **implemented** — CVK writes the managed leaves listed below on every reconcile.

- Shape: `keyed_list`
- Key field(s): `id`
- netascode inner key: `servers`
- Depends on: [aaa](aaa.md)
- netascode portal: [https://netascode.cisco.com/docs/data_models/iosxe/device/radius_server/](https://netascode.cisco.com/docs/data_models/iosxe/device/radius_server/)

## YANG paths

- `/Cisco-IOS-XE-native:native/radius/server`


## Managed leaves

The writer reads and writes the following leaves. Leaves outside this
set present on the device are preserved (additive merge).

- `address`
- `key`
- `timeout`
- `retransmit`
- `deadtime`

