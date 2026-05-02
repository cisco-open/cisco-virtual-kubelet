# username

Status: **implemented** — CVK writes the managed leaves listed below on every reconcile.

- Shape: `keyed_list`
- Key field(s): `name`
- netascode inner key: `users`
- netascode portal: [https://netascode.cisco.com/docs/data_models/iosxe/device/username/](https://netascode.cisco.com/docs/data_models/iosxe/device/username/)

## YANG paths

- `/Cisco-IOS-XE-native:native/username`


## Managed leaves

The writer reads and writes the following leaves. Leaves outside this
set present on the device are preserved (additive merge).

- `privilege`
- `secret`
- `password`
- `description`
- `view`
