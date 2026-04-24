# Copyright © 2026 Cisco Systems Inc.
# Licensed under the Apache License, Version 2.0.
#
# Family-namespace rules: enforce that managedFamilies references
# only families CVK actually knows about, and warn on dependency
# inversions ("you manage interface_ethernet but not vrf, and the
# interface uses a vrf — declare the vrf family too").
package main

# Closed set of families CVK currently writes. Mirrors
# schema/families.yaml (Phase-1 + Phase-2 + Phase-3).
known_families := {
    "system", "vlan", "vrf",
    "interface_ethernet", "interface_loopback",
    "interface_virtual_port_group", "interface_switchport",
    "dhcp",
    "access_list_extended", "access_list_standard",
    "aaa", "banner", "bgp", "cdp", "eigrp",
    "line", "lldp", "logging", "ntp", "ospf",
    "prefix_list", "route_map", "snmp_server",
    "static_route",
    "class_map", "policy_map",
    "crypto_isakmp", "crypto_ipsec",
}

# Reject managedFamilies entries that aren't in the closed set —
# typo'd family names render as silent no-ops on the device.
deny[msg] {
    is_iosxeconfig(input)
    some i
    fam := input.spec.managedFamilies[i]
    not known_families[fam]
    msg := sprintf("managedFamilies[%v]=%q: unknown family; check schema/families.yaml", [i, fam])
}

# A CR that manages interfaces in a VRF context but doesn't declare
# the vrf family will write the interface before the vrf exists on
# the device, producing a transient ApplyError until the next
# reconcile. Warn the operator.
warn[msg] {
    is_iosxeconfig(input)
    interfaces_use_vrf(input.spec.source.inline)
    not contains_family(input.spec.managedFamilies, "vrf")
    msg := "interface(s) reference a vrf but vrf is not in managedFamilies — declare it to avoid bring-up race"
}

contains_family(arr, fam) {
    some i
    arr[i] == fam
}

interfaces_use_vrf(intent) {
    some i
    intent.interface_ethernet.interfaces[i].vrf_forwarding
}
interfaces_use_vrf(intent) {
    some i
    intent.interface_loopback.interfaces[i].vrf_forwarding
}
