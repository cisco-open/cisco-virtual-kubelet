# dhcp

Status: **implemented** — CVK writes the managed leaves listed below on every reconcile.

- Shape: `singleton`
- netascode inner key: `pools`
- netascode portal: [https://netascode.cisco.com/docs/data_models/iosxe/device/dhcp/](https://netascode.cisco.com/docs/data_models/iosxe/device/dhcp/)

## YANG paths

- `/Cisco-IOS-XE-native:native/ip/dhcp`


## Managed leaves

The writer reads and writes the following leaves. Leaves outside this
set present on the device are preserved (additive merge).

- `network`
- `prefix_length`
- `default_router`

