# crypto_ikev2_profile

Status: **implemented** — CVK writes the managed leaves listed below on every reconcile.

- Shape: `keyed_list`
- Key field(s): `name`
- netascode inner key: `profiles`
- Depends on: [crypto_pki_trustpoint](crypto_pki_trustpoint.md)
- netascode portal: [https://netascode.cisco.com/docs/data_models/iosxe/device/crypto_ikev2_profile/](https://netascode.cisco.com/docs/data_models/iosxe/device/crypto_ikev2_profile/)

## YANG paths

- `/Cisco-IOS-XE-native:native/crypto/ikev2/profile`


## Managed leaves

The writer reads and writes the following leaves. Leaves outside this
set present on the device are preserved (additive merge).

- `authentication`
- `identity`
- `keyring`
- `match`
- `pki`
- `lifetime`
