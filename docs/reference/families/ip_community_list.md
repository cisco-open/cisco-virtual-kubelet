# ip_community_list

Status: **implemented** — CVK writes the managed leaves listed below on every reconcile.

- Shape: `singleton`
- netascode portal: [https://netascode.cisco.com/docs/data_models/iosxe/device/community_list/](https://netascode.cisco.com/docs/data_models/iosxe/device/community_list/)

## YANG paths

- `/Cisco-IOS-XE-native:native/ip/community-list`


## Managed leaves

The writer reads and writes the following leaves. Leaves outside this
set present on the device are preserved (additive merge).

- `standard`
- `expanded`
- `no-advertise`
