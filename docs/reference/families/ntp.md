# ntp

Status: **implemented** — CVK writes the managed leaves listed below on every reconcile.

- Shape: `singleton`
- netascode inner key: `servers`
- netascode portal: [https://netascode.cisco.com/docs/data_models/iosxe/device/ntp/](https://netascode.cisco.com/docs/data_models/iosxe/device/ntp/)

## YANG paths

- `/Cisco-IOS-XE-native:native/ntp`


## Managed leaves

The writer reads and writes the following leaves. Leaves outside this
set present on the device are preserved (additive merge).

- `prefer`
- `source`
- `key`
- `version`

