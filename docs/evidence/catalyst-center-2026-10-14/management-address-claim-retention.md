# Single-device claim retention and address recovery integration

Added a read-only single-device counterpart to the existing group retained-claim
observer. It reuses the immutable operation-binding digest and live lease-grant
validation. It checks the exact reservation incarnation/spec/status, controller
UID and journal, correlated claim/time, held lease identity/holder/transitions
and renewal lifetime, then rereads those objects to reject changed evidence.
It does not require current desired intent, worker readiness or the
new-submission freshness window: retention keeps an existing fence and cannot
grant dispatch, refresh or release authority.

This is an additive common helper. Existing group, submission, grant freshness,
actor and settlement behavior, schemas and permissions are unchanged.

Management-address retained recovery now combines the admitted old-endpoint
baseline and live device/Node/worker checks with that observer and the exact
current journal phase/task receipt. The helper returns evidence only. Current
source/owner authorization, controller worker, independent endpoint proof and
rollout/settlement still belong to the full recovery bridge.

Shared fixtures cover accepted, unknown and verifying work; retention after
controller pause/revision; older grants with a still-live renewed lease; and
wrong/future claims, stale reservations, foreign/expired leases and settled
claims. Address integration fixtures reject foreign/expired leases, wrong
claims, controller replacement and changed task receipts. Combined race run:
shared configuration 3.820s, Catalyst Center 3.178s; see
[retained output](management-address-claim-integration.log).

The full shared configuration lifecycle race suite also passed (31.123s),
including existing group, dispatch, grant and settlement behavior; see
[full regression output](single-retention-full-regression.log).
Strict documentation build and `git diff --check` passed.

No lease was acquired/refreshed/released, device declaration changed or lab
management-address mutation issued in this increment. Full managed public
dispatch/recovery, CVK address/worker rollout, independent old/new physical
reachability, installed actors and accepted live transition remain pending.
