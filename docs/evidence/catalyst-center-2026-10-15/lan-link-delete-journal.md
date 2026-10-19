# LAN link DELETE: private journal, dispatch and acknowledged recovery

The implementation on `pr/johalley/catc-support` now admits the bounded private
DELETE lifecycle to the existing configuration group journal. Public Apply still
refuses DELETE declarations. This is a staged implementation, not full NetAsCode
LAN link parity or accepted hardware execution qualification.

## Implementation

ADD and DELETE share one implementation of preparation, source/native
revalidation, the all-member submission permit, authority checked immediately
before native submission, and task acknowledgement retention. Product-specific
strategies supply observations and frozen payloads; there is no new transport,
credential path, CRD, lease family, reservation actor or manager protocol.

DELETE preparation validates every declaration, requires a verified ADD receipt
whose configured interfaces match, and requires retirement of the predecessor's
original reservation objects. Mixed actions and an unowned second link refuse
preparation of even a valid first link. DELETE's original resource binds logical
identity, predecessor receipt, physical endpoint identity, declared model fields
(including omitted pool), and the pre-dispatch snapshot. Pruned/malformed group
identities cannot fall through to a single-device operation.

An acknowledged task can recover into `Verifying` through the same guarded
shared transition as ADD. Recovery selects the exact admitted declaration and
retained original ADD receipt, brackets native reads with source/controller and
managed member identity, and requires complete successful rooted task evidence.
Both exact physical interfaces must still be present and reachable, with empty
IPv4 address/mask fields. Stale topology addresses, active sessions, changed
physical devices/ports or foreign ownership hold. LLDP may remain or disappear;
its disappearance alone is never proof of deconfiguration. Recovery needs no
current pool lookup because a pool may have been retired after the accepted
DELETE. Loss of an acknowledged task, missing predecessor or uncertain submission
keeps the original fence and never resends the native mutation.

`Verifying` is not completion. This work has no DELETE receipt writer and cannot
release either device reservation based only on task/native inventory evidence.
Independent post-task device deconfiguration proof, atomic receipt publication,
settlement, deleted-state idempotency/re-add and public composition are pending.

## Validation scope

Native HTTPS fixtures with fake Kubernetes exercise real shared journal/permit
code. Explicit fake grants and settlement are identified in test helpers. The
predecessor helper does not perform real lease release; tests explicitly remove
its fake leases before installing a subsequent fake grant. These are not installed
worker, lease-release or hardware attestations.

Focused cases include complete-source refusal, malformed/pruned groups, missing
or revoked authority, stale source, changed peer/port, active sessions, lost
acknowledgement, restart no-replay, failed task descendants, partial cleanup,
stale topology, missing receipt/foreign owner, and native-only success retaining
both members without a DELETE application.

See [focused tests](lan-link-delete-journal-focused.log),
[recovery tests](lan-link-delete-journal-recovery-focused.log),
[full affected regressions](lan-link-delete-journal-regression.log),
[runtime contracts](lan-link-delete-journal-runtime.log),
[read-only native prerequisites](lan-link-delete-journal-live.log) and
[installed cluster probe](lan-link-delete-journal-cluster.log).

The fresh lab read qualified three native topology nodes and two adjacencies,
maximum five sessions, zero active sessions and zero discovery history. The
CVK API still returned EOF. No LAN deletion/session/device/pool writes were
submitted to the lab, and installed qualification remains pending.

The focused dispatch suite passed in **12.622s** and focused DELETE/ADD recovery
in **41.320s**. Full affected race regressions passed (Catalyst Center **369.573s**;
NetAsCode parsing, shared configuration, reservation workers, Nexus Dashboard,
controller and managed protocol cached passes). Runtime/RBAC checks passed in
**3.752s**. Fresh read-only lab prerequisites passed in **9.087s**.
