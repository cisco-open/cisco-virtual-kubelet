# crypto_ipsec_profile

Status: **implemented** — CVK writes the managed leaves listed below on every reconcile.

- Shape: `keyed_list`
- Key field(s): `name`
- netascode inner key: `profiles`
- Depends on: [crypto_ipsec_transform_set](crypto_ipsec_transform_set.md), [crypto_ikev2_profile](crypto_ikev2_profile.md)
- netascode portal: [https://netascode.cisco.com/docs/data_models/iosxe/device/crypto_ipsec_profile/](https://netascode.cisco.com/docs/data_models/iosxe/device/crypto_ipsec_profile/)

## YANG paths

- `/Cisco-IOS-XE-native:native/crypto/ipsec/profile`


## Managed leaves

The writer reads and writes the following leaves. Leaves outside this
set present on the device are preserved (additive merge).

- `set`
- `match`
- `responder-only`

