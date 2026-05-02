# access_list_extended

Status: **implemented** — CVK writes the managed leaves listed below on every reconcile.

- Shape: `keyed_list`
- Key field(s): `name`
- netascode inner key: `extended`
- netascode portal: [https://netascode.cisco.com/docs/data_models/iosxe/device/access_list/](https://netascode.cisco.com/docs/data_models/iosxe/device/access_list/)

## YANG paths

- `/Cisco-IOS-XE-native:native/ip/access-list/extended`


## Managed leaves

The writer reads and writes the following leaves. Leaves outside this
set present on the device are preserved (additive merge).

- `rules`
