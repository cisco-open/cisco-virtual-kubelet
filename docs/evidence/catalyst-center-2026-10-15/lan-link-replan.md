# Stale LAN preparation cancellation and replanning

## Implemented scope

Public LAN Apply can now retire an unused stale pre-dispatch snapshot through
the existing group cancellation protocol. It never rewrites the original resource
hash, target set, operation ID or permit. Ordinary Apply prepares a new operation
only after the original cancellation has fully settled and its receipt is retained.

Cancellation requires all of the following:

- The original operation is Prepared, v2, unsubmitted and unacknowledged, with no
  existing cancellation request.
- Every link in the current complete source has a positive supported observation.
  The selected link retains the same logical digest, immutable native dependency
  digest and exact full CiscoDevice/Node footprint, but its disposable pre-dispatch
  digest differs. Changed usage counters and refreshed topology IDs are examples.
- Source/config/controller revisions and section ownership still match the original
  operation. Unobservable evidence or changed immutable identity cannot establish
  eligibility for this path.
- Both original persisted reservations exist with the exact currently bound
  device/controller worker identities and complete group identity. The connector
  creates no missing records and does not substitute a new worker incarnation.
- Installed all-member runtime authority passes, bracketed by fresh complete native,
  source, candidate and member reads. Callback inputs are private copies.
- Shared cancellation receipt capacity remains available. Full history stays fenced;
  the connector cannot prune evidence to admit new work.

Grant renewal or a concurrent partial claim does not turn cancellation into
cleanup authority. Existing claims remain retained. Wrong/future claims, replaced
members and unsupported lifecycle evidence hold. The cancellation CAS races
Prepared-to-Submitting on the same controller resourceVersion; Submitting or
uncertain work cannot become a cancelled preparation.

## Existing actors remain responsible

After the durable Cancelling fence, existing device workers acknowledge the exact
member cancellation and release only their own canonical lease. Existing manager
maintenance cleanup and cancellation settlement attest restoration. The connector's
terminal recovery cannot clear the journal before every original member settles.
It retains the original cancellation receipt before subsequent Apply prepares a
fresh ID/resource snapshot. Cancellation publishes no Verified action receipt and
does not imply a native LAN request ran.

No new CRD, transport, lease/status store, authorization protocol or RBAC permission
was added. Only the CC LAN-specific public path initiates this narrowly qualified
use of the shared cancellation primitive. ND, direct-device upgrades and SWIM keep
their existing paths.

## Tests and qualification

The focused cancellation/replan race suite passed in **8.271s**. It covers mutable
usage and topology changes, partial claims, ungranted members, unchanged baselines,
changed firmware/source/member identity, missing members, denied actors, full
history, late source/native/member/submission changes, lost cancellation response
and callback-copy isolation.

The composed public test invokes the real device-worker cancellation reconciler
and FamilyLeaser with RequireExisting against fake Kubernetes and retained canonical
leases. It checks acknowledgment, actual holder release, refusal to finish before
manager settlement, shared settlement publication, retained cancellation evidence
and fresh preparation with no native API mutation. Maintenance cleanup is supplied
as an explicit fake manager observation; this is not an installed actor or appliance
qualification. The complete LAN/descriptor race suite passed in **39.347s**.

The final complete affected race suite passed: Catalyst Center **327.156s**,
device-worker configuration-reservation package **3.540s**, plus NetAsCode parsing,
shared configuration protocol, Nexus Dashboard, controller/manager and managed
protocol (cached passes). Runtime actor/descriptor and managed role checks passed
in **3.765s**. See [full regressions](lan-link-replan-regression.log),
[runtime](lan-link-replan-runtime.log), [focused](lan-link-replan-focused.log)
and [worker/replan cycle](lan-link-replan-cycle.log).

The first automated live prerequisites read timed out at the 20-second request
bound; [initial log](lan-link-replan-live-initial.log) is retained. The existing
HTTPS tunnel/UI then responded HTTP 200, and direct token generation and native
session reads both returned HTTP 200. Retrying the completed read-only test run
passed in **5.546s** without credential or tunnel changes: maximum five sessions,
zero active sessions, two stable adjacencies and no discovery history. See
[recovered live prerequisites](lan-link-replan-live.log). No native LAN/session/pool
mutation was submitted, and these prerequisite reads do not qualify cancellation
or settlement on deployed actors. Strict documentation and whitespace validation
also passed.

## Remaining

Installed runtime, actual maintenance cleanup/settlement and native LAN execution
remain unqualified. The existing lab Kubernetes API still returns EOF; the
[readiness probe](lan-link-replan-cluster.log) confirms installed testing remains
unavailable. Its previously captured containerd recovery failure is not repaired
by this feature. The lab's missing recorded discovery
origin is not bypassed or manufactured.

Changed-source or changed-immutable-scope cancellation, missing/orphaned original
members, automatic evidence-history collection, large-model request budgets,
explicit link deletion, LAN discovery execution, Report and broader model
composition remain pending. Those conditions keep their original fence and do not
reuse this positive-snapshot cancellation path.
