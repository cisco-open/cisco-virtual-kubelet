# Extranet managed creation and owned-expansion planning

Later stage: see [public lifecycle evidence](extranet-public-recovery.md) for the
current bounded routing and qualification. The text below records this earlier stage.

## Implemented boundary

Extranet policy creation and additive expansion now have private managed planning
bridges. They use the existing repeated native/managed observer and existing
CiscoDevice/Node identity validation. The complete footprint includes explicit
fabric/zone scope, zone parents, and fabrics associated with the provider and
subscriber VNs. Non-border devices and remote subscriber-fabric devices are not
discarded. No alternate transport, lease, reservation, receipt or journal is added.

The native operation's dependency digest excludes the selected policy because
creation/update is an expected postcondition. Managed preparation additionally
binds the complete policy baseline, including absence. This detects a policy
appearing transiently between native observations, as well as changes to peers.
The existing native snapshot and operation resource format are unchanged.

Creation requires an explicit dispatch guard. The guard is one-use even when
denied or when the response is uncertain; native dependencies are reobserved
after the guard before POST. Managed creation returns a candidate with dispatch
unconditionally denied. Concurrent consumers cannot submit twice.

Managed expansion reuses the existing retained creation receipt/UUID ownership
rules and additive-only native planner. It also checks the selected intent
against the actual current source, brackets planning with source/controller
observations, and retains the existing policy UUID. A common managed mutation
helper avoids separate identity/footprint protocols for Create and Update.
The declaration reader is shared with existing atomic receipt recovery.

Managed planning does not create a journal, reservation or claim, cordon a Node,
submit a policy mutation or establish native convergence. Public Apply remains
gated until group preparation, exact dispatch, recovery and settlement are wired.
These private bridges are implementation progress, not advertised apply support.

## Verification

The final race run covers all extranet tests, the existing fabric multicast
managed-target consumer, provisioning pacing and SWIM. It passed in 8.615s.
Fixtures cover:

- Parent/zone, non-border and remote subscriber-VN physical footprints.
- Missing managed peers, replaced Nodes, native serial/VN changes, missing
  membership evidence and unbounded omitted scope.
- Selected-policy appearance, including a transient second-observation result,
  and changes to unrelated policies.
- Converged no-op, retained ownership, wrong native UUID, foreign owner,
  undeclared/changed intent and source/controller changes during observation.
- Deliberate dispatch refusal, concurrent one-use consumption, native changes
  during the guard and uncertain acknowledgement without replay.
- Existing native create/update, task/readback, ownership and atomic receipt
  application/recovery behavior.

The source-race test uses a separate updated object while retaining the caller's
original snapshot. Local Kubernetes and native HTTP actors are simulated.
ND, shared configuration/reservation/controller protocols, direct upgrades,
maintenance and mutation guards passed separately. Runtime manager checks and
strict documentation results are recorded in companion logs. The full Catalyst
Center package suite was not rerun for this private-path addition and declaration
reader refactor; the scope above is the verification performed on this revision.

## Native lab observation and remaining work

Authenticated read-only first-page queries returned HTTP 200 for all three APIs:
fabric sites: zero rows; L3 virtual networks: two rows; extranet policies: zero
rows. No isolated fabric fixture was available or selected. These observations
do not qualify any mutation, policy forwarding or installed actor behavior.
No appliance or physical device mutation was submitted by the lab check.

Remaining work includes the public group preparation/dispatch/recovery lifecycle,
whole-model prerequisite composition, verified settlement and receipt retirement,
qualified removals/replacement, omitted-scope semantics, installed actors and a
native isolated fabric create/update/verify cycle. Full NetAsCode parity remains
open.
