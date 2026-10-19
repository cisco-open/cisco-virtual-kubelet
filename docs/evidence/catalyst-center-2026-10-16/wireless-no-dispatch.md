# Automatic retirement after proven wireless no-dispatch

Date: 2026-10-16. Pinned NetAsCode 0.5.0. Follow-up to
[definition submission snapshots](wireless-definition-dispatch.md).

## Result and boundary

For anchor groups, interfaces, power profiles, 802.11be profiles, pre-auth ACLs
and RF profiles, a final preflight refusal can now retire its exact unsubmitted
operation automatically. The config reports Waiting / `NativePreflightRefused`
and the existing positive Apply requeue schedules a fresh compilation. Section
ownership and unrelated history stay retained. This is not resubmission of a
consumed closure or recovery inferred from an error message.

## Implementation

The definition guard issues an adapter-private refusal capability only when its
read/identity check fails before invoking the original mutation closure. The
capability belongs to that guard, survives the existing group write copy, and
can be claimed once. Ordinary errors returned by the native submit closure,
missing acknowledgements, another guard's refusal and already-claimed proofs
cannot take this path. Feature-template guards retain their existing behavior.

The existing shared receipt path binds an eligible proof to its original
write-ahead operation ID. A one-use status CAS then requires the complete original
operation, controller UID/generation, one matching section owner and no task,
created result, physical/group identity, credentials, dependency or cancellation
state. Submitting or an otherwise identical taskless Unknown can be retired only
with this live proof. Changed owner/operation/controller identity or conflicting
application history retains the fence. CAS conflicts retry Kubernetes writes;
they never repeat native submission. A bounded cancellation-independent context
allows retirement when a preflight read was cancelled.

Only the six definition resource namespaces and ordinary asynchronous Create or
Update shapes are eligible. The optional write/receipt fields are private to the
Catalyst Center adapter. No CRD, public manifest option, RBAC rule, provider
contract, physical reservation protocol or parallel journal was added. Other
receipt paths retain their existing behavior. Clearing only the operation in one
CAS avoids inventing a durable Prepared state that another worker could replay.

## Verification

- [Final proof/public race tests](wireless-no-dispatch-tests.log): pass 4.575s.
  Sixteen proof cases cover refusal, read error, identical Unknown, cancellation,
  CAS retry/exhaustion, ordinary native errors, missing acknowledgements,
  foreign/replayed proof, changed operation/owner/controller, late tasks/receipts
  and feature-scope exclusion. Eighteen public cases cover each definition with
  a native-state race, failed read or lost acknowledgement. Refusals retire with
  zero native mutation calls, retain the owner, return a positive requeue, then converge
  after exactly one fresh Update. Lost acknowledgements remain taskless Unknown
  and do not replay. Spent proof reuse is refused.
- [Private Kubernetes API-server tests](wireless-no-dispatch-api.log): pass
  12.583s for all eighteen public cases, with actual CRD metadata/status and
  controlled native HTTP fixtures. These ran before the additional explicit
  requeue assertion in the final fixture suite; production code is identical.
  The installed lab cluster was not started or changed.
- [Wireless/shared configuration regression](wireless-no-dispatch-regression.log):
  race pass 102.112s, including other journal, receipt, source, wireless,
  feature-template, credential and task completion paths. It is a targeted
  selection, not a full CC-package test claim.
- [Foundation tests](wireless-no-dispatch-foundations.log): adapter registry,
  complete ND, shared software lifecycle and software-upgrade packages pass with
  race checking. Their production logic is unchanged.

## Native complete cycles

`TestLiveWirelessDefinitionRecovery` with these opt-ins passes all six cases:

```text
CATC_WIRELESS_DEFINITION_RECOVERY=isolated-definitions
CATC_WIRELESS_DEFINITION_STALE=isolated-definitions
CATC_WIRELESS_NO_DISPATCH=isolated-definitions
```

[Native log](wireless-no-dispatch-native.log): 33.60s (package 35.733s).
[Completion receipts](wireless-no-dispatch-complete.json) retain native IDs and
Create/Update/Delete tasks. Each case:

1. Creates one generated unused definition and captures its native ID.
2. Injects one synthetic HTTP 503 for the selected native collection read only
   after the durable operation enters Submitting. The original mutation closure
   is not invoked. This is controlled client-side fault injection, not a claim
   that the appliance naturally failed.
3. Verifies Waiting / `NativePreflightRefused`, no operation, the retained owner
   and no additional mutation. Private evidence retains this controller snapshot.
4. Reconciles fresh intent, performs one real native Update, serializes its known
   task as Unknown, and completes task-tree/readback recovery to InSync.
5. Refuses the older retained stale plan and its reuse before HTTP, then proves
   three no-op reconciliations add no writes.
6. Deletes only the captured generated ID and restores the original inventory
   projection. All six fixtures are removed; each has exactly three native
   mutation attempts, eighteen total.

This exercises the public reconciler and actual controller APIs with simulated
Kubernetes persistence. The private API-server tests separately qualify status
persistence. Installed workers, real actor coordination and device convergence
are not claimed. ACL and RF names still deliberately contain `443` and `100`,
maintaining the numeric-name regression check from the previous qualification.

## Remaining limits

A process crash before proof retirement loses the in-memory capability and keeps
the write-ahead fence. Older taskless Submitting/Unknown records cannot gain this
proof from current metadata, an API error or apparent native absence. No force
release or inferred replay is introduced. Feature-template and other resource
preflight retirement remains pending. This change does not add Create-result
ownership, adoption, pruning, physical WLC/AP deployment or installed rollout.
Full 93-resource parity remains incomplete and support flags remain partial.


A subsequent extension now qualifies current CleanAir/FRA owned lifecycle and
legacy Update preflight retirement using this same proof mechanism; see
[feature lifecycle no-dispatch recovery](wireless-feature-no-dispatch.md).
Older taskless outcomes and process-lost proof still retain their fence.
