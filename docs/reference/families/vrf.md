# vrf

Status: **implemented** — CVK writes the managed leaves listed below on every reconcile.

- Shape: `keyed_list`
- Key field(s): `name`
- netascode inner key: `vrfs`
- netascode portal: [https://netascode.cisco.com/docs/data_models/iosxe/device/vrf/](https://netascode.cisco.com/docs/data_models/iosxe/device/vrf/)

## YANG paths

- `/Cisco-IOS-XE-native:native/vrf/definition`


## Managed leaves

The writer reads and writes the following leaves. Leaves outside this
set present on the device are preserved (additive merge).

- `rd`
- `description`

