# Aggregator retirement before worker replacement

## Integrated behavior

Cancelling an aggregator worker now moves it into retained in-process retirement
state instead of treating removal from the active map as completed shutdown.
Replacement for the same Device key waits for the worker loop to return and its
transport cleanup to finish. Pending or missing completion handles and failed
transport cleanup remain retained. Repeated cancellation cannot discard the
record or cause an overlapping replacement. Other reconciliation passes requeue
while retirement is incomplete.

IOS-XE and NX-OS use one shared worker-loop lifecycle helper. A completion channel
closes only after loop return, child-context cancellation and cleanup. The
IOS-XE version-retry goroutine is joined before transport closure. Subscribe
watchers now use the per-device context instead of the manager root context;
after cancellation their notification pump is drained before transport Close.
Cleanup errors are published before completion and prevent automatic replacement.
A new worker registration rechecks retirement under the same mutex, avoiding a
check/register gap. Factory startup is also refused while prior retirement is
known to be incomplete.

Worker reuse now binds Device UID/generation and retained Node identity as well
as the existing spec/credential hash. A recreated Device or changed retained
binding cannot silently reuse the old worker. The binding hash excludes
credential values. Successful retirement permits the current incarnation to
start; unchanged active workers remain idempotent. Already-exited workers are
not treated as serving an unchanged spec.

## Verification

The final full aggregator tests passed in 3.008 seconds with race detection.
Deterministic channel-based tests distinguish cancellation delivery, Run return,
transport cleanup start and cleanup completion. Replacement is refused before
both loop and cleanup complete. Concurrent stop requests retain one retiring
record; failed Close and missing completion handles remain held. Device
UID/generation/Node-binding rebind tests wait for observed cleanup before starting
the replacement. The existing lifecycle test now observes completion channels
rather than assuming synchronous rotation or sleeping for teardown.

The broader race-enabled regression passed: aggregator 2.911 seconds, full
controller 33.188 seconds, manager 9.681 seconds. Provider, shared configuration,
provider reservation, ND and pinned model tests passed from cache. CC
configuration/inventory/PnP/SWIM regression passed in 70.213 seconds. Existing
provider/controller coverage includes direct upgrades, gNOI, SWIM, ownership,
configuration and handoff fixtures. No CRD, RBAC, transport protocol, device
mutation lease or native execution capability was added.

## What this does not prove

Retirement is local and keyed by Device namespace/name. Completion proves that
this manager-owned loop and its tracked cleanup finished; it does not prove that
a remote device committed or rolled back an RPC, or that a transport's internal
upstream goroutines independently acknowledged termination. Unknown native
outcomes still need the existing journal/task/device evidence. Aliases under
other Device keys and unidentified legacy hardware require separate containment.

These records are not durable across manager restarts. A production bootstrap
authorizer still needs installed manager/actor/RBAC rollout proof, containment of
other direct-worker consumers, retained unknown-outcome evidence and a durable
quiescence/settlement protocol before issuing a one-use native dispatch grant.
It must not infer that no worker or native mutation exists merely because a
process restarted. Public claiming, cancellation/release and correlated claim/
template completion remain gated.

Tests use simulated Kubernetes/device operations; no lab aggregator deployment,
physical upgrade/reset or native PnP fixture was created in this stage. Installed
hardware quiescence and full NetAsCode execution parity remain unqualified.


A subsequent [physical alias fence](bootstrap-aggregator-physical-aliases.md)
now extends local retirement exclusion across Device keys sharing known declared
or retained serials, including cleanup of rejected candidates. Unknown hardware,
other consumers/processes, restart durability and native settlement remain pending.
