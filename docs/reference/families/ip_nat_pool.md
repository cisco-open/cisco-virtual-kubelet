# ip_nat_pool

Status: **implemented** — CVK writes the managed leaves listed below on every reconcile.

- Shape: `keyed_list`
- Key field(s): `id`
- netascode inner key: `pools`
- netascode portal: [https://netascode.cisco.com/docs/data_models/iosxe/device/ip_nat/](https://netascode.cisco.com/docs/data_models/iosxe/device/ip_nat/)

## YANG paths

- `/Cisco-IOS-XE-native:native/ip/nat/pool`


## Managed leaves

The writer reads and writes the following leaves. Leaves outside this
set present on the device are preserved (additive merge).

- `start-address`
- `end-address`
- `netmask`
- `prefix-length`
- `type`

