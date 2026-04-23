# bgp

Status: **implemented** — CVK writes the managed leaves listed below on every reconcile.

- Shape: `singleton`
- Depends on: [vrf](vrf.md), [interface_ethernet](interface_ethernet.md), [interface_loopback](interface_loopback.md)
- netascode portal: [https://netascode.cisco.com/docs/data_models/iosxe/device/bgp/](https://netascode.cisco.com/docs/data_models/iosxe/device/bgp/)

## YANG paths

- `/Cisco-IOS-XE-native:native/router/Cisco-IOS-XE-bgp:router-bgp`


## Managed leaves

The writer reads and writes the following leaves. Leaves outside this
set present on the device are preserved (additive merge).

- `id`
- `bgp`
- `neighbor`
- `address-family`
- `redistribute`

