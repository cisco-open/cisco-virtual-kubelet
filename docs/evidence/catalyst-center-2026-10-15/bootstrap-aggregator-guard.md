# Aggregator bootstrap write guard and cleanup evidence

## Integrated behavior

The manager now supplies the same shared bootstrap namespace and uncached reader
to the independent config aggregator. Before resolving credentials/starting a
worker, aggregator reconciliation checks current Device/source identity and
absence of retained serial evidence. A hold or read failure cancels an existing
worker and requeues; cancellation is not an acknowledgement of drained I/O.

For existing workers, each IOS-XE write transaction now uses the engine's existing
`AcquireMutation` hook to repeat that observation. NX-OS receives the same optional
hook through its facade and common reconciler, for both normal application and
relinquishment cleanup. No new transport or engine state machine is introduced;
nil hooks retain existing callers and report-only behavior still skips the write
guard. Family lease namespace/arbitration behavior is unchanged.

The actual Device UID/generation/spec/retained identity/Lease observation is shared
with the CiscoDevice manager guard in controllerconfiguration, instead of being
copied into the aggregator. A guard freezes its original Device input, so caller
memory changes cannot retarget it. Each invocation rechecks current known serials
through uncached API reads; an existing worker cannot reuse an earlier absence
when a bootstrap Lease later appears. Its finish callback never removes or
changes bootstrap evidence and makes no claim about holding a distributed lock.

An empty bootstrap namespace preserves prior local/test configurations. Known
spec-declared and retained physical serials are checked. Unknown legacy aliases
remain outside this observation; the runtime must not infer their containment
from a successful check with no declared identity.

## Cleanup correction

While adding denial tests, the delete/relinquish paths were found to treat any
engine result other than Failed as successful cleanup. The engine reports
`LeaseBlocked` when a mutation guard refuses, so that path could remove a config
finalizer without pruning its owned keys. IOS-XE and the common NX-OS cleanup
paths now require InSync before returning successful completion. Guard refusals
retain the config finalizer and recorded owned keys, with no device mutation.
Existing family-lease release-on-attempt behavior is unchanged; this is not a new
exclusive family-ownership protocol.

## Verification

Focused race-enabled aggregator/provider/controller tests passed in 2.910,
2.613 and 2.796 seconds respectively. Guard fixtures cover retained/malformed
serial evidence, absent evidence, retained identity after spec pruning, missing/
failed uncached reads, changed source/replaced Device, frozen caller inputs,
new evidence seen by an existing guard and unchanged default configurations.
An existing aggregator worker is cancelled before credentials/startup on a hold;
its retained Lease remains present.

NX-OS facade-to-engine tests exercise allowed mutation with one completion
callback, report-only reads without calling the guard, denied application with no
device fetch/save/delete and denied delete cleanup preserving owned VLAN and
finalizer. IOS-XE direct and polling cleanup both refuse guard denial, perform no
prune and retain ownership/finalizer evidence.

Full race-enabled suites passed: aggregator 3.302 seconds, provider 5.180,
controller 33.884, manager 10.658, shared configuration 33.925, provider reservation
3.875. ND and pinned model tests passed from cache. CC configuration/inventory/
PnP/SWIM regressions passed in 71.275 seconds. Full provider/controller
coverage includes existing direct-upgrade, gNOI, SWIM, config, handoff and cleanup
fixtures; no existing driver/provider protocol was replaced.

A read-only lab probe returned HTTP 200 for token generation, PnP and network
inventory, with zero PnP records and three network devices. No new native fixture,
site claim, upgrade/reset or lab aggregator deployment was submitted in this
stage. These guard/cleanup tests use simulated Kubernetes/device state and do
not qualify installed hardware containment.

## Remaining requirements

A guard blocks newly attempted known-identity transactions. It does not prove
an earlier native mutation completed or a cancelled worker drained. Installed
manager/worker rollout and actor authority, acknowledgement of quiescence,
unknown/unmanaged legacy aliases, other direct-worker mutation consumers,
production bootstrap authorization, one-use dispatch, cancellation/settlement/
release and correlated native claim/template completion remain pending. Public
claiming remains gated and full NetAsCode execution parity remains incomplete.


The subsequent [worker retirement lifecycle](bootstrap-aggregator-retirement.md)
now waits for observed local loop/transport completion before same-key
replacement. This is in-process evidence; durable quiescence, unknown native
outcomes, aliases and production dispatch authority remain separate requirements.
