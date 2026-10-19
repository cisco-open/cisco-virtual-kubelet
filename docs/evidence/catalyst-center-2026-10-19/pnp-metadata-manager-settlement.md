# Original metadata outcome settlement and native cycle

The manager now validates a product-pure terminal outcome, reconstructs the
original pre-submission ledger and checks its sealed binding against original
fence worker facts. Only the exact acknowledged/verified PnP absence receipt or
original durable certified non-dispatch is accepted. Unknown, Submitting,
Prepared, wrong ownership/history and absence-only outcomes cannot settle.
Current model presence and a live original worker are not required for a valid
terminal outcome. The existing creator/tombstone ledger is retained.

An optional immutable settlementDigest on the existing handoff commits the
terminal configuration and original custody before any Lease mutation. Manager
actor admission confines its publication to terminal, ready, complete custody;
workers cannot publish it or change it. Release acknowledgement requires that
prior commitment. The initial commitment validates all original fences and
reconstructs the original worker binding. After that, interrupted settlement can
resume from the authenticated intent even if some original objects are gone.

Each retained original Lease is matched by canonical serial/name, original UID,
full binding and retained shape. The manager adds its exact release digest,
removes only the evidence finalizer and deletes with both UID and resourceVersion
preconditions. Lost marker/Delete/status acknowledgements return an error with
custody retained; fresh snapshots resume without replaying native work. Replaced
or altered objects remain held. Independent full absence precedes ReleasedAt;
retirement separately removes only settled custody. The existing worker finish
then clears its verified operation. No native API is called by settlement.

The controller registry carries an optional pure settlement projection. Manager
retirement invokes it without constructing a provider/client or loading current
model content. This path is independent of enabling new physical reservations.
In-flight metadata remains pending; terminal cleanup requires an explicit
compatible runtime callback. The production composition root supplies none.
ND and direct-device provider behavior remains unchanged.

## Controlled and real-API qualification

Controlled flows cover normal verified completion, certified refusal, missing
runtime, original/late UID replacement, foreign history, pruned intent readback,
lost intent/marker/Delete/release/retirement responses, worker disappearance and
missing current source. Original UID/RV delete preconditions are checked. No
worker may finish or plan another operation before manager retirement; terminal
creator/tombstone evidence prevents replay afterward. An altered protection policy
must be repaired before even certified non-dispatch can settle.

Actual isolated API workflows exercise verified deletion/tombstone, late native
refusal and unknown native response. Source schemas, original server UIDs,
manager intent/release/retirement, Lease admission and Pod-bound worker status
are real; appliance HTTP is controlled. Separate actor API tests reject worker
settlement and release without prior intent. Test impersonation has broad RBAC
to isolate admission. Worker readiness, background compiler status and remaining
deployed compatibility are explicitly staged. Manager route tests include
settlement after the current model source is deleted and disabled-runtime holds.

## Catalyst Center lab qualification

An opt-in private native run created two generated unclaimed PnP records. An
original pre-HTTP cohort refusal was durably certified with zero DELETEs, then
its exact fence custody was settled. The first run stopped during the test
harness's retirement because snapshot persistence had advanced resourceVersion.
No DELETE had occurred. The harness now re-reads authoritative state between
persistence and retirement.

Recovery resumed the exact saved released no-dispatch journal and the same two
native UUIDs without registering replacements. Both original records completed
claim, synchronous acknowledged DELETE, independent absence verification,
creator-bound tombstone, UID settlement, retirement and worker finish. The PnP
catalog returned to empty and the network inventory matched its recovery baseline.

A separate fresh run then qualified the corrected normal path: two registrations,
one original certified pre-HTTP refusal with zero DELETEs, settlement/retirement,
a fresh operation/cohort, two exact DELETEs and two verified tombstones. Original
creators remained intact. Two final reads verified the original empty PnP catalog
and unchanged network inventory. Only generated metadata records were mutated;
no claim/reset/device upgrade or other device mutation was permitted by the
qualification transport.

Kubernetes storage, actor/compatibility checks and worker readiness in these
native lab runs are simulated/JSON-persisted. They are private native execution
qualification, not an installed production-worker rollout. Real API actor/schema
qualification is the separate gate above. The live runner retains exact generated
ownership on failure and supports the specifically completed no-dispatch
settlement recovery; it is not general recovery from arbitrary unknown mutations.
[Results and source fingerprints](pnp-metadata-manager-settlement.json) record
the gates, native UUIDs, private recovery locations and preserved limitations.

Final shared API/manager/ND/direct-upgrade gates pass 3385 test/package events;
PnP/journal/SWIM regressions pass 1233, foundation API gates pass 81, manager
route checks pass 25, and all three typed real-API scenarios pass. Main build,
chart rendering, pinned generated schema and frozen-source/format checks pass.

## Remaining full-model work

Production manager/worker/enrollment/settlement compatibility and runtime wiring,
public new-prune routing, complete consumer protection and installed native
end-to-end qualification remain pending. Changed controller generation/domain,
CR/config deletion and missing-source finalization need their full lifecycle
handling; this terminal settlement does not authorize new native work or bypass
uncertainty. Foreign replacement fence keys remain held for investigation.
The wider pinned 93-resource execution and hardware gaps remain open. Full
NetAsCode model completion and merge readiness are not established by this slice.
