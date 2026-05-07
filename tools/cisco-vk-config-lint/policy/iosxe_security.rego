# Copyright © 2026 Cisco Systems Inc.
# Licensed under the Apache License, Version 2.0.
#
# Cisco-IOS-XE-specific security guard rails. These walk the
# netascode-shaped intent under input.spec.source.inline and emit
# warn/deny when the operator's authoring choices conflict with
# common compliance baselines (FedRAMP, CIS Cisco IOS-XE benchmark,
# NIST 800-171). None of these gate the driver — they're a CI hint.
package main

# privilege-15 user without a secret is a hard fail. IOS-XE accepts
# this shape but it's an enable-secret bypass: anyone who knows the
# username gets a level-15 shell with no password.
deny[msg] {
    is_iosxeconfig(input)
    some u
    user := input.spec.source.inline.system.usernames[u]
    user.privilege == 15
    not user.secret
    msg := sprintf("system.usernames[%v]: privilege 15 without 'secret' is an enable-shell bypass", [u])
}

# enable password (cleartext) is deprecated in favour of enable
# secret. Authors should migrate; flag both legacy spellings.
warn[msg] {
    is_iosxeconfig(input)
    input.spec.source.inline.system.enable_password
    msg := "system.enable_password is the legacy cleartext form; use system.enable_secret instead"
}

# SNMP write community without explicit ACL is a fleet-wide risk.
# A typo'd community gives anyone routable to the management plane
# the ability to mutate the device. Require an ACL to be referenced
# from at least one community when any community has access=rw.
deny[msg] {
    is_iosxeconfig(input)
    some i
    cmty := input.spec.source.inline.snmp_server.communities[i]
    cmty.access == "rw"
    not cmty.access_list_name
    msg := sprintf("snmp_server.communities[%v]: rw access without access_list_name — restrict source IPs via an ACL", [i])
}

# AAA must be enabled when any TACACS+/RADIUS server is declared.
# The opposite shape — declared servers but no aaa.new-model — is a
# common bring-up bug that leaves devices using the local-only
# fallback even when the operator thought the central servers were
# in use.
warn[msg] {
    is_iosxeconfig(input)
    has_central_aaa(input.spec.source.inline)
    not input.spec.source.inline.aaa.new_model
    msg := "aaa: TACACS+/RADIUS servers declared but aaa.new_model is false — central AAA never reached"
}

has_central_aaa(intent) {
    count(intent.aaa.tacacs_servers) > 0
}
has_central_aaa(intent) {
    count(intent.aaa.radius_servers) > 0
}
