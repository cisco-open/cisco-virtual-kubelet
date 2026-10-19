# Wired bulk provisioning native and managed stages

The pinned NetAsCode 0.5.0 `catalystcenter_provision_devices` resource is backed
by the pinned provider 0.6.1 root-list resource, which splits requests at ten
entries, uses POST for creation and PUT for re-provisioning, and reserves
clean_up_config for deletion query parameters. Sources:
[provider definition](https://github.com/CiscoDevNet/terraform-provider-catalystcenter/blob/v0.6.1/gen/definitions/provision_devices.yaml),
[provider resource](https://github.com/CiscoDevNet/terraform-provider-catalystcenter/blob/v0.6.1/internal/provider/resource_catalystcenter_provision_devices.go),
[Cisco provision API](https://developer.cisco.com/docs/catalyst-center/2-3-7-5/provision-devices/) and
[Cisco re-provision API](https://developer.cisco.com/docs/catalyst-center/re-provision-devices/).

## Implemented stage

A private native batch planner now reuses single-device native inventory,
assignment, role, reachability, site and provisioned-record checks for every
member before producing a write. It accepts a homogeneous POST or PUT batch of
at most ten members within one grouping root/descendant hierarchy, canonicalizes
ordering by native UUID, and refuses duplicate native or physical identities,
unassigned/unsupported targets, mixed methods and oversized input. Larger and
mixed model grouping/sharding are not yet orchestrated.

The nonsecret resource key retains the original selected-member mask and a
binding digest. PROVISION omits already-converged members from the POST without
losing their identity checks. Existing no-op provisioned IDs and every PUT ID
are bound; newly created IDs are independently read back. Partial creation is
verified against the original mask rather than planning a smaller replacement
request. PUT masks must retain every declared member.

Dispatch closures are one-use even after denial or an ambiguous response.
Complete native snapshots are repeated around an externally supplied exact
all-member authority guard. The response taskId is validated; its URL is not
trusted. Native verification requires complete rooted task-tree evidence plus
all expected member/site records, and refuses child failure/running tasks or
replaced existing/re-provisioned records. This is provisioning-record/native
execution evidence, not rendered device configuration attestation.

The managed wrapper uses the existing repeated native/managed observation,
global alias detection, exact management address, CiscoDevice/Node UID and
physical inventory checks. It returns canonical shared group targets and repeats
binding checks around the supplied guard. It creates no reservations or claims,
publishes no completion and releases no lease. A caller must still supply shared
claim/live-lease and worker authority, including current revision evidence.

The resource prefix is reserved as inventory group work even when malformed,
so pruning or unsupported recovery cannot route it through single-device
dispatch. There is no public compiler/dispatcher activation for this new kind.

## Verification

Native fixture cases cover create, re-provision, no-op, initially converged
members, canonical ordering, guard denial, late serial/record changes, partial
creation, lost response, child failure/running tasks, changed retained IDs,
missing guard, mixed methods, duplicates, outside-root and oversized inputs.
Malformed versions/counts/masks/hashes and dropped PUT members are rejected.
Managed fixtures cover missing declarations, cross-namespace physical aliases,
wrong address/driver, replaced Node and guard-time address/Node changes, with
no partial evidence and no unsafe request. The combined native/managed/group
identity race checks passed: Catalyst Center 4.052s; shared contract 2.133s.
Broader wired/journal/group/precondition/template checks passed: Catalyst Center
6.310s; shared configuration 1.918s. See
[managed checks](wired-bulk-managed.log) and [regression](wired-bulk-regression.log).

Actual Catalyst Center inventory/assignment/provisioned-record APIs supported
planning for all three lab switches. A dispatch guard deliberately refused
before the provisioning request, and the closure could not be replayed. All
provisioning records remained unchanged. This test passed in 4.46s (package
5.359s): [live planning/refusal](wired-bulk-live.log),
[sanitized access](wired-bulk-live-access.log). No native provisioning POST/PUT
or device mutation was performed or qualified. Credentials/token were withheld.

## Remaining scope

Public full-model grouping, ten-member chunk sequencing and mixed POST/PUT
ordering, bulk_site_provisioning/managed-sites parameter validation, all-component
preflight, group journal preparation/dispatch/recovery, claims, atomic receipts,
completion/settlement/retirement, clean_up_config ownership-aware deletion,
embedded/wireless composition, rendered-device verification, installed actors
and an accepted isolated live batch remain pending. The resource row is in
progress, not supported/full parity. Model section capability flags are unchanged.

Final bulk-only race tests, including the canonical all-member PUT-mask rule, passed in 3.443s: [final output](wired-bulk-final.log). Strict documentation and whitespace checks also passed.
