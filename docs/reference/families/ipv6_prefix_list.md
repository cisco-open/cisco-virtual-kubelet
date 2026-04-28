# ipv6_prefix_list

Status: **implemented** — CVK writes the managed leaves listed below on every reconcile.

- Shape: `keyed_list`
- Key field(s): `name`
- netascode inner key: `prefixes`
- netascode portal: [https://netascode.cisco.com/docs/data_models/iosxe/device/prefix_list/](https://netascode.cisco.com/docs/data_models/iosxe/device/prefix_list/)

## YANG paths

- `/Cisco-IOS-XE-native:native/ipv6/prefix-list/prefixes`


## Managed leaves

The writer reads and writes the following leaves. Leaves outside this
set present on the device are preserved (additive merge).

- `description`
- `sequences`
