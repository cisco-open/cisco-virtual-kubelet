# Owned credential dependencies: real schema and native metadata composition

This qualification extends the [owned credential prerequisite bridge](wired-bulk-owned-credentials.md).
No production credential writer, transport, CRD or ownership protocol was added.
The existing public metadata reconciler and the existing bulk dependency observer
are exercised with real schema/status storage and the Catalyst Center appliance.

## Actual API-server contract

The [private API test](wired-bulk-owned-credentials-api.log) passed three cases:
all six versioned credential definitions, NOAUTH metadata ownership, and a
ConfigMap-backed owner source. It uses the actual NetworkController and
NetworkControllerConfig CRDs on a fresh private API server, not kubeconfig or the
installed cluster. The binary reports Kubernetes v1.35.0; this qualifies CRD behavior on that version, not the installed cluster runtime/version. Native responses and task/worker evidence in this test are
synthetic local HTTPS fixtures.

The API assigns real owner UIDs and generations, preserves all canonical
UUID/version or metadata receipt bindings, and rejects stale status writers,
invalid receipt versions/generations, invalid authorization aliases and attempts
to mutate immutable scope. A valid mutable owner-spec change advances generation
and invalidates unreconciled prerequisite evidence. ConfigMap resourceVersion
changes alter the retained dependency identity even when credential values are
unchanged. Deleting/recreating an otherwise matching owner gives a new UID and
cannot inherit the old ownership receipt. No Kubernetes Secret objects or
projected values are needed by the consumer.

## Native public metadata producer and dependency consumer

The [native metadata run](wired-bulk-owned-metadata-native.log) passed with the
existing fake Kubernetes actors. It creates one generated, unassigned
NOAUTHNOPRIV credential through the actual public CVK reconciler, persists the
native task/UUID/intent receipt, performs an owned metadata update on the same
UUID, and checks restart no-op behavior. Bulk consumption returns a stable
identity for each unchanged revision and a different identity after the update.
Removing the receipt in the test harness is refused; explicitly restoring the
original receipt recovers the exact original dependency identity. This is not
an automatic reconstruction or adoption capability.

A read-only transport wrapper rejects any native mutation during dependency
checks. The main producer trace permits only the exact generated metadata
payloads and exact fixture UUID cleanup. There are no existing-site or device
mutations. Cleanup verifies the generated credential is absent and restores the
complete typed non-secret credential baseline.

## Combined real API and native appliance run

The [combined qualification](wired-bulk-owned-metadata-api-native.log) passed in
37.14 seconds. The same CVK public reconciler writes to a real private API server
while executing against Catalyst Center; its actual serialized task/UUID/metadata
receipts then feed the private bulk dependency observer. Create, same-UUID update,
restart no replay, explicit missing-receipt refusal/restoration, changed-revision
identity and exact native cleanup all pass in this one process.

The reconciler is invoked by the qualification process. This does not establish
installed worker startup, RBAC/actor separation, device-worker execution or
accepted physical bulk provisioning. The credential is unassigned. The source's
inventory row is a dependency-context selector and never causes a physical device
lookup, claim or provisioning request.

The [initial combined attempt](wired-bulk-owned-metadata-api-initial.log) did not
observe the config and made no native credential write; a separate read verified
no generated fixture remained. Its harness rewrote controller spec after pinning
worker identity. The revised harness completes controller configuration before
pinning, removes that post-pin write and checks actor/target identity and observed
config generation immediately. No production stale-worker guard was weakened.

Full model parity remains incomplete. Installed versioned owner/consumer recovery,
complete rendered device proof, accepted native bulk mutation, public bulk routing
and broader resource execution/prune/handoff qualification remain pending. The
installed cluster API-only recovery still awaits the separately requested approval.
All resource and whole-section parity flags remain partial.

Subsequent [combined versioned qualification](wired-bulk-owned-versioned-qualification.md) now exercises the public producer, real API receipts and native dependency consumer together for all six types. The earlier separate-fixture qualification limits remain historical; installed actors, physical device convergence and public bulk routing remain pending.
