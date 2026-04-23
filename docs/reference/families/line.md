# line

Status: **implemented** — CVK writes the managed leaves listed below on every reconcile.

- Shape: `singleton`
- netascode inner key: `vty`
- Depends on: [aaa](aaa.md)
- netascode portal: [https://netascode.cisco.com/docs/data_models/iosxe/device/line/](https://netascode.cisco.com/docs/data_models/iosxe/device/line/)

## YANG paths

- `/Cisco-IOS-XE-native:native/line`


## Managed leaves

The writer reads and writes the following leaves. Leaves outside this
set present on the device are preserved (additive merge).

- `last`
- `transport`
- `exec-timeout`
- `login`
- `password`

