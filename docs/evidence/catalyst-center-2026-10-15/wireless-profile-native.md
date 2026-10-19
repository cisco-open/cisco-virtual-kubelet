# Wireless network profile native qualification

The pinned NetAsCode 0.5.0 network profile model is exercised through the existing
`compileNetworkProfileConfiguration` and shared wireless profile executor. Its
Global SSID prerequisite is created first through `compileWirelessConfiguration`.
These are sequential section compiler executions, not a claim that a combined
multi-section Kubernetes manifest was installed or qualified.

## Compatibility gap found and fixed

The first [native run](wireless-profile-initial-failure.log) accepted a disabled
OPEN SSID and a logical wireless profile, then failed profile readback with the
existing unknown-field/ownership guard. Inspection of the exact created UUID
identified `ssidDetails[].policyProfileName` as the only unmapped attribute.

Cisco's [Get Wireless Profiles 2.3.7.9](https://developer.cisco.com/docs/catalyst-center/2-3-7-9/get-wireless-profiles/)
response documents that public string. The pinned NaC profile input does not
select it; `policy_profile_name` belongs to the SSID model. CVK now recognizes
only this typed public attribute and preserves it during full-object profile
updates. Unknown attributes remain excluded and block updates, malformed values
remain errors, and arbitrary profile model attributes remain rejected. No new
transport, controller, Secret, journal or physical reservation implementation was
introduced.

The new regression fixture covers public observation, preservation during payload
assembly and rejection of malformed metadata/unpinned input. The existing native
identity lifecycle fixture now injects the server-generated policy name at create,
checks preservation in PUT and still verifies no-op/replacement/no-replay behavior.

## Retained operation recovery

The [retained recovery](wireless-profile-retained-recovery.log) used the exact
learned profile and SSID UUIDs and original accepted create task/resource digest.
It verified that original operation without replaying creation, updated the WLAN
profile name on the same UUID, and confirmed no-op convergence. It then deleted
the unassigned profile before its disabled SSID, followed by public baseline
restoration. That supervised native recovery test is not Kubernetes worker or
journal-restart qualification.

## Isolation and scope

The durable opt-in live test uses random absent names, Create-only initial plans,
learned UUID-bound updates and cleanup, disabled OPEN/5GHz SSID metadata, and no
site/tag declarations. It rechecks unrelated SSID/profile public baselines before
every mutation. Unknown profile metadata, any assignment/tag on the test profile,
foreign references to its SSID, replaced identities or uncertain tasks retain the
fixture for investigation. Cleanup checks unreferenced profile scope and removes
the exact profile UUID before the exact SSID UUID. No wireless provisioning,
physical AP action or switch upgrade is submitted.

Broader feature-template/AP-zone/FlexConnect/fabric/interface/VLAN-group cases,
site/tag assignment and removal ownership, adoption/pruning, installed actors and
actual WLC/AP behavior remain open in the
[parity ledger](../../controllers/catalyst-center-resource-parity.md).

## Fresh qualification result

The [fresh native run](wireless-profile-live.log) passed in **14.495s** (test body
**12.63s**) from absent random names. The test verified each accepted task and
native result, updated the same profile UUID, proved no-op through fresh section
compilation and restored both public inventories. The
[manager/worker regression](wireless-profile-runtime.log) passed in **4.056s**.

The [CVK readiness probe](wireless-profile-cluster.log) still returned EOF.
Strict documentation build passed in **4.32s**; the revised example parses as YAML
and contains no site/tag assignment declarations. `git diff --check` passed.
Evidence was checked to exclude the account credentials.

The [full affected race suite](wireless-profile-regression.log) passed, Catalyst
Center **521.802s**, with nacmodel, controller configuration, reservation,
Nexus Dashboard, controller and managed protocol packages passing from cache.
That run preceded the separately tested power-request constraint changes below;
it is not represented as a full-suite run of those later changes.

The later power validation change also passed final all-wireless/configuration
race fixtures (**9.411s**) and manager/worker checks (**4.091s**); see the
[final power qualification](power-rules-native.md).
