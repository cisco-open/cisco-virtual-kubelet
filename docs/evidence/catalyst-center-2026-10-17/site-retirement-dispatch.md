# Guarded staged site-retirement submission

The staged resource writer now connects dependency/ownership binding, exact
submission, original-task observation and atomic retirement receipts through the
existing configuration journal. It reuses `configurationWrite`, the one-use
preflight/no-dispatch capability and the existing site mutator/transport. Public
sites Delete/prune admission remains closed.

## Dispatch contract

Planning requires the existing private retirement binding and sufficient action
receipt capacity. Unknown operation/task IDs and submission time reserve their
full schema-sized representation before dispatch. Capacity is checked again
against the durable operation before native preflight. The one-use guard then
rechecks source, owner, key, history, native baseline and dependency evidence.

The shared site mutator uses one common endpoint mapping for areas, buildings and
floors. Delete requires an empty body, canonical target UUID, distinct valid
parent, matching action/method and the exact fixed endpoint. Existing Create and
Update must also match their POST/PUT methods. There is no appliance-provided URL
or separate HTTP/session implementation.

A guard-specific proof can release only an operation demonstrably refused before
submission. Source/key/native/consumer changes and late capacity exhaustion are
qualified through this path. Changed ownership retains the operation fence.
API authentication/server errors or missing acknowledgements never acquire a
no-dispatch proof: the operation remains taskless Unknown and cannot resubmit or
publish a retirement receipt.

An exact unsubmitted Prepared operation can reconstruct the writer after restart.
The config/source/controller frame, target, parent and recomputed selector/native
fingerprint must still match. It uses the common original-ID preparation path.
Changed native state cannot acquire a replacement preparation. Submitting,
Accepted and Unknown are never admitted as new submissions.

## Verification

The [final local race run](site-retirement-dispatch-regression.log) passes
**35** families / **733** subcases in **39.269 s**. The new writer family covers
38 inline/ConfigMap cases, including single submission, latest lineage, 128-byte
task IDs, acknowledged Unknown recovery, pending/failed tasks, lost receipts,
401/500 uncertainty, preflight refusals, capacity and preparation resumption.
Three-kind endpoint validation includes wrong endpoint, body, target, parent,
kind and method, including a POST-with-body Delete descriptor.

The [real temporary Kubernetes API run](site-retirement-dispatch-api.log) passes
**73** scenarios across binding, submission, recovery and receipt persistence in
**43.947 s**. The dispatch family contributes 19 scenarios. The
[existing Create no-replay and site-verification gate](site-retirement-existing-transport.log)
passes 2 families / 11 subcases in **2.414 s**. The
[source and terminal-gate audit](site-retirement-dispatch-validation.json) fixes
the exact qualified boundary.

This is an area lifecycle driven through staged components against native HTTP
fixtures and private API servers. It is not public compiler admission or an
installed/native Catalyst Center retirement result. Successful fixtures settle
the original task exactly once and retain the creator/latest rows, bounded
retirement receipt and section owner. They do not recreate a missing site or
coordinate parent retirement.

## Remaining work

Public policy/compiler routing and complete operational/settings consumer
ownership checks, bounded lineage archival/recreation, parent/child retirement
coordination, building/floor full lifecycle, source/config deletion and installed
end-to-end lab qualification remain open. The shared foundations, ND, direct
device and SWIM provider interfaces are unchanged. Full site lifecycle and
93-resource parity remain incomplete.


The following [recreation history protocol stage](site-recreation-protocol.md)
now validates complete archived cohorts and successor chains. This is structural
qualification; it does not enable source reintroduction or promote the staged
retirement writer to a public/installed complete lifecycle.
