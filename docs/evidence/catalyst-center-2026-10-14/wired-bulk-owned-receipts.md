# Wired bulk owned receipts and convergence boundary

This extends the [grouping/sequence stage](wired-bulk-sequence.md) without
activating a public bulk controller capability.

## Owned selection

A stable nonsecret receipt key binds the logical root/method and complete
canonical native/physical/CiscoDevice/Node incarnation, excluding task ID,
operation nonce and transient baseline/mask evidence. The operation receipt
still retains all original resource/selection and group-digest bindings.
Re-provision is suppressed only by one correctly owned Verified receipt for the
exact configuration/controller/source revision with valid submission and
verification times. Foreign/duplicate ownership, duplicate or malformed receipt,
unverified/missing/future evidence and group-digest corruption are refused.
Older intent/controller/source revisions cannot suppress new requested work.
Native baseline drift within the same revision is refused rather than silently
replaying PUT over a changed object.

The adapter-owned sequence bridge verifies every batch against the original
parsed source, requires the existing inventory owner and an idle journal, then
runs complete native/managed sequence preflight before receipt filtering. Even
suppressed PUT batches are preflighted. A comparison-only canonical operation
uses an internal zero nonce; it is never returned/persisted/submitted. The shared
journal still generates the actual random operation ID.

Returned writes require an exact current Submitting group journal for the same
source, resource, action, root and complete target set, with a real submission
timestamp. Source/owner/journal checks bracket the supplied all-member authority
callback. Planning itself does not claim or dispatch; no standalone duplicate
selection wrapper remains.

## Final receipt construction and publication

Native task-tree and provisioned-record presence are execution evidence, not
rendered-device convergence. A separate convergence-proof callback is mandatory
before a final receipt can be constructed. Complete native/task/managed evidence
is observed before and after that callback. Missing/denied proof, changed native
records, replaced managed bindings or stale source do not advance Verifying.
The real proof provider is not yet implemented/qualified for this bulk runtime;
there is no fallback that treats record presence as final device attestation.

The private publisher binds every batch declaration to the current immutable
source and journal, then uses the existing shared atomic group receipt contract.
It authorizes the complete held/claimed member set before the one CAS that
advances Verified and retains its receipt. Prior receipts use the shared
predecessor-retirement contract; this helper neither releases members nor clears
the journal. Lost CAS response remains observable persisted evidence without a
native retry.

## Fixture verification

Selection tests cover current/absent/older-generation/controller/source receipts,
foreign/missing/duplicate ownership, duplicate receipt, wrong phase/timestamps,
digest corruption and same-revision native baseline drift. Source binding refuses
changed batch actions and undeclared selectors.

Native HTTPS and simulated Kubernetes publication fixtures cover success,
missing/denied convergence proof, native/source changes during proof, denied actor
authorization, revoked member during authorization, incomplete members,
undeclared batch and lost persistence response. Every case makes zero native
mutation requests. Failure keeps Verifying/no receipt; a lost status response is
retained as one atomic Verified operation/receipt. Publication alone creates no
member completion or manager settlement.

The successful fixture then supplies explicit simulated worker-release/manager
settlement markers and uses the real shared FinishVerifiedControllerGroup
contract to clear the settled journal. Before settlement, owned planning is
refused. After settlement, the current receipt suppresses PUT; a new declared
revision returns a new candidate, whose closure refuses to dispatch without an
exact Submitting journal. This is contract/fixture evidence, not installed actor,
actual lease release or real convergence qualification.

Affected wired/journal/group receipt/settlement/identity/precondition/template
race checks passed: Catalyst Center 7.241s; shared configuration 9.470s. Initial
publication/selection checks passed 3.314s. See
[affected regression](wired-bulk-owned-regression.log) and
[publication checks](wired-bulk-receipt-publication.log).

## Live negative qualification

Three actual Catalyst Center lab identities passed source-owned bulk planning
using simulated Kubernetes source/ownership/managed objects. No Submitting
journal was fabricated: the closure refused before reaching authority, could
not replay, and missing convergence proof refused receipt publication. Final
native identity/provisioned-record reads were unchanged. No claims, receipt or
native provisioning POST/PUT occurred. Test 10.13s; package 10.941s. See
[live refusal](wired-bulk-owned-live.log) and
[sanitized access](wired-bulk-owned-live-access.log).

## Remaining scope

Public importer/options and lifecycle routing, actual all-member preparation/
claims/dispatch/recovery, real convergence-proof implementation, installed actors/
lease qualification, predecessor retirement/replacement in a complete public
cycle, scope/embedded/wireless/deletion composition and an accepted isolated
live batch remain pending. The new callback and private bridge do not by
themselves close device attestation or authorize final release. No capability
flag was marked complete.

Final consolidated bulk receipt/ownership and shared receipt/settlement race checks passed: Catalyst Center 4.495s, shared configuration 9.516s. See [final output](wired-bulk-receipt-final.log). Strict documentation and whitespace checks passed.
