# interface_ethernet

Status: **implemented** — CVK writes the managed leaves listed below on every reconcile.

- Shape: `keyed_list`
- Key field(s): `type, name`
- netascode inner key: `interfaces`
- Depends on: [vrf](vrf.md), [access_list_extended](access_list_extended.md)
- netascode portal: [https://netascode.cisco.com/docs/data_models/iosxe/interface/ethernet/](https://netascode.cisco.com/docs/data_models/iosxe/interface/ethernet/)

## YANG paths

- `/Cisco-IOS-XE-native:native/interface/GigabitEthernet`
- `/Cisco-IOS-XE-native:native/interface/TwoGigabitEthernet`
- `/Cisco-IOS-XE-native:native/interface/FiveGigabitEthernet`
- `/Cisco-IOS-XE-native:native/interface/TenGigabitEthernet`
- `/Cisco-IOS-XE-native:native/interface/TwentyFiveGigE`
- `/Cisco-IOS-XE-native:native/interface/FortyGigabitEthernet`
- `/Cisco-IOS-XE-native:native/interface/HundredGigE`
- `/Cisco-IOS-XE-native:native/interface/TwoHundredGigE`
- `/Cisco-IOS-XE-native:native/interface/FourHundredGigE`


## Managed leaves

The writer reads and writes the following leaves. Leaves outside this
set present on the device are preserved (additive merge).

- `description`
- `shutdown`
- `mtu`

