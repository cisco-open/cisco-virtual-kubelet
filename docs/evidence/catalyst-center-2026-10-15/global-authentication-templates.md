# Global authentication-template preservation and recovery

The pinned NetAsCode 0.5.0 module declares
`catalystcenter_update_authentication_profile` for global templates and fabric
overrides. This qualification covers a global Low Impact template only. It uses
the existing REST transport, configuration executor and task journal; no ND,
direct-device, gNOI or SWIM path was changed.

## Implementation and fixtures

The native ACL description is now preserved during updates. Source description
changes remain ignored, matching the pinned Terraform resource's
`ignore_changes`. ACL contract order does not create drift; duplicate ports and
DHCP contracts with TCP/TCP_UDP are refused before submission. The API defines
unique ports and reserves TCP/TCP_UDP for DOMAIN. See the primary
[update API contract](https://developer.cisco.com/docs/dna-center/update-authentication-profile/).

Planning freezes pointer-backed desired fields and contract slices. The existing
executor reobserves all selected profiles twice before its single PUT attempt,
checking native UUIDs, fabric scope, managed fields and preserved descriptions.
Unrelated profile changes remain allowed. A consumed plan cannot be reused,
including after a lost acknowledgement. Reconstructed verification reads only
the acknowledged selection twice and requires the persisted target UUID.

Race fixtures cover description changes, first/later identity or value changes,
replacement during observation, reordered contracts, caller mutation, unrelated
profiles, lost acknowledgement and one-use dispatch. Serialized recovery covers
a missing future selection, replacement or drift on the second read, read
failure and malformed operation scope/action. These fixtures use local HTTP and
simulated persistence; they are not installed-cluster evidence.

The final affected authentication, serialized-recovery and apply-example race
suite passed in **2.740 seconds**. Strict documentation compilation, whitespace
checks and the exact **93-resource-type** pinned-module ledger audit passed.

## Native failure and bounded restoration

The first native run accepted task `01a1403b-20e6-7f5a-b672-fc893b3c86a3`
for global Low Impact UUID `0ddb440b-e867-409c-9902-bc7a8c2ceec8` and reached
current-generation InSync. The subsequent complete raw-response comparison
failed. That test version retained its starting baseline only in memory and did
not log changed field paths. The unexpected difference therefore remains
unidentified; full equivalence to that first baseline cannot be claimed.

After verifying the original task's success, a separate scoped operation
`01a14040-cd93-79b8-84d9-b067eb66a0b6` restored the known original Wake-on-LAN
value, `false`. Its task tree and readback passed, and its own saved baseline
comparison found no further changes beyond Wake-on-LAN. This was a new
correlated restoration, not a replay or journal deletion. See
`global-authentication-initial-native.log` and
`global-authentication-restore-native.log`.

The live test now saves each raw baseline privately before writing and logs only
unexpected field paths. Baselines may contain configuration metadata and are
excluded from the repository; credentials and tokens are never recorded.

## Successful repeated cycle

A fresh run checked zero fabric sites and zero fabric zones before each PUT,
then exercised the public reconciler with simulated Kubernetes persistence:

1. Toggle only Low Impact Wake-on-LAN from false to true, retaining the native
   description despite a deliberately different source description.
2. Wait for the correlated task and current-generation InSync; compare all
   three raw global profiles with the saved baseline plus only that boolean.
3. Rebuild the reconciler twice and verify no additional write or journal entry.
4. Restore false through a new source generation and task, repeat verification
   and no-op checks, then require exact equivalence to this run's full baseline.

Both phases passed. Tasks were `01a14045-4af7-7f48-95bd-9cbd62bd272f` and
`01a14045-67e8-7963-a771-37ceda13eebe`. The test took **16.81 seconds**
(package **18.841 seconds**). See `global-authentication-qualified-native.log`.
No fabric configuration, device operation or controller login policy was changed.
The successful comparison establishes restoration to the second run's baseline;
it does not reconstruct the first run's lost baseline.

## Remaining scope

Fabric overrides, Closed Authentication fabric BPDU settings, wider field
combinations, installed actors/API-server recovery, deletion/reset ownership and
real fabric/device behavior remain pending. The first raw discrepancy remains a
qualification limitation. This evidence does not establish complete resource or
NetAsCode parity.
