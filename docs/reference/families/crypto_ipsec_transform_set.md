# crypto_ipsec_transform_set

Status: **implemented** — CVK writes the managed leaves listed below on every reconcile.

- Shape: `keyed_list`
- Key field(s): `tag`
- netascode inner key: `transform_sets`
- netascode portal: [https://netascode.cisco.com/docs/data_models/iosxe/device/crypto_ipsec_transform_set/](https://netascode.cisco.com/docs/data_models/iosxe/device/crypto_ipsec_transform_set/)

## YANG paths

- `/Cisco-IOS-XE-native:native/crypto/ipsec/transform-set`


## Managed leaves

The writer reads and writes the following leaves. Leaves outside this
set present on the device are preserved (additive merge).

- `esp`
- `ah`
- `mode`

