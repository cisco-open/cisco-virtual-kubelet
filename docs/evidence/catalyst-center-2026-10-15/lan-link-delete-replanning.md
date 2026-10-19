# DELETE re-planning and versioned declaration semantics

The Catalyst Center adapter on `pr/johalley/catc-support` now reuses the existing
unused-preparation cancellation protocol for DELETE as well as ADD. New v2 DELETE
operations also distinguish exact submitted source from post-completion pool
omission semantics. This is additional bounded LAN link behavior, not completion
of the full pinned NetAsCode model or installed/hardware qualification.

## Unused DELETE snapshots

A current positive full-model observation can identify a stale DELETE baseline
only when its logical link, original verified ADD receipt, physical endpoint
commitment and raw/semantic declaration commitments are unchanged. Native snapshot
metadata may differ, such as a topology cache ID or port operational state. Missing
or changed configured ownership, software, pool provenance, source, group or actor
evidence cannot establish a disposable baseline.

Both LAN actions share the same immutable-scope matcher and existing cancellation
CAS. Original operation/resource/group identity is never refreshed in place.
Every original member must already exist with the exact persisted worker binding;
the connector creates no replacement recovery reservations. Partial claims are
retained. A submission race, lost acknowledgement, denied authority, missing or
replaced member, changed source/native evidence or full cancellation history
keeps the appropriate original fence.

Public DELETE now follows durable Cancelling through actual reservation-worker
acknowledgement/release, complete manager settlement and retained cancellation
receipt. Only then can ordinary reconciliation prepare a new DELETE with the
current snapshot. No native DELETE is sent while cancelling. An unchanged valid
preparation can still follow normal all-member admission and submission.

## Versioned source identity

New DELETE resources use v2 and retain both the exact raw declaration digest and
a pool-independent declaration digest. Logical identity still commits the
resolved admitted pool; physical identity, predecessor receipt and prepared
snapshot remain bound. Both v1 and v2 readers validate strict bounded lowercase
hex identities and preserve reserved-prefix refusal for malformed/pruned input.
No CRD, actor protocol, credential path or transport is added.

Raw declaration, source version/generation and immutable operation identity still
freeze in-flight requests. Accepted work cannot migrate between omitted and
explicit pool by changing its source. After independent completion, a v2 receipt
can recognize the same deleted link under omitted pool or an explicit reference
to the original admitted pool, using native result equality without another
mutation or capacity read. Wrong pools, names, interfaces or physical identities
remain fenced. This is idempotency, not fresh device attestation.

Legacy v1 receipts retain exact raw-declaration semantics. Missing semantic scope
is never invented. In particular, an unused v1 preparation is not silently
replanned into v2; explicit legacy migration remains pending. A legacy accepted
operation can still recover with its original source/shape.

## Evidence

Cancellation cases in the full regression suite cover operational
and topology drift, unchanged intent, partial claims, missing/ungranted/replaced
members, denied authority, source/native/member/submission races, callback-copy
isolation, full history and lost status acknowledgement. The public composition
runs actual reservation-worker cancellation/release and RequireExisting retained
lease code against fake Kubernetes, then the real manager settlement publisher
validates explicit fake maintenance cleanup. Original cancellation evidence
survives and a fresh preparation is produced without another native request.
Existing ADD cancellation is tested through the same implementation. The
[focused public composition](lan-link-delete-recovery-replan.log) exercises the
new public re-planning/settlement and existing public DELETE cycles with v2
identities; the full suite includes the complete private cancellation race matrix.

[Versioned declaration tests](lan-link-delete-recovery-scope.log) cover strict
legacy/current parsing, post-completion explicit-to-omitted and omitted-to-explicit
pool convergence, wrong pool/interface refusal and in-flight source fencing.
Tests isolate source-version checks from generation checks and retain both claims
when source changes. Receipt ownership in lifecycle cases comes from guarded
writers; device proof/manager actors in ownership setup are explicit fixtures.
These are not installed/hardware attestations.

See [full affected race regressions](lan-link-delete-recovery-regression.log),
[runtime/RBAC checks](lan-link-delete-recovery-runtime.log),
[fresh native prerequisites](lan-link-delete-recovery-live.log), and
[CVK API readiness](lan-link-delete-recovery-cluster.log).

The lab still has two stable native adjacencies, no active sessions and no
recorded discovery history. The CVK API still returns EOF. No LAN link/device/
session/pool mutation was submitted to the appliance by this work.

## Broader lab prerequisite review

The additional [cross-model read-only checks](cross-model-read-only-prerequisites.log)
found two AP-profile metadata records through the existing public-field allowlist
reader; no credential fields were retained. Native fabric inventory had zero sites,
so bulk-anycast accepted execution was explicitly skipped and remains unqualified.
No profile, fabric or gateway was created. This helps prioritize wider validation;
it does not turn metadata reads or skips into resource execution parity.

## Remaining full-model work

Legacy/opaque port-history migration, ordered opposite actions on one port,
discovery execution, capacity dependencies, concurrent session/request-budget
qualification, wider configuration composition and installed/hardware validation
remain incomplete. The full resource ledger was rechecked; its other incomplete
resources remain part of the active objective. This milestone does not equate
metadata reports or native read prerequisites with execution parity.

The focused v2 public recovery/cycle suite passed in **15.047s**, versioned
source/declaration cases in **9.290s**, and complete affected race regressions in
**511.017s** for Catalyst Center (other unchanged affected packages passed from
cache). Runtime/RBAC checks passed in **3.771s** and fresh native LAN prerequisites
in **5.037s**. Cross-model read checks passed in **2.604s**, with bulk-anycast
execution explicitly skipped for missing fabric sites. Strict documentation and
whitespace checks pass.
