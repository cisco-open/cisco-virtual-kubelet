# eigrp

Status: **implemented** — CVK writes the managed leaves listed below on every reconcile.

- Shape: `keyed_list`
- Key field(s): `id`
- netascode inner key: `processes`
- Depends on: [vrf](vrf.md), [interface_ethernet](interface_ethernet.md), [interface_loopback](interface_loopback.md)
- netascode portal: [https://netascode.cisco.com/docs/data_models/iosxe/device/eigrp/](https://netascode.cisco.com/docs/data_models/iosxe/device/eigrp/)

## YANG paths

- `/Cisco-IOS-XE-native:native/router/Cisco-IOS-XE-eigrp:router-eigrp`


## Managed leaves

The writer reads and writes the following leaves. Leaves outside this
set present on the device are preserved (additive merge).

- `address-family`
- `network`
- `router-id`
- `metric`
- `eigrp-instance`
