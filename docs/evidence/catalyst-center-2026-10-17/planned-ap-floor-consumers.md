# AP corrections consume verified floor checkpoints

## Execution and authority

The private site/design compiler now loads per-AP authority from the existing
strict floor-related receipt decoder. Complete native AP baseline checks,
retained AP observations, correction planning, dispatch and publication all use
that authority. No additional transport, task protocol, journal or API field is
introduced in this phase.

The first correction after a floor checkpoint binds a digest of that exact floor
receipt and AP effect. Its `planned-ap-floor-update:v1:` producer also carries
the floor operation ID and expected complete AP result MAC. Subsequent corrections
under the same floor operation bind their own AP latest receipt. A newer floor
checkpoint takes precedence over an older AP latest row by explicit operation
identity, including when timestamps tie. Times still validate chronology; they
do not decide which baseline wins.

Each AP advances independently. Correcting one AP leaves other members' floor
checkpoints available, and one bounded AP latest row replaces its predecessor.
Original AP observations and the floor receipt remain unchanged. An AP created
after a checkpoint can use its own observation until a later floor checkpoint
includes it; this fallback cannot downgrade an AP that already has a floor-bound
correction when its floor proof is lost.

Missing/malformed floor evidence, stale predecessors, mismatched floor operation
IDs, key/source-mode loss and late checkpoint replacement refuse before dispatch
or settlement. The existing source/owner/journal guard now compares current floor
authority as well as AP observations/latest rows. Current AP corrections cannot
settle using a historical floor operation. Original acknowledged-task recovery
and complete expected-result MAC verification remain in the existing AP path.
Unrelated native changes after submission stay fenced without publication or
replay.

Floor receipt decoding canonicalizes effect-set order, matching the previously
introduced set-root and predecessor-digest contract. Reordering API set members
does not alter authority. The compiler still requires explicit private AP opt-in
and the existing projected site key for these producers.

The expected AP result builder also fixes shallow typed-state aliasing: decoding
the predicted result now starts with an independent typed observation. Computing
an update no longer mutates the original AP position/radio pointers or native
baseline. Full native data remains in memory; only existing public identities
and keyed fingerprints enter receipts.

## Qualification and limits

- [Final regression evidence](planned-ap-floor-consumer-regression.log) passes
  with the race detector in 80.682 seconds. It includes existing site/AP/image,
  receipt and SWIM tests plus nine authority-selection cases, separate two-AP
  member/order and immutable-expected-state checks, and five public consumer
  manifest cases.
- The positive public cases correct an AP, update it again under the same floor
  checkpoint, then correct it under a newer checkpoint. Three AP Update requests
  to the simulated product produce three original task completions. One case
  explicitly recovers the original create and all three correction tasks through
  Unknown, with no resubmission. Three final no-ops preserve four total status
  rows: original floor/AP observations and one floor/AP latest row each.
- Negative public cases change native state before dispatch, remove the floor
  proof, or introduce unrelated native state after the correction request.
  Preflight cases perform no mutation; post-task drift retains Verifying, emits
  no AP latest receipt and does not replay. An initial test harness retained an
  obsolete AP slice between stages; the corrected fixture edits current native
  state and all final cases pass without relaxing production checks.
- [Final real Kubernetes API qualification](planned-ap-floor-consumer-api.log)
  passes in 74.060 seconds: five consumer cases, twelve existing keyed baseline
  cases, nine existing type-update cases, twelve floor receipt cases and the
  standalone actual old-schema pruning test (39 cases total). Kubernetes 1.35.0
  and repository CRDs are real; product HTTP is simulated.
- [Shared controller, ND, lifecycle and direct-upgrade checks](planned-ap-floor-consumer-foundations.log)
  pass, partly cached. [Eleven source/test fingerprints](planned-ap-floor-consumer-sources.json)
  identify this phase. The 93-resource ledger scope remains unchanged.
- [Strict documentation validation](planned-ap-floor-consumer-docs.log)
  accompanies this phase.

Consumer fixtures seed a synthetic already-verified floor checkpoint after
simulating its coordinate effect. They test real consumer execution and API
persistence, **not** the disabled floor writer or native floor checkpoint
publication. No new lab/hardware qualification is claimed here. The earlier
[native width-effect calculation](planned-ap-floor-effect.md) remains the lab
evidence for that effect; a complete protected native cycle is still pending.

## Remaining handoff work

The protected floor planner still refuses before mutation. It must next derive
complete expected effects for all retained APs, reject unowned native members,
bind exact predecessor authority, reserve receipt count/bytes, and verify the
original floor task and repeated full native AP results before atomically
publishing the checkpoint. This publisher must then be qualified together with
these consumers through multiple APs, restarts and later geometry edits in the
lab. No synthetic AP operations or manually adopted native baseline should be
used to claim that integrated flow.

Length/height/unit/version and boundary native behavior, public destructive
lifecycle, installed actors and full pinned NetAsCode model parity remain open.
New AP creation after an existing checkpoint also needs dedicated integration
qualification; the current member test validates independent baseline selection.

The subsequent [original-task handoff phase](planned-ap-floor-handoff.md) connects
the protected publisher to these consumers and qualifies two native resizes and
corrections under explicit opt-in. The publisher gap above describes this phase's
historical boundary; the newer evidence records its qualified implementation.
