# Native repeated wireless tag lifecycle

Pinned model: NetAsCode Catalyst Center 0.5.0. Branch: `pr/johalley/catc-support`.

## Singleton qualification

`TestLiveWirelessTagRepeatedRecreationPublicRecovery` passed in **1,626.75 seconds**
(package result: **1,629.144 seconds**) against the lab appliance. The native
fixture comprised a generated disabled OPEN SSID, a generated wireless profile,
two empty areas, one site tag and one policy tag. No device was assigned,
provisioned or otherwise operated. Final profile/SSID/site baselines were restored.

The public reconciler exercised creation, ordinary same-ID updates, membership
expansion/reduction and **two complete tag deletion/recreation cycles**. Both
native tag identifiers remained stable across recreation, while authenticated
incarnation proofs changed. Four complete predecessor cohorts were retained.
After each stage the source converged with additional no-op reconciliations.

All **26 original native tasks** recovered through full task trees and readback,
with exact mutation counts and no replay. Unknown status and preflight refusals
were deliberately injected into the qualification's simulated Kubernetes state;
these are recovery tests, not 26 naturally occurring appliance failures. The
shared lifecycle retired 26 proven no-dispatch refusals. Final ordered pruning
removed tags before profile-site associations; generated definitions were then
cleaned up under identity and unrelated-inventory guards.

The [machine-readable record](wireless-tag-repeat-native.json) retains all
original task IDs, source fingerprints and the [terminal native log](wireless-tag-repeat-native-singleton.log).
The selected fixture/live sources exactly match the earlier
[local and real Kubernetes API qualification](../catalyst-center-2026-10-17/wireless-tag-repeat-recreation.md).
No production, transport, provider, CRD or shared foundation change was needed.

## Bulk policy cohort and limits

The first bulk run completed both recreation cycles, retained four predecessor
cohorts and recovered 28 original tasks, then exhausted its 45-minute qualification
context during a pre-cleanup reference read. The [failed gate](wireless-tag-repeat-native-cohort-failed.log)
remains failed. A subsequent [exact-ID supervised cleanup](wireless-tag-repeat-native-cohort-retained-cleanup.log)
removed nine generated objects/associations and preserved unrelated inventories;
that cleanup does not turn the original gate into a passing workflow.

A full retry is now in progress with the same assertions, empty-site constraints
and RF references, using a 75-minute explicit test budget. The opt-in override
ceiling is now 90 minutes; default and production task timeouts are unchanged.
Bulk repetition remains **unqualified until a terminal full-flow pass and cleanup**.

Native Kubernetes actors/storage were simulated by the harness. Earlier real API
tests qualify persistence separately; this run does not prove installed workers,
physical WLC/AP behavior or device provisioning. Larger/mixed scopes, ownership/
key/archive retirement and handoff, parent/config deletion and association
resurrection remain open, as do other gaps in the 93-resource ledger.


## Full cohort retry completed

The explicit 75-minute qualification retry passed against the frozen source recorded
in the retry case. Both cycles preserved ordinary updates and membership reductions,
recreated three tag members (including a two-policy-tag cohort), and retained four
complete predecessor cohorts. It recovered 33 original task trees and retired 33
proven preflight refusals without replay: 28 tasks across the repeated cycles plus
three ordered tag deletes and two strict association deletes. Final no-op, atomic
tombstones, generated profile/SSID/area cleanup and unrelated native baselines passed.

The original 45-minute attempt remains recorded as failed, together with its exact
supervised cleanup; it is not relabelled as passing. The retry changes only the
explicit test deadline, not assertions or default production timeouts. Kubernetes
actors/storage remain simulated, and no physical WLC/AP, device assignment,
provisioning or device operation is qualified. See `cases.cohortRetry` in the
[qualification record](wireless-tag-repeat-native.json) for source and task IDs.
