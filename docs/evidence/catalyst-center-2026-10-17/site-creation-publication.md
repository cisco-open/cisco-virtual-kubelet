# Atomic site creator publication

The production creator publisher now stages a complete predecessor lifecycle
archive and the verified successor in one status update through CVK's existing
configuration action receipt helper. Native task-tree/result UUID and repeated
native desired-state verification still precede publication. The publication
builder changes a copy only, validates the complete successor chain, rejects
historical UUID/task reuse, and preserves every predecessor operation and time.
It cannot publish a partial archive. This does not enable public recreation.

Capacity admission includes the archive envelopes as well as the new creator,
under the existing 256-row / 256-KiB bounds. No historical cohort is evicted.
Post-write verification checks the entire configuration status, including owners,
historical and unrelated receipts, in addition to the new creator proof. A CAS
conflict is returned without an automatic status-write retry; a lost response
leaves the actual verified receipt available to existing completion recovery.
Incomplete history cannot settle through the existing creator-proof fence.

## Qualification scope

The publication tests use synthetic native proofs. They exercise production
publication and the shared status CAS, not native recreation or ownership
adoption. Fault injection covers conflict, lost response, dropped/edited archive,
dropped successor/operation, and unrelated-row loss. Two-cycle history fixtures
now use the production publication builder. Existing singular creation/recovery,
private baseline, retirement and floor-image cases remain in the regression gate.
The [final race regression](site-creation-publication-regression.log) passes
**61 families / 829 subcases** in **75.669 s**.
The [real temporary Kubernetes API gate](site-creation-publication-api.log) passes
**6 families / 89 scenarios** in **57.573 s**.
Exact source hashes and gate results are recorded in the
[validation audit](site-creation-publication-validation.json). Initial new-fixture
failures compared subsecond in-memory times with second-precision API values;
the corrected fixture uses the persisted operation and production-precision time.

## Remaining integration

Recreation planning must establish fresh native absence and surviving ancestry,
then bind guarded submission and original-task recovery. Public compiler/policy
routing, full consumer ownership protection, parent/child retirement coordination,
configuration deletion and installed native complete-cycle qualification remain
open. The isolated installed worker has not been updated with this change. No new
native mutation was performed for these tests. Full site lifecycle and pinned
93-resource parity remain incomplete.


## Subsequent staged execution integration

The next change connects native absence/ancestry planning, guarded singular
Create and original-task observation to this publisher. Its separate
[execution evidence](site-recreation-execution.md) does not enable public routing
or qualify an installed native cycle. The publication hashes and gates above
remain historical and unchanged.
