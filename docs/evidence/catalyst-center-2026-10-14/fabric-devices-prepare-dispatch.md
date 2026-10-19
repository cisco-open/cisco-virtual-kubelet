# Source-bound bulk fabric-device preparation and shared dispatch

This extends the [managed footprint](fabric-devices-managed-footprint.md) with
private source-bound preparation and dispatch through CVK's existing shared
group contracts. It does not activate public bulk fabric-device submission.

## Preparation

Every incoming intent must exactly match one declaration in the complete parsed
source. Duplicate or undeclared members and changed roles are rejected. The
installed group runtime, fabric-only Apply scope, unchanged source/config and
idle controller journal are required. Complete native/managed preflight precedes
the existing shared preparation CAS, which records ownership, fresh operation
nonce and all incoming/peer/parent identities together. Re-reading the controller
detects schema pruning or a competing journal update.

Preparation supplies an unusable native guard and creates no reservation, claim,
lease or native request. A no-op remains journal-free. Restart cannot replace a
retained Prepared identity. Foreign section ownership, missing peer bindings,
stale source or missing runtime prevents preparation.

## Shared dispatch

The bridge requires the non-reconstructible GroupSubmissionPermit. It revalidates
the exact source, owner, native resource and complete immutable managed targets
before crossing the shared Prepared-to-Submitting CAS. The original Submitting
operation and full footprint are checked around the shared live claim/lease
permit check. The native planner's one-use closure then submits one request.

Task acknowledgement uses the shared receipt CAS. Lost native acknowledgement
retains Submitting. Revocation after native acceptance refuses durable recording
but returns the known task with the error; no implicit retry or release follows.
Partial native membership is acceptance evidence only, never completion or
source-bound device convergence. Both accepted and uncertain paths retain the
entire incoming-plus-peer claim set.

## Verification

Eight preparation cases cover complete/no-op, changed source, foreign ownership,
undeclared member, changed role, missing peer and missing runtime. Eight shared
dispatch cases cover acceptance, partial membership, lost response, denial,
missing permit, stale source, replaced peer and post-acceptance revocation.
The existing native/managed cases remain in the same reusable fixture.

Final affected race checks passed: Catalyst Center 11.820s and shared
configuration 10.466s. See
[preparation/dispatch and shared regression](fabric-devices-prepare-dispatch-tests.log).
These use native HTTPS and simulated Kubernetes claims/leases/worker snapshots;
they do not qualify installed actors or real device convergence.
Strict documentation build and `git diff --check` passed. The repeated
authenticated prerequisite check found zero fabric sites and explicitly skipped
accepted execution (0.35s; package 1.247s). See
[native fabric prerequisites](fabric-devices-prepare-dispatch-live-prerequisites.log)
and [sanitized access](fabric-devices-prepare-dispatch-live-access.log).

## Remaining scope

Public grouping/chunk/option routing, whole-model composition, real manager/device
admission, acknowledged recovery, independent source-bound device proof, final
receipt/completion/settlement/retirement and isolated accepted live mutation
remain pending. The lab prerequisite check is read-only; no fabric or device
membership is created to manufacture qualification. No resource/full-section
completion flag changed.
