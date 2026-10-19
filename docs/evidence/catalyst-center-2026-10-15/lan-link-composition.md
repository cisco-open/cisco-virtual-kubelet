# LAN link composition and repeated public cycles

On `pr/johalley/catc-support`, a shared whole-model observer now handles both LAN
link actions. The public path supports mixed ADD_LINK/DELETE_LINK on disjoint
physical ports, including distinct actions sharing a NetAsCode name. This is
bounded IPv4 link execution; full pinned model and hardware parity remain unproven.

## Source and execution contract

The pinned NetAsCode 0.5.0 `cc_lan_automation.tf` uses `name#_#action` keys for
links. CVK now accepts repeated names across distinct actions, uses that key when
the simple name is ambiguous, and refuses duplicate name/action keys. Existing
name validation excludes `#`, so the simple/composite source keys cannot collide.
Shared/reversed physical-port conflicts and unsupported discovery/extra fields
remain refused. Typed ADD-only and DELETE-only internal entry points retain their
strict contracts; the public observer validates every supported declaration.

Both actions reuse the same journal/revalidation/permit/native sender and the same
source/controller checks. Every declaration must have complete action-specific
native/managed prerequisites before any operation is prepared. Ready DELETE
intent is selected before ADD, then by name. Effects execute sequentially, with
all-member worker release and manager settlement before the next journal. No
reclaimed pool capacity is assumed during initial whole-model preflight.

Retained ADD and DELETE recovery selects the originally admitted member from the
complete source. It does not accidentally reject another supported action or
require unrelated unfinished ports for that operation's completion. Existing
source-version checks still hold changed intent. ADD stale-preparation
cancellation now rechecks the same complete mixed observation; DELETE stale
preparation still does not automatically re-plan.

## Receipt scope

A newer ADD on the same device pair no longer automatically makes an unrelated
completed DELETE ambiguous if its exact scope is independently recoverable from
the current model. CVK requires a matching logical commitment, the latest retained
independently verified ADD application, positive current native result equality,
and disjoint physical device/port UUIDs. Device-pair equality or a name alone is
insufficient. Duplicate operation identity and malformed or unresolved histories
hold. Missing the declaration needed to interpret an opaque historical ADD still
holds; no port identity is invented or adopted from empty inventory.

Current native equality plus the original independent receipts establishes
idempotency, not fresh device CLI attestation on every reconcile.

## Qualification

[Mixed and existing lifecycle tests](lan-link-compose-focused.log) cover mixed
source preparation and execution, explicit/omitted DELETE pool, shared names
across actions, whole-source refusal for missing ports/unowned effects/bad fields,
and existing ADD/DELETE and cancellation behavior. Mixed public cycles on two
links between the same device pair complete DELETE first, then ADD, independently
verify both effects, settle them and reach InSync without replay. Dropping the
current declaration required to interpret the newer opaque ADD remains fenced.

The [clean-start repeated public cycle tests](lan-link-compose-repeat.log) begin
with no controller owner, operation or canonical lease. All three effects enter
through public reconciliation: ADD, DELETE, and same-logical-link ADD. Cases cover
new allocations, identical-result guarded receipt replacement, and omitted DELETE
pool. Each effect has native task and independent DeviceOperation output checks.
Actual shared reservation-worker code releases both retained canonical leases;
real settlement publication validates explicit fake maintenance-manager cleanup.
Lease UIDs survive all three operations and transition counts advance twice.
Latest-effect lookup and unchanged InSync recognize the final ADD, retaining the
appropriate effect history without replay or erasing live retirement evidence.

These tests use native HTTPS fixtures and fake Kubernetes. Device CLI output,
initial actor grants and maintenance-manager cleanup are explicit fixture data.
The retained leases are actually updated/released by the shared worker code in
that fake cluster. This is not installed actor or accepted hardware qualification.
The mixed setup reuses older ownership fixtures; the clean-start repeated tests
additionally avoid any predecessor lease reset.

See [full affected race regressions](lan-link-compose-regression.log),
[runtime/RBAC contract checks](lan-link-compose-runtime.log),
[fresh read-only native prerequisites](lan-link-compose-live.log), and
[CVK API readiness](lan-link-compose-cluster.log). The descriptor adds
`lan-link-compose` through the existing worker rollout contract; no new transport,
credential path, actor protocol or CRD is introduced.

The repository example is `examples/configs/catalyst-center/lan-link-mixed.yaml`.

## Remaining scope

- Accepted native/hardware execution and actual post-DELETE CLI output remain
  unqualified. The CVK API still returns EOF; the lab has no recorded LAN
  discovery history. No LAN device/link/session/pool mutation was submitted by
  this work.
- An opposite action on the same port within one source remains a conflict,
  requiring an explicit ordered-transition contract rather than implicit racing.
- Opaque history without a matching current declaration, legacy/adoption paths
  and semantic explicit/omitted-pool declaration migration remain incomplete.
- Automatic stale DELETE cancellation/re-planning, shared/cross-site capacity,
  concurrent session behavior, request-budget/scale qualification and broader
  configuration composition remain pending.
- LAN discovery devices and other incomplete resource rows in the parity ledger
  remain within the active full NetAsCode model objective.

The focused mixed/existing lifecycle suite passed in **27.442s** and clean-start
three-effect public cycle cases in **14.377s**. Runtime/RBAC checks passed in
**3.788s**, fresh read-only native prerequisites in **5.249s**, and all affected
race regressions passed (exact final package results are in the linked log).
Strict documentation and whitespace validation also pass.
