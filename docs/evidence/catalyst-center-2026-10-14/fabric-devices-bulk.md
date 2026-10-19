# Bulk fabric-device native stage and physical footprint

The previously pending bulk fabric-device resource now has a private native
planner and retained readback. The
[pinned provider 0.6.1 implementation](https://raw.githubusercontent.com/CiscoDevNet/terraform-provider-catalystcenter/v0.6.1/internal/provider/resource_catalystcenter_fabric_devices.go)
uses ten-member create batches; this stage adopts that bound without claiming
an independently qualified appliance maximum.

## Reused dependencies and original selection

The existing single-device collector resolves each incoming selector, placement,
assignment, wired provisioning, inventory role, fabric/zone and parent membership.
Bulk observation adds all incoming native IDs to each complete physical
footprint, including existing fabric peers and zone-parent peers. Every member
must resolve to the same native fabric and identical complete physical footprint.
Duplicate targets, physical aliases and mismatching existing roles are refused.

Singular callers still use the original target-only recovery behavior. The bulk
collector excludes only the requested incoming memberships from its recovery
baseline, so adding another requested member does not look like an unrelated
peer mutation. Original pre-existing incoming row UUIDs are retained separately
in the selected-mask resource digest and replacements invalidate readback.

The original UUID ordering and selected mask stay pinned across partial creation.
The resource digest binds canonical role/border configuration, native dependencies
and baseline identity. Source roles/border values are captured from canonical
native snapshots before constructing the closure. One atomic-use closure repeats
full observations around an external all-footprint guard, then sends one array
through the existing task transport. Denial, drift, success and uncertain receipt
never permit retry. Readback requires an acknowledged Verifying operation and
checks every original member; partial creation remains incomplete.

## Admission boundary

The resource family is reserved for group execution even when its version or
identity is malformed/pruned. Ordinary logical preparation cannot accept the
native write identity without a complete group identity. Only strict create
resources with canonical count/mask/digest pass group action classification.
The private callback does not create claims or grant mutation authority.

Public source grouping, managed member binding, real all-member admission,
one-use shared dispatch permit, durable task/receipt/recovery integration and
device convergence remain required before public execution can be enabled.
No physical lease, claim, manager settlement or completion marker is written by
this native stage.

## Verification

Sixteen native HTTPS cases cover create, zone-parent membership, existing/no-op,
partial creation, lost acknowledgement, missing/denied guard, duplicate target,
physical alias, peer/target identity races, replaced existing members, wrong-role
readback and immutable captured roles. The guard receives both incoming devices
plus an existing peer. Group classification cases additionally reject logical
preparation and malformed/future-version resources.

Initial native tests passed in 2.409s; singular dependency/recovery checks passed
in 2.246s. Affected Catalyst Center checks passed in 22.636s before the final
family-prefix/alias refinements. That filtered shared-configuration invocation
selected no tests and is not shared lifecycle qualification; the complete suites
are recorded separately. See [native cases](fabric-devices-bulk-tests.log) and
[affected checks](fabric-devices-bulk-regression.log).
Complete final race suites passed with the family-prefix/alias refinements:
Catalyst Center 265.974s and shared configuration 30.849s. See
[full regression](fabric-devices-bulk-full-regression.log). Strict documentation
build and `git diff --check` passed.

## Lab and remaining scope

The authenticated read-only native fabric prerequisite check is repeated. The
lab currently has no fabric site, so accepted fabric-device execution remains
unqualified. No fabric or device role is created to manufacture a passing test.
Native metadata inspection cannot replace installed actor, physical lease,
acknowledged mutation, source-bound device convergence or settlement evidence.
Border/mixed-role batches, broader zone transitions, source chunk/group routing,
installed workers/API-server, update/delete/adoption and accepted live recovery
remain pending. Resource and full-section completion flags stay false.
The repeated native prerequisite check found zero fabric sites and explicitly
skipped accepted execution (0.32s; package 1.134s). See
[fabric availability](fabric-devices-bulk-live-prerequisites.log) and
[sanitized access](fabric-devices-bulk-live-access.log). This is shared fabric
inventory inspection, not live fabric-device mutation qualification.
