# ip_as_path_access_list

Status: **implemented** — CVK writes the managed leaves listed below on every reconcile.

- Shape: `keyed_list`
- Key field(s): `name`
- netascode inner key: `as_path_access_lists`
- netascode portal: [https://netascode.cisco.com/docs/data_models/iosxe/device/as_path_access_list/](https://netascode.cisco.com/docs/data_models/iosxe/device/as_path_access_list/)

## YANG paths

- `/Cisco-IOS-XE-native:native/ip/as-path/access-list`


## Managed leaves

The writer reads and writes the following leaves. Leaves outside this
set present on the device are preserved (additive merge).

- `action-list`
