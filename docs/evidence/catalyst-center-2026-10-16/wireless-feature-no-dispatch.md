# Feature lifecycle automatic no-dispatch recovery

Date: 2026-10-16. Pinned NetAsCode 0.5.0. Follow-up to the
[shared wireless proof mechanism](wireless-no-dispatch.md).

## Implementation and scope

CleanAir/FRA now use the existing guard-bound one-use refusal proof for owned
Create, Update, source-prune Delete, tombstone-bound recreation and
spec/deletion-bound config cleanup. Existing unowned Updates use their original
UUID-bound legacy namespaces without acquiring creator/applied ownership.
Unbound legacy Creates remain excluded.

Companion-group guards delegate claims to the selected child's guard when the
inner check refuses. A group copy therefore preserves the correct refusal
capability instead of dropping it or accepting another guard's proof. Read-only
source/owner/receipt/capacity authority checks run inside certified preflight,
after native snapshots and before the mutation delegate. The delegate itself
retains the existing shared no-replay mutation client.

The common receipt/journal path still binds the proof to the original operation
ID, permits one claim/spend, checks complete original operation identity,
controller generation, section owner and absence of task/result/physical/secret
state, and preserves all application history. It clears only that proven
unsubmitted slot and returns the existing Waiting/NativePreflightRefused requeue.
A fresh compilation must pass normal ownership, fingerprint, reference, source,
capacity and dispatch checks. No new provider interface, CRD, RBAC grant,
credential store or parallel journal was introduced. ND and direct-device/SWIM
production logic are unchanged.

## Fixture and API results

The [eighteen race cases](wireless-feature-no-dispatch-tests.log) pass in 10.322s.
Six stages (Create, unowned Update, owned Update, source prune, recreation and
config finalization) each test selected-read refusal, companion-read refusal and
lost acknowledgement. Every refusal retires without a mutation, preserves the
original owner/history and returns a positive requeue before exactly one fresh
action per definition. Imported Updates retain no creator/applied receipts.
Lost acknowledgements keep taskless Unknown and do not replay. Finalizer cleanup
uses a status-only worker client; metadata access remains denied.

The same eighteen cases pass on a [private real API server](wireless-feature-no-dispatch-api.log)
in 30.900s, including actual config deletion/generation/status persistence and
finalizer retention. Native services there are controlled HTTP fixtures. An
initial cold-fixture nil-journal assumption and a lost-ack fault applied during
private import seeding were corrected before these final passes.

[Wireless/shared configuration regression](wireless-feature-no-dispatch-regression.log)
passes in 110.720s. [Registry, ND, lifecycle and upgrade foundations](wireless-feature-no-dispatch-foundations.log)
pass with race checking. These are named package/selections, not a full CC-package
or installed-worker rollout claim.

The final combined [52-case proof/public safety selection](wireless-feature-no-dispatch-final-safety.log)
passes with race checking in 12.161s, including the six earlier definition types
and the clarified operator message that no configuration mutation was sent.

## Native config-deletion cycle

The [native public cycle](wireless-feature-no-dispatch-finalizer-native.log) passes
in 45.86s (package 48.004s). It injects one synthetic HTTP 503 for a feature summary
read after each original operation becomes Submitting. No configuration mutation
is invoked by that refusal. A following read witnesses the operation gone, with
unchanged owner, credential/application history and mutation count, before a fresh
dispatch. The fault is controlled client-side injection, not an appliance failure.

Sixteen refusals retire automatically: 2 Creates, 12 Updates and 2 config-cleanup
Deletes. Exactly sixteen native mutation requests follow, with original
acknowledged task/UUID recovery for each. Seven manifest revisions converge;
manager enrollment, status-only worker cleanup and manager finalization complete.
Both native baselines are restored, the config is removed, and six lifecycle
records remain. See [refusal proof](wireless-feature-no-dispatch-finalizer-proof.json)
and [completion](wireless-feature-no-dispatch-finalizer-complete.json).
The existing manager harness emits a nonfatal missing-logger diagnostic; terminal
PASS confirms completion. Kubernetes persistence in this run is simulated.

## Native recreation qualification and pacing

An initial [unpaced recreation run](wireless-feature-no-dispatch-rate-limit.log)
performed all 28 effects with 28 proven refusals retired, then failed its final
baseline GET with HTTP 429. The saved final controller state had no operation and
18 lifecycle rows. The [fault counts](wireless-feature-no-dispatch-rate-limit-proof.json)
are retained without treating this failed run as a full qualification pass.

Before any new mutations, independent read-only API calls compared both complete
summary inventories with their original saved baselines: three system CleanAir
and two system FRA definitions, with no generated fixtures remaining.
[Recheck evidence](wireless-feature-no-dispatch-baseline-recheck.json) records this.
No Delete or other mutation was repeated to recover from an observation failure.

The live client now reuses the production adapter's shared transport limiter
(default 5 requests/s, burst 10). Heavy qualification can explicitly set
CATC_LIVE_REQUESTS_PER_SECOND=2 (burst 1), without changing production defaults or
adding any mutation retry. Feature fault qualification has a longer bounded test
context for this pacing. The [fresh paced run](wireless-feature-no-dispatch-recreate-native.log)
passes in 427.88s (package 430.250s), with 826 transport requests including
508 summary reads. It completes fourteen source revisions and two full
recreate/update/prune cycles, verifying new UUIDs, exact task recovery,
unchanged original history and both original baselines.

All 28 injected refusals retire before fresh dispatch: 6 Creates, 16 Updates
and 6 Deletes. Exactly 28 native mutation attempts and 28 original-task
recoveries occur. Eighteen lifecycle rows and four retired cohorts remain, with
no operation and no generated native fixtures. See the
[refusal/request proof](wireless-feature-no-dispatch-recreate-proof.json) and
[completion receipt](wireless-feature-no-dispatch-recreate-complete.json).
Together the two successful native workflows qualify 44 proven refusals and
44 intended effects; rejected plans add no mutation requests.

## Remaining limits

Old taskless outcomes and a process crash that loses the in-memory proof remain
fenced. No current metadata, absence or error string can reconstruct a dispatch
proof. Create-result/native-state/receipt checks, reference refusals and unsafe
prune protections are unchanged. Native unowned-update fault recovery, installed
actors, WLC/AP behavior, parent-controller deletion, cross-owner handoff and
broader field/default coverage remain unqualified. Full 93-resource parity is
incomplete and whole-resource flags remain partial.
