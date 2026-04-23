# interface_virtual_port_group

Status: **implemented** — CVK writes the managed leaves listed below on every reconcile.

- Shape: `keyed_list`
- Key field(s): `id`
- netascode inner key: `interfaces`
- Depends on: [vrf](vrf.md)
- netascode portal: [https://netascode.cisco.com/docs/data_models/iosxe/interface/virtual_port_group/](https://netascode.cisco.com/docs/data_models/iosxe/interface/virtual_port_group/)

## YANG paths

- `/Cisco-IOS-XE-native:native/interface/VirtualPortGroup`


## Managed leaves

The writer reads and writes the following leaves. Leaves outside this
set present on the device are preserved (additive merge).

- `description`
- `ipv4_address`
- `ipv4_address_mask`
- `shutdown`

