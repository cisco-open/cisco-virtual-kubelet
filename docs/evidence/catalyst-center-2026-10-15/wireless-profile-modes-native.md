# Native isolated FlexConnect and Fabric profile definitions

Both tests use the existing authenticated client, profile compiler/executor,
task polling and authoritative readback. Each starts with absent random names,
creates a disabled OPEN 5 GHz SSID, then an unassigned profile. Before each
mutation, the harness checks exact owned identities, absence of device/site
assignments and unchanged unrelated SSID/profile inventory. Uncertain outcomes
retain fixture identities for inspection instead of replaying or guessing cleanup.

| Test | Native create | Same-UUID update | Result |
| --- | --- | --- | --- |
| `TestLiveWirelessFlexProfileExecution` | `enable_fabric: false`, `enable_flex_connect: true`, local VLAN 100 | Local VLAN 200 plus WLAN profile name change | Pass, 14.618s overall |
| `TestLiveWirelessFabricProfileExecution` | `enable_fabric: true`, `enable_flex_connect: false`, no interface/VLAN selector | WLAN profile name change while retaining Fabric settings | Pass, 15.626s overall |

Each accepted create/update task is followed by independent profile inventory
verification and a fresh compiler no-op check. Cleanup removes the exact profile
UUID before the exact disabled SSID UUID and verifies restored public baselines.
No device assignment, provisioning or device operation was submitted.

Reproduce using the existing live-test credential environment and explicit flags:

```sh
CATC_WIRELESS_PROFILE_EXECUTION=isolated-definitions \
CATC_WIRELESS_FLEX_PROFILE_EXECUTION=isolated-definitions \
go test -race -tags live ./internal/controlleradapter/catalystcenter \
  -run '^TestLiveWirelessFlexProfileExecution$' -count=1 -v

CATC_WIRELESS_PROFILE_EXECUTION=isolated-definitions \
CATC_WIRELESS_FABRIC_PROFILE_EXECUTION=isolated-definitions \
go test -race -tags live ./internal/controlleradapter/catalystcenter \
  -run '^TestLiveWirelessFabricProfileExecution$' -count=1 -v
```

The existing journal's public restart/failure semantics are fixture-qualified
separately. These live tests qualify metadata definitions and selected field
mapping only. They do not qualify switching-mode migration, Fabric associations,
installed actors, WLC/AP deployment or service behavior. Validating incompatible
mode mixtures locally is covered in [switching-mode evidence](wireless-switching-modes.md).
Exact native task and fixture IDs are in `wireless-flex-profile-native.log` and
`wireless-fabric-profile-native.log`.
