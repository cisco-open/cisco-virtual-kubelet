# AP-profile dispatch and recovery qualification

NetAsCode **0.5.0**, revision `a435555fe828f03cf3311d52fe1ab6572a247916`,
branch `pr/johalley/catc-support`. Full model and AP-profile parity remain
**incomplete**.

## Closed execution gaps

AP-profile plans now reuse the existing one-use wireless definition guard to
repeat their selected typed public native observation before mutation. A changed,
missing, replaced or newly appeared target refuses the stale submission. The
explicit desired metadata and credential version selectors are also frozen;
projected credential values are never serialized into this guard's intent.
Referenced static and calendar power profiles, including native references omitted
from the source but preserved in the effective payload, retain their typed identities
and content. Planned prerequisites may be absent during whole-model planning,
but must exist at actual AP dispatch. An absent/replaced/changed dependency
requires a fresh plan rather than permitting the old body to overwrite state.
Unrelated AP or power definitions do not enter the selected dependency snapshot.

Metadata-only AP writes reuse the existing private no-dispatch proof and journal
CAS: a certified preflight refusal retires only that unsubmitted operation,
retains the section owner and requeues for fresh observation. A lost response
without that proof stays fenced; a stale closure cannot be reused.
Credential-bearing APs gain the native guard but retain their existing version,
receipt and recovery gates; they do not gain automatic no-dispatch retirement.

Declared metadata-only AP Create/Update operations now join the existing
known-task observation selector. Unknown acknowledged tasks are observed without
resubmission, with the complete rooted task tree. Accepted Updates also use that
complete-tree observer. Resource membership is exact and remains ungrouped for
APs even when prerequisite definitions are grouped. Credential-bearing,
taskless, foreign-source/target and physical envelopes do not acquire this path.
Normal accepted Create completion retains its existing contract; this change
creates no per-object creator receipt or ownership/adoption authority.

## Verified behavior

- Race tests cover concurrent preserved-field/identity changes, absent/new targets,
  desired/version-selector changes, unrelated objects, changed/absent/unavailable
  power dependencies, planned-but-absent prerequisites, one-use closures and lost
  acknowledgements. Public replan and original-task tree failure/pending/foreign
  and source/target fencing tests pass.
- A real temporary Kubernetes API server passes dispatch/recovery and existing
  metadata calendar/SSID cases, including eleven focused persistence, no-dispatch,
  acknowledged-task and refusal cases.
- A generated unassigned native AP profile completes one Create and one same-UUID
  Update. Both original acknowledged tasks recover from Unknown. A forced native
  read failure while Submitting retires the exact unsubmitted Update with zero
  mutation and retains the owner; fresh reconciliation then submits once.
  A previously captured stale Update closure refuses and is consumed.
- Exact generated UUID cleanup completes once and the complete typed AP-profile
  inventory baseline is restored. There are exactly three native mutation
  attempts: Create, Update and Delete. No association, AP provisioning or device
  action is submitted. Native Kubernetes persistence is simulated, not installed
  qualification.

A separate compiled-executor native run creates isolated power/AP definitions and
verifies DAILY, WEEKLY, MONTHLY, multiple, reordered and cleared schedules under
the same final guard. Exact cleanup restores AP/power/profile baselines; no
assignment, provisioning or device action is submitted. This is not installed
calendar-worker qualification.

The final broad regression passes **19,528 test events across 16 packages**,
including ND, shared configuration/managed protocol, maintenance and direct-device/
SWIM upgrade paths. Production build and strict documentation checks pass.

Gate counts, original native task IDs, fingerprints and log hashes are in
[the machine-readable evidence](ap-profile-dispatch.json).

## Fixture corrections and remaining limits

An initial absence fixture returned null rather than a collection. It now returns
an empty array. The older AP calendar fixture did not answer task-tree reads;
it now uses the same complete-tree task fixture as other guarded definitions.
These corrections preserve the stronger observer rather than adding a fallback.

AP creator ownership, source/config deletion, adoption, installed calendar and
credential/device behavior, credential-specific no-dispatch retirement and
complete native schema/catalogue coverage remain pending. No report or successful
metadata task is counted as those capabilities.

Read-only inspection of the completed Create and Update task records and trees
found no captured AP-profile ID in either response. Their progress is plain text,
not a structured identity result. This is evidence against treating successful
metadata completion as creator custody; authoritative creation correlation must
be established before adding AP creator receipts or destruction authority.

A subsequent [ordinary Accepted Create qualification](wireless-accepted-create.md)
closes the normal-Create root-only completion exception recorded above. Full
task-tree success still grants no creator UUID ownership or deletion authority.
