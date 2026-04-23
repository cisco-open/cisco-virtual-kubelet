# tacacs_server

Status: **implemented** — CVK writes the managed leaves listed below on every reconcile.

- Shape: `keyed_list`
- Key field(s): `name`
- netascode inner key: `servers`
- Depends on: [aaa](aaa.md)
- netascode portal: [https://netascode.cisco.com/docs/data_models/iosxe/device/tacacs_server/](https://netascode.cisco.com/docs/data_models/iosxe/device/tacacs_server/)

## YANG paths

- `/Cisco-IOS-XE-native:native/tacacs/server`


## Managed leaves

The writer reads and writes the following leaves. Leaves outside this
set present on the device are preserved (additive merge).

- `address`
- `key`
- `port`
- `single-connection`
- `timeout`

