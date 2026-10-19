# Bulk fabric-device convergence and atomic receipt bridge

## Implemented

A private bulk fabric-device completion bridge now requires independent
convergence proof before constructing or publishing a final action receipt.
It reuses the existing rooted task-tree completion reader, original bulk resource
identity/mask and incoming selection, complete native membership observer,
repeated native/managed footprint resolver and atomic group receipt contract.

The proof receives the exact operation, every incoming/peer/parent physical
incarnation and the latest rooted native task completion boundary. It receives
private copies. Native and managed observations and the completion boundary must
remain identical after proof. Future, zero or second-resolution-unrepresentable
completion boundaries cannot reach proof/publication. Configuration identity is
frozen before external proof and reread around it, preventing a source change
from becoming the new expected identity during verification.

The stable receipt key includes native fabric identity, ordered incoming desired
roles/border settings and the complete physical incarnation. It excludes task,
nonce, creation mask and transient baseline record IDs. The existing source/owner/journal observer wraps receipt construction. Source/ownership and
all-member actor authorization bracket the existing atomic journal/receipt write;
retained predecessors use the existing replacement contract. This does not
release, settle or delete any reservation.

The repeated observation/proof bracket is shared with existing wired bulk
completion. No new transport, provider framework, status store or RBAC permission
was introduced. ND, direct upgrades and SWIM interfaces were not changed.

## Validation

The final focused race suite passed in 16.603 seconds. New fabric tests exercise:

- successful publication and callback mutation of private proof input copies;
- missing/denied proof and partial or failed native outcomes;
- native membership, managed peer, source, controller and task-completion changes during proof;
- missing/foreign ownership refusal before invoking proof;
- future native completion, missing members and denied/revoked authorization;
- a lost response after the atomic write, retaining both receipt and Verified journal;
- exactly one native submission and retention of all three member claims without
  fabricated completion, release or manager settlement.

Existing wired bulk application and acknowledged fabric recovery tests also pass.
See `fabric-devices-bulk-application-focused.log`.

The fixture supplies native HTTPS responses, Kubernetes objects/authority and
independent-proof decisions. It is not installed actor or real device attestation
qualification.

## Lab and remaining scope

Authenticated read-only lab fabric inventory succeeded but found no fabric sites;
the live qualification test explicitly skipped accepted fabric execution. See
`fabric-devices-bulk-application-live-prerequisites.log`.

Public bulk dispatch remains fenced until an actual source-bound device proof
provider is installed and qualified. Public final receipt wiring, complete
root-to-zone/chunk lifecycle, native no-op proof, manager/worker settlement,
whole-model composition and real fabric qualification remain pending. This bridge
is a necessary execution/ownership component, not a full parity claim.


## Final regression

The final race suite passed for the complete Catalyst Center adapter and
NetAsCode parser, shared controller-configuration contracts and Nexus Dashboard.
See `fabric-devices-bulk-application-regression.log`. Strict documentation and
whitespace checks also pass. Public execution, actual independent device proof,
installed actors and real fabric qualification remain incomplete.
