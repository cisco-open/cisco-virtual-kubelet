# PnP terminal settlement across controller edits

Completed PnP metadata cleanup can now settle its original custody after an
ordinary controller specification/generation edit. New settlement intents bind
the original controller UID, sealed operation generation, worker/fence/owner
history and acknowledged terminal outcome. They exclude mutable desired spec and
current generation. The internal digest domain is controller-metadata-settlement-v2;
no new API field, reservation, journal, native transport or controller family was
introduced.

The manager also preserves the original metadata worker and its bootstrap/access
objects between custody retirement and Verified journal finishing, including
pause/spec edits. This uses the existing pure metadata-scope selector; the central
manager does not import the product adapter. It closes a quiesce/rollout race at
that final boundary and grants no new dispatch. Original-worker restart or
replacement during earlier custody still needs installed qualification.

Settlement reconstructs the sealed pre-submission ledger at its original
generation solely to compare its binding digest. Every retained Lease must still
match that original operation, controller UID, creator/source identity, worker and
Lease UID. Current observations and status writes remain bound to the current
controller snapshot. An intervening edit interrupts that attempt; recovery uses a
fresh snapshot and the retained intent. Remaining original Leases can then be
released with the existing UID/resourceVersion preconditions. Replacement fences,
changed ownership/history, Unknown operations and unverifiable outcomes remain held.

This is terminal recovery. New planning, sealing and dispatch claims still require
current source/generation, worker, complete cohort and live runtime authority.
An unpaused controller credential-reference/generation edit is explicitly tested
to deny a new claim. Historical settlement never contacts the native appliance
or makes edited desired intent InSync. Controller type and endpoint remain
immutable under the existing API contract; changing controller domains requires
a new NetworkController.

## Existing intents and admission contract

An already persisted v1 settlement digest is accepted only when its original
current-spec digest can still be corroborated. It is never rewritten or inferred
from native absence. A legacy intent whose bound spec/generation has already
changed remains held; automatic legacy-intent migration is not implemented.

The real API test uncovered an existing policy error: a non-status UPDATE can
omit request.subResource entirely. The metadata handoff policy now checks presence
before reading it, then uses that guarded status predicate. This allows ordinary
spec edits while preserving custody and status actor confinement. The compiled
metadata policy fingerprint was updated to match the rendered chart exactly.
Matching chart/binary contracts must roll out together; an old/mismatched contract
is refused. Bootstrap fence policy, physical reservation protocol, ND and SWIM
contracts were not changed by this slice.

## Qualification

Controlled full cleanup cycles test edits before settlement, after the terminal
marker, after an original Lease delete and after release acknowledgement. Injected
edits must actually commit and advance generation; a denied edit cannot satisfy
these tests. Lost acknowledgements, lease replacements, missing runtime authority,
foreign history, missing source and missing original worker retain their existing
coverage. Digest tests cover stable desired edits/release acknowledgements and
refusal of changed original worker, operation, controller UID and owner history,
including corroborated versus unverifiable legacy intents. Final manager/CLI
regression tests separately cover the post-retirement worker hold with normal,
paused and invalid desired specs.

Kubernetes 1.35 API-server tests exercise the same edit/recovery windows with real
CRDs and actor-separated CEL admission. Actual generation changes, immutable
handoff retention and exact lease cleanup pass. Separate actor-admission tests
verify the corrected rendered contract. Worker readiness and runtime compatibility
facts in these tests are staged; no kubelet runs here.

The native Catalyst Center qualification creates a fresh pair of generated,
unclaimed PnP records, removes only their durably verified creator UUIDs, and
retains original creators plus absence tombstones. After the second Delete is
Verified, it pauses the simulated controller and advances generation from 1 to 2
before settlement. Original-epoch settlement and source-independent journal
finishing succeed without another native request. Both PnP and managed-device
inventory baselines are independently restored. Kubernetes storage/runtime
authority in this native harness are simulated, so this is not installed-process
qualification or a test of new intent convergence after an edit.

[Machine-readable results](pnp-terminal-epoch-settlement.json) contain final gates,
source/chart fingerprints, native UUIDs and the private completed recovery folder.
Those generated IDs were deleted; do not replay the completed qualification.

## Remaining gaps

Prepared metadata cancellation, uncertain native outcomes, unready/partial-custody
retirement, in-flight generation transitions and deletion-policy cleanup are not
implemented by this change. Automatic legacy settlement migration after a bound
spec edit also remains unavailable. Docker's API still times out; the existing
isolated cluster could not be inspected or upgraded, and Docker was not restarted.
Full pinned 93-resource execution/hardware parity and merge readiness remain
incomplete.
