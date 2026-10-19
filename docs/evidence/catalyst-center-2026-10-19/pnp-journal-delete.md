# Journaled PnP removal and restart verification

The private source-prune execution stage now uses the existing configuration
journal for synchronous PnP Delete acknowledgement, independent absence
verification, atomic receipt publication and guarded slot release. Public
manifest routing and CR finalization remain gated.

Planning requires an owned canonical baseline, explicit source-prune Delete,
pinned inventory-only intent and omission of the selected canonical serial from
the complete supported source. It binds the original creator receipt into the
Delete resource, preserves the exact target UUID, rechecks capacity before a
native request and uses the existing shared dispatch permit/one-use guards.
The retained identity and current native snapshot are compared without guessing
the original serial spelling or inventing a site. Registration and canonical
identity paths share one payload builder.

A validated synchronous HTTP acknowledgement advances the existing journal to
Verifying. A new adapter can then recheck source, ownership, original baseline,
exact journal and repeated native/managed-serial absence. The shared action writer
publishes Verified and a distinct absence receipt atomically. Generic verification
cannot bypass this receipt. The same admission protects both preparation and the
start permit; a Prepared record containing submission evidence can neither get
a fresh permit nor release its slot. Release is refused if the original creator, current
owner or absence receipt disappears. Unknown protocol versions are also fenced.
A lost native acknowledgement stays Unknown even when native absence is observed;
there is no automatic replay or conversion of absence into acknowledgement.

## Qualification

The controlled and real-API restart/refusal matrix passes. Cases include source
removal, successful restart verification, lost response, missing receipt/creator/
owner, native drift, post-Delete managed serial presence and exhausted receipt
capacity. Capacity, drift and incomplete proof refuse completion without an
additional native request.

The native generated-record cycle passes public registration, canonical baseline,
source replacement with explicit empty PnP intent, private journaled Delete,
JSON-persisted acknowledgement, fresh-adapter absence verification, atomic receipt
publication and receipt-gated release. Exactly one POST and one DELETE restore the
PnP/network baselines. Kubernetes persistence in this native test is simulated;
real API-server tests qualify storage/CAS separately. No physical claim/reset or
device action is performed.

The controlled/real-API matrix passes 21 test/package events. Selected
PnP, shared-journal/receipt, template-reference and upgrade/recovery regressions
pass 1005 events; the five foundation packages and main binary build also
pass. See [results and source hashes](pnp-journal-delete.json).

## Remaining public execution gates

This does not expose public manifest-driven PnP deletion or complete the pinned
resource. Remaining work includes public family selection/reconciliation for
removed sources, whole-model ordering/preflight, complete consumer and discarded
private-content protection, no-dispatch retirement, CR deletion/spec binding,
manager finalization, installed actors and broader stack/contacted-device cases.
The safe native projection is not proof about discarded configuration bodies.
Legacy records without canonical baselines remain fenced; imported records gain
no authority. The full 93-resource NetAsCode goal remains incomplete.
