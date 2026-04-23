# crypto_pki_trustpoint

Status: **implemented** — CVK writes the managed leaves listed below on every reconcile.

- Shape: `keyed_list`
- Key field(s): `id`
- netascode inner key: `trustpoints`
- netascode portal: [https://netascode.cisco.com/docs/data_models/iosxe/device/crypto_pki_trustpoint/](https://netascode.cisco.com/docs/data_models/iosxe/device/crypto_pki_trustpoint/)

## YANG paths

- `/Cisco-IOS-XE-native:native/crypto/pki/trustpoint`


## Managed leaves

The writer reads and writes the following leaves. Leaves outside this
set present on the device are preserved (additive merge).

- `enrollment`
- `revocation-check`
- `rsakeypair`
- `subject-name`
- `fingerprint`

