# Copyright © 2026 Cisco Systems Inc.
# Licensed under the Apache License, Version 2.0.
#
# Generic shape rules for IOSXEConfig CRs. Conftest-compatible.
package main

# managedFamilies must be declared and non-empty. The CRD already
# enforces this at the API server (kubebuilder MinItems=1), but
# a CR rendered offline (kustomize build, helm template) won't have
# been API-server-validated yet — admission rules catch it first.
deny[msg] {
    is_iosxeconfig(input)
    not input.spec.managedFamilies
    msg := "iosxeconfig: spec.managedFamilies is required and must be non-empty"
}

deny[msg] {
    is_iosxeconfig(input)
    count(input.spec.managedFamilies) == 0
    msg := "iosxeconfig: spec.managedFamilies cannot be empty"
}

# A revert policy without explicit pruneOnRelinquish guidance is
# error-prone — a family removed from the CR is left dangling on
# the device. We require the operator to make the choice, even
# if that choice is "false".
warn[msg] {
    is_iosxeconfig(input)
    input.spec.driftPolicy == "revert"
    not has_field(input.spec, "pruneOnRelinquish")
    msg := "iosxeconfig: driftPolicy=revert without explicit spec.pruneOnRelinquish — choose true or false"
}

# Inline secret material in spec.source.inline is a smell: secrets
# belong in spec.secretRefs (Phase 4 / §10.6). The check is shallow
# — it looks for fields named like 'password', 'key', 'secret',
# 'community' anywhere in the inline body — and is intentionally
# noisy to encourage authors to migrate.
warn[msg] {
    is_iosxeconfig(input)
    has_inline_secret(input.spec.source.inline)
    msg := "iosxeconfig: secret-like leaf in spec.source.inline; prefer spec.secretRefs"
}

# Transactional applies under RESTCONF are a no-op (the transport
# has no candidate datastore). Flag it so the operator either flips
# the device's transport to NETCONF or drops the field.
warn[msg] {
    is_iosxeconfig(input)
    input.spec.transactional == true
    msg := "iosxeconfig: spec.transactional=true requires the device's transport to be netconf or gnmi; ignored on restconf"
}

# Helpers
is_iosxeconfig(obj) {
    obj.apiVersion == "config.cisco.vk/v1alpha1"
    obj.kind == "IOSXEConfig"
}

has_field(obj, name) {
    obj[name]
}

has_inline_secret(node) {
    is_object(node)
    some k
    secret_key(k)
    node[k]
}

has_inline_secret(node) {
    is_object(node)
    some k
    has_inline_secret(node[k])
}

has_inline_secret(node) {
    is_array(node)
    some i
    has_inline_secret(node[i])
}

secret_key(k) {
    lower := lower(k)
    contains(lower, "password")
}
secret_key(k) { contains(lower(k), "secret") }
secret_key(k) { contains(lower(k), "community") }
secret_key(k) { contains(lower(k), "psk") }
