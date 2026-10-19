# Planned-AP floor effect calculation and handoff contract

## Current implementation boundary

The new `plannedAPFloorEffect` calculation produces an independent complete
expected AP observation for a horizontal floor resize. It starts with the
same-response typed/native AP object and the exact old/new floor identity.
Physical floor dimensions are converted through the existing feet/meters
geometry helpers. The model scales X with width and Y with length; Z and every
other native field are preserved. Unchanged physical axes retain their original
coordinate representation. Changed horizontal coordinates use a precise
binary32 shortest-decimal projection, reflecting the lab observation described
below. This conversion is limited to expected floor effects; existing private
baselines and generic native comparisons are not rounded or compared with an
epsilon.

The calculation rejects changed floor UUID/path/parent, invalid dimensions or
units, incomplete coordinates, typed/raw divergence, invalid initial positions,
overflow and resulting positions outside the new physical extent. The private
native clone uses the existing exact-number decoder, keeping unknown nested
fields, null/absence, radio ordering and identities. The resulting typed object
is decoded independently; it must not alias the original observation.

This function is an expected-state calculation for the pending handoff, **not an
enabled public geometry execution path**. Protected floor Updates remain fenced.
No post-task native observation is adopted as a new private baseline by this
change, and the existing journal, transport, ND, direct-device and SWIM paths
are unchanged.

## Native contract check

The isolated private-design qualification now supports
`CATC_AP_GEOMETRY_CONTRACT=isolated-floor-scaling`. A resource-specific observer
samples native AP state after the original floor operation reaches Verified and
before the following AP correction can run. It compares the complete predicted
and observed canonical AP objects, not just selected coordinates. Only synthetic
fixture coordinates and a comparison outcome are logged; native private bodies
and keyed fingerprints remain in memory.

The [documented AP response](https://developer.cisco.com/docs/catalyst-center/2-3-7-9/get-planned-access-points-positions/)
defines coordinates in feet but does not specify binary precision. The
[floor update request](https://developer.cisco.com/docs/catalyst-center/2-3-7-6/updates-a-floor/)
contains dimensions and units, without a documented switch for disabling AP
rescaling. Native observations, rather than those schemas alone, must establish
the exact expected effect on the deployed controller.

This observer does not publish AP ownership or baseline evidence. Existing
manifest reconciliation still performs the subsequent AP correction, validates
the original AP/radio state, observes stable no-ops and cleans up the generated
AP and hierarchy. A disproved prediction is reported only after the original
state has been restored and safe cleanup has been established; uncertain native
outcomes retain their existing fence.

The [initial native run](planned-ap-floor-effect-native-initial.log) disproved
an exact float64 calculation: X changed from the synthetic fixture value `5.5`
to `6.0850325`, while float64 predicted `6.085032307016012`. The observed decimal
matches binary32 rounding with a shortest decimal representation. This is an
inference from native behavior, not a precision guarantee in the API schema.
The initial run completed the exact AP correction and verified AP/hierarchy
cleanup before failing the contract check; no new fixture remained.

The [31 focused cases](planned-ap-floor-effect-focused.log) pass in 2.391
seconds with the race detector. They cover horizontal expansion/shrink, unchanged
axes, height/bounds, units, origin, fractional results, opaque native preservation,
typed/raw divergence, independent pointer ownership, invalid scope/dimensions
and binary32 formatting/overflow. These are calculation fixtures, not evidence
that every modeled dimension or unit transition is native-qualified. The tests
first exposed and then verified the fix for pointer reuse during result decoding.

The [corrected native run](planned-ap-floor-effect-native.log) passes in 166.45
seconds including exact cleanup. The predicted and observed complete AP objects
match before correction, with X=`6.0850325`, Y=`6`, Z=`4`. Original AP/radio UUIDs,
MAC and every other native field remain unchanged. The existing public manifest
flow then restores X=`5.5`, verifies the full original AP object and observes
three stable no-op passes. Six design writes and eight acknowledged task
recoveries are counted, with six bounded status rows. Exact generated AP and
hierarchy cleanup are verified.

This qualifies the tested single-AP width resize on this lab controller.
Length/height/unit transitions, multiple APs, other versions and boundary
rounding remain native qualification gaps. Native HTTP/tasks are real;
Kubernetes persistence, projected keys and restarted workers are simulated.
No physical AP/WLC provisioning, installed actors or protected keyed AP geometry
handoff is claimed.

The [final AP/site/image regression suite](planned-ap-floor-effect-regression.log)
passes with the race detector in 75.471 seconds, including existing retained,
keyed-state, type-update, task and one-use-dispatch cases. [Source fingerprints](planned-ap-floor-effect-sources.json)
identify the calculation and qualification code; historical source fingerprints
remain unchanged. [Strict documentation validation](planned-ap-floor-effect-docs.log)
accompanies this phase.

## Required publication work

The retained-state preflight from the [preceding phase](planned-ap-retained-boundaries.md)
is necessary but insufficient to enable protected geometry. The remaining
handoff must satisfy all of these requirements:

1. Freeze every retained AP on the target floor, including APs omitted from
   current source. Reject unowned native members rather than mutate their
   coordinates as an untracked side effect.
2. Bind expected complete AP effects and exact current authority/predecessor
   receipts before dispatch, using the existing projected key. Keep raw native
   data private and bound metadata count/bytes before beginning the operation.
3. Retain one real floor operation and one primary floor receipt. Do not invent
   AP operation IDs or native AP tasks for changes caused by the floor task.
4. Verify the complete original task tree and floor result UUID, desired floor
   state, exact AP membership/identities and repeated complete AP observations.
   A mismatched expected result must stay fenced without replay or silent rebase.
5. Atomically publish bounded related-state evidence with the floor receipt.
   Missing/pruned metadata must be detected through the bound producer/result
   proof, including on real Kubernetes API round trips.
6. Allow each AP correction to consume only its applicable floor checkpoint or
   subsequent AP latest receipt. Multiple AP corrections, later floor edits,
   APs retained outside source and new AP creates must not require timestamp
   guessing or retain unbounded operation history.
7. Repeat source/key/owner/receipt guards around dispatch and publication;
   recover only original acknowledged tasks and preserve one-use no-dispatch
   retirement. Qualify multiple-AP and restart paths in the lab before claiming
   the geometry handoff complete.

Full pinned NetAsCode parity, radio replacement, public destruction and
installed-worker qualification remain incomplete.
