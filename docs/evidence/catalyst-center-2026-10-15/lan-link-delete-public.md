# Bounded public LAN DELETE and deleted-state ownership

The Catalyst Center adapter on `pr/johalley/catc-support` now exposes the bounded
DELETE lifecycle through the existing NetworkControllerConfig reconciliation
path. This is fixture-qualified public execution, not full pinned NetAsCode LAN
parity or hardware qualification. The descriptor advertises `lan-link-delete`
through the existing worker rollout contract.

## Implemented behavior

A source may contain ADD_LINK declarations or DELETE_LINK declarations. Complete
source validation and action selection precede execution; mixed actions and
unsupported discovery input are refused rather than partially applied. Both
actions use the same public preparation/admission/dispatch implementation. DELETE
still requires retained independently verified ADD ownership of the exact
interfaces and retirement of the original ADD reservations.

Accepted/Unknown DELETE work recovers through the same retained-operation route
as ADD. Task/native evidence enters Verifying, and existing DeviceOperation
workers provide the independently bound chassis/interface deconfiguration proof.
The existing atomic group writer publishes the receipt; actual reservation
workers release leases, and both manager settlements precede journal retirement.
Unacknowledged submission is never replayed. Sources, native/managed identities,
tasks or authority which no longer match retain the original fence.

For an unchanged DELETE declaration, historical independent completion plus
current exact native result equality establishes idempotency. The two physical
interfaces must still be present and unaddressed. Missing LLDP or a down link
alone does not negate ownership. Current pool metadata/capacity is not required
for a deleted result. Neither an unowned empty interface nor an obsolete receipt
can be adopted as completed DELETE. Idempotency does not attest fresh device CLI.

Both explicit-pool and omitted-pool declarations complete the public cycle in
fixtures. Omitted pool is resolved from verified ADD ownership for admission and
is not inserted into the native request. The original raw declaration remains
bound during execution and deleted-state observation.

A subsequent ADD of the same logical link checks the retained DELETE result and
requires retirement of every original DELETE reservation before normal empty-port,
origin, pool and managed-group preparation can resume. Tests prove fresh ADD
preparation and refusal of changed state or retained members; they do not qualify
an entire repeated native ADD/DELETE/ADD cycle.

Use the same NetworkControllerConfig and link name for the action transition.
The repository example is `examples/configs/catalyst-center/lan-link-delete.yaml`;
replace its documentation addresses and interfaces with owned lab values.

## Fixture validation

[Public ownership/admission tests](lan-link-delete-public-owned.log) cover
explicit/omitted-pool public cycles, deleted-state InSync without new native
requests or capacity reads, unavailable/changed pool metadata, missing/changed
ports and Nodes, stale sources, foreign/unowned state, down/absent adjacency and
re-add preparation only after reservation retirement. The public cycle uses real
shared reservation-worker lease release and real manager-settlement publication
against fake Kubernetes, with explicit fake device output and maintenance-manager
cleanup. No installed actor/hardware attestation is implied.

[Public restart recovery tests](lan-link-delete-public-recovery.log) cover
Accepted/Unknown progress, running/failed/future tasks, changed sources, replaced
ports/chassis, revoked grants, missing group identity and lost acknowledgement.
Repeated reconciliation never replaces or replays the admitted native request;
unqualified outcomes retain both claims. Pool changes after accepted cleanup do
not invalidate the immutable physical/declaration recovery binding.

See [full affected race regressions](lan-link-delete-public-regression.log),
[runtime/RBAC contracts](lan-link-delete-public-runtime.log),
[fresh native prerequisites](lan-link-delete-public-live.log), and
[CVK API readiness](lan-link-delete-public-cluster.log).

## Remaining scope and limitations

- Mixed ADD/DELETE sources, broader model composition and automatic stale DELETE
  preparation cancellation/re-planning remain pending.
- The v1 DELETE receipt binds the original raw pool declaration. Changing an
  already completed declaration between explicit and omitted pool remains fenced;
  semantic declaration migration has not been qualified.
- Current ADD receipts expose opaque port/pool commitments. A later ADD on the same
  device pair, in either orientation, might supersede an old DELETE under another
  pool. Ambiguous same-pair history is conservatively fenced; exact port-specific
  migration/composition remains pending. It is not resolved from empty inventory.
- An entire repeated ADD/DELETE/ADD native cycle, legacy/adoption
  paths, installed actors/leases and actual post-DELETE CLI output remain
  unqualified. Source-version or immutable-scope recovery is not inferred.
- LAN discovery remains absent in the lab, and the CVK Kubernetes API still
  returns EOF. Native topology/session reads do not qualify accepted execution.
  No LAN device/link/session/pool mutation was sent to the appliance by this work.

These gaps, the remaining LAN resources and the other incomplete resource rows
in the parity ledger remain part of the active full-model objective.

The focused public ownership/admission suite passed in **28.852s**, public restart
recovery in **13.778s**, and full affected race regressions in **445.962s** for
Catalyst Center (other unchanged affected packages passed from cache). Runtime
manager/connector and RBAC checks passed in **3.808s**; fresh read-only native
prerequisites passed in **5.124s**. Strict documentation and whitespace checks pass.
