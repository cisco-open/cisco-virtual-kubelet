# Wired bulk grouping and whole-sequence preflight

This extends the [native/managed batch stage](wired-bulk.md) on the pinned
NetAsCode 0.5.0/provider 0.6.1 contracts. Grouping operates on parsed inventory
intent, explicit PROVISION/REPROVISION state and a supplied resolved declared-site
set. Options are private typed inputs; no new public annotation or model field
was exposed before its lifecycle is ready.

## Grouping

Default grouping is by device site. An explicit bulk root filters to that site
and descendants, takes precedence over managed-site grouping, must be in the
supplied declared-site set, and follows the pinned bulk option's five-level
limit. Managed scopes support deeper declared hierarchy and reject overlapping
roots. Site matching uses hierarchy separators so a Lab scope cannot capture
Lab2. Caller-supplied site resolution/scoped ownership remains a public-import
obligation, not authority derived from this helper.

All declarations and state references validate before returning any batch.
Explicit scope filtering is the only selection exclusion; unsupported wireless/
embedded candidates remain refused. Create and re-provision members use separate
homogeneous batches. Source selectors are sorted for stable chunk boundaries,
then split into at most ten entries; the native planner independently sorts each
batch by actual UUID. Root and method ordering are deterministic. No source or
option list is sorted in place.

## Whole-sequence guard

Every selected batch completes existing native and managed identity/alias/Node
preflight before any submission closure is returned. Native UUID or physical
identity reuse across chunks is refused, including aliases with different source
names. Native/managed observations for the complete sequence are repeated after
planning and around the selected batch's supplied authority guard. Thus a later
unmanaged, replaced or changed batch cannot leave an earlier write exposed.

The returned candidates are an ordered plan, not an automatic lifecycle loop.
A native change invalidates old whole-sequence evidence: callers must finish the
current journal/reservation lifecycle and replan before another batch. A fixture
proves the stale second closure is refused after first-batch creation, then a
fresh plan preserves the converged first batch and can plan the second. PUT
completion must eventually use durable owned receipts; native presence cannot
suppress re-provision for the current model generation.

## Catalog reuse

A bulk observation now reads the site/inventory catalogs once, then reuses the
existing single-device target and record/assignment validators for each member.
The single-device function preserves its original inventory → target validation
→ site → assignment/record order. Catalogs are local to one observation and
never shared across observations, guards or submission. A request-count fixture
proves one catalog read per observation and a new read on the next observation.
Repeated full native/managed observations around authority are preserved.

## Verification

Grouping fixtures include 23 devices, 10/10/3 root chunks with separate methods,
source reorder stability, exact root filtering, managed and deeper scopes,
explicit-root precedence, overlapping roots, unknown/deep bulk roots, unknown
sites, dangling/bad state, duplicate declarations and unsupported wireless input.
Sequence fixtures cover ordered fresh planning, missing later declarations,
cross-chunk aliases, guard-time later native/Node changes and missing authority.
No partial plan or unsafe earlier request is returned after failure.

After catalog reuse, affected wired/journal/group/precondition/template race
regression passed: Catalyst Center 6.579s, shared configuration 2.135s. Final
bulk-only tests passed 3.602s. See
[affected regression](wired-bulk-catalog-regression.log),
[final checks](wired-bulk-sequence-final.log) and
[sequence/identity checks](wired-bulk-sequence.log).

## Live qualification

Actual Catalyst Center inventory, sites, assignments and provisioned-record APIs
qualified default, Global bulk-root and Global managed-site grouping for the
three lab devices. Kubernetes managed objects were simulated from the native
baseline; their identities are not installed actor evidence. Each grouping mode
passed all-batch planning with no authority callback during planning, then the
selected closure deliberately refused dispatch and could not replay. Final
native identity and provisioned-record readback remained unchanged. No native
provisioning POST/PUT or lease/claim occurred.

The optimized sequence test passed in 24.78s, alongside the native-only test in
3.49s (package 28.936s): [live checks](wired-bulk-sequence-live.log),
[sanitized access](wired-bulk-sequence-live-access.log). Before catalog reuse the
same sequence test took 46.16s: [earlier run](wired-bulk-sequence-before-catalog-live.log).
These are individual qualification timings, not a production scale benchmark.
Real lab input produced one three-member batch per mode; multi-chunk and mixed
methods remain fixture-only. The lab still has no fabric sites.

## Still pending

Public option/import/schema routing and declared-site ownership, all-component
composition, shared group preparation/claim/dispatch and retained original-batch
recovery, owned receipt selection/atomic publication, completion/settlement/
retirement, clean-up deletion semantics, wireless/embedded composition, rendered
device convergence, larger-scale qualification, installed actors and an accepted
live batch remain pending. Capability flags stay incomplete; this is private
plan/preflight qualification, not a merge-ready bulk feature.
