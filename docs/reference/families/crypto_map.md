# crypto_map

Status: **implemented** — CVK writes the managed leaves listed below on every reconcile.

- Shape: `keyed_list`
- Key field(s): `name`
- netascode inner key: `maps`
- Depends on: [crypto_ipsec_transform_set](crypto_ipsec_transform_set.md)
- netascode portal: [https://netascode.cisco.com/docs/data_models/iosxe/device/crypto_map/](https://netascode.cisco.com/docs/data_models/iosxe/device/crypto_map/)

## YANG paths

- `/Cisco-IOS-XE-native:native/crypto/map`


## Managed leaves

The writer reads and writes the following leaves. Leaves outside this
set present on the device are preserved (additive merge).

- `sequences`
- `local-address`
