# Management-address candidate planning

Pinned NetAsCode 0.5.0 `cc_device_provision.tf` selects
`catalystcenter_update_device_management_address` when a non-AP inventory entry
has a requested `device_ip` not currently in controller inventory and its
name/FQDN resolves to an existing device. The requested address is therefore a
new value, not an additional current-address selector. The existing generic
CVK selector resolver is preserved for other operations.

The new read-only planner resolves the native UUID by name/FQDN and corroborates
serial identity using the existing resolver. It validates the old and requested
literal unicast addresses, checks whether another native device owns the new
address, refuses AP/missing-serial/ambiguous/malformed identities and normalizes
equivalent IPv6 values for no-op comparison. It does not submit an API request,
configure a device interface or request physical mutation authority.

Unit cases cover a changed address without using it as a current selector,
unchanged/equivalent IPv6 no-ops, missing/conflicting selectors and serials,
AP metadata/legacy platform refusal, address collisions, duplicate native IDs,
invalid old/new addresses, multicast, loopback, mapped IPv6 and scoped addresses.

Read-only qualification against the lab passed for all three switches: native
UUID/serial/baseline identity was preserved for unchanged-address no-ops and
local synthetic changed-address requests. No synthetic address left the planner
and no API/device mutation was made. Live test 0.58s, package 1.280s; see
[retained output](management-address-plan-live.log).

The [Cisco API contract](https://developer.cisco.com/docs/catalyst-center/2-3-7-6/update-device-management-address/)
defines a task-returning management-address PUT with `newIP`. That mutation is
not implemented or exposed here. Further work must bind managed physical
identity and old/new endpoint reachability to the existing handoff/lease
protocol, retain the native task and baseline for recovery, reconcile the CVK
device/provider address relationship, verify native convergence and qualify an
isolated live transition. Controller inventory planning alone is not endpoint
reachability or upgrade admission proof. The resource remains in progress.
