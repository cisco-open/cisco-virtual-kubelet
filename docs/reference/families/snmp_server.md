# snmp_server

Status: **implemented** — CVK writes the managed leaves listed below on every reconcile.

- Shape: `singleton`
- netascode portal: [https://netascode.cisco.com/docs/data_models/iosxe/device/snmp_server/](https://netascode.cisco.com/docs/data_models/iosxe/device/snmp_server/)

## YANG paths

- `/Cisco-IOS-XE-native:native/snmp-server`


## Managed leaves

The writer reads and writes the following leaves. Leaves outside this
set present on the device are preserved (additive merge).

- `community`
- `community-config`
- `Cisco-IOS-XE-snmp:community-config`
- `location`
- `contact`
- `Cisco-IOS-XE-snmp:contact`
- `Cisco-IOS-XE-snmp:location`
- `trap-source`
- `host`
