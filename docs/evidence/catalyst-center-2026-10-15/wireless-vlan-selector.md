# Wireless profile VLAN-group selector mapping

Pinned NetAsCode module: Catalyst Center 0.5.0, commit
`a435555fe828f03cf3311d52fe1ab6572a247916`.
The [pinned module](https://github.com/netascode/terraform-catalystcenter-nac-catalystcenter/blob/a435555fe828f03cf3311d52fe1ab6572a247916/cc_wireless.tf)
forwards vlan_group_name as a name, and the
[native profile API](https://developer.cisco.com/docs/catalyst-center/create-wireless-profile/)
declares vlanGroupName as a string. On that evidence the adapter treats it as
a name-valued design selector, rather than fabricating a globally resolved UUID.
This does not establish that the named group exists or is realized on a WLC.

## Implementation

The previous collector refused all used or retained VLAN-group references while
waiting for an unqualified global inventory contract. That unconditional gate
and unused UUID map were removed. The existing strict parser, profile payload,
metadata executor and common journal now carry the declared selector through
create/update and retained-field handling. Effective selectors are name-validated;
operation dependency identity includes the selector per SSID. A changed selector
changes the operation binding, and a change after planning prevents submission.
Global/SSID identity, native profile UUID, source/owner, unknown-field and other
reference guards remain unchanged. Interface/group mutual exclusion and fabric
suppression keep the pinned module behavior. No group object ID is invented.

The controller API validates the referenced design definition. A rejected
mutation follows the existing uncertain-operation journal contract: it does not
publish success or replay the request. This implementation does not configure
VLAN groups, prove their membership, replace provisioning admission, or assert
successful device realization. Authoritative design-group lookup remains a
qualification gap; an existing group fixture is needed for a positive lab run.

## Native evidence and limits

The [initial isolated probe](wireless-vlan-selector-native-rejected.log) created
one disabled OPEN SSID and attempted a new unassigned profile with an absent
random VLAN-group name. The appliance returned HTTP 406. A supervised diagnostic
first checked profile absence and the exact retained disabled SSID, then observed
[NCND01106](wireless-vlan-selector-validation.log): the configured interface/group
name is not present. This is a missing dependency rejection, not a passing
selector create/update test. It does not justify inventing an inventory identity
or advertising full VLAN-group qualification.

The [retained SSID cleanup](wireless-vlan-selector-cleanup.log) checked that no
profile existed or referenced it, removed its exact ID and verified the public
SSID/profile baselines. No site assignment, VLAN-group creation, provisioning or
device operation was submitted. The temporary diagnostic was removed from the
repository and production error-body redaction remains unchanged.

The opt-in live harness now requires CATC_WIRELESS_VLAN_SELECTOR_NAME together
with CATC_WIRELESS_VLAN_SELECTOR=isolated-definitions. An optional
CATC_WIRELESS_VLAN_SELECTOR_UPDATE_NAME can select a second existing group;
otherwise the update retains the original selector and changes WLAN metadata.
The [missing-fixture check](wireless-vlan-selector-prerequisite.log) explicitly
SKIPped before creating fixtures. A known design-group name was requested from
the user; positive native create/update/readback remains pending.

## Verification

- [Focused selector/profile race fixtures](wireless-vlan-selector-focused.log):
  PASS, 4.696s. These cover literal binding, stale intent, create/update/readback,
  retained mappings and public journal restart using HTTP fixtures.
- [Wireless/profile plus SWIM race regression](wireless-vlan-selector-regression.log):
  PASS, 11.162s. Public missing-reference rejection stays Unknown across restarts
  with the same operation, no task ID and exactly one native mutation attempt.
- [Complete ND/shared foundation packages](wireless-vlan-selector-foundations.log):
  PASS (cached).
- [Manager/connector/RBAC checks](wireless-vlan-selector-manager.log): PASS, 3.803s.

These results implement name mapping and exercise failure/recovery behavior.
They do not close positive native group-reference, definition lookup, WLC
realization, installed actors or full network-profile parity gates.

[Strict documentation build](wireless-vlan-selector-docs.log) passed in **4.82s**;
whitespace checks passed.
