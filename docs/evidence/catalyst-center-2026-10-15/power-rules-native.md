# Native power rule and description qualification

Pinned NetAsCode Catalyst Center 0.5.0 power profiles now have additional native
logical-definition evidence through the existing compiled wireless executor.

The [fresh run](power-rules-live.log) passed in **16.941s**, body **14.45s**:

1. Created an absent random-named power definition with Ethernet SPEED 1000MBPS.
2. Changed the same UUID to RADIO / 5GHZ / SPATIALSTREAM / FOUR_BY_FOUR.
3. Changed it to USB / USB0 / STATE / DISABLE.
4. Selected all three rules in Ethernet/radio/USB order.
5. Reordered them to USB/radio/Ethernet and verified that exact order.
6. Updated its description with the same ordered rules.
7. Changed its rules while omitting description; verified the existing description
   remained intact.
8. Verified task completion, metadata, fresh-compiler no-op and unrelated public
   power/AP baselines throughout, then deleted only its learned UUID and verified
   baseline restoration.

No AP referenced the test profile by static setting or calendar at any step. No
assignment, provisioning or device operation was submitted. The tests refuse name
collisions, UUID replacement, unrelated public baseline changes and acquired AP
references. Uncertain results retain exact names/IDs; there is no blind cleanup.

## Native constraints discovered

The [first run](power-rules-initial-failure.log) attempted an empty description and
empty rules together. The appliance rejected that request HTTP 400. A second
[isolated attempt](power-rules-empty-rules-failure.log) retained a valid description
and confirmed empty rules also fail. [Sanitized diagnostics](power-rules-diagnostic.log)
identified separate native constraints: NCND86002 rejects an empty/whitespace-only
description; NCND86004 rejects null/empty rules. Both rejected requests preserved
the exact public power baseline.

The exact retained fixture subsequently completed the valid omitted-description
transition and [verified cleanup](power-rules-retained-recovery.log) before the
fresh run. This evidence does not claim support for clearing descriptions or
emptying rules. It distinguishes omitted optional description (retained) from an
explicit invalid blank value.

Other radio bands, ports, speeds/spatial stream counts, rule combinations, actual
AP power/radio/USB behavior, pruning and installed actor qualification remain
separate gaps in the [ledger](../../controllers/catalyst-center-resource-parity.md).

## Request validation and metadata preservation

CVK now rejects both native-invalid desired forms during model compilation and
validates every power declaration before selecting a first native plan. An invalid
later definition cannot release an earlier mutation or prospective prerequisite.
Observed metadata remains readable independently of these request constraints.
When valid rule changes omit description, a nonblank observed description is
preserved in the update; a blank observed value is omitted from the payload so it
is not resubmitted as a native-invalid explicit string. Unknown fields and other
controller/provider foundations were not changed.

Fixtures cover the native constraints at parser/batch/composition boundaries,
zero native requests for invalid later intents, and omission of unconfigured
observed descriptions. Existing AP dependency fixtures now use native-valid rules.

## Final-state checks

After the request/observation fixes, the [fresh native cycle](power-rules-final-live.log)
passed in **18.640s** (body **16.76s**) with exact cleanup and baseline restoration.
The [all-wireless/configuration race fixtures](wireless-final-focused.log) passed
in **9.411s**. Final [manager/worker checks](wireless-final-runtime.log) passed in
**4.091s**. These are the final-state targeted regressions; the preceding full
[affected suite](wireless-profile-regression.log) passed before this separate power
change. No fresh live SWIM, ND or direct-device upgrade is claimed by these runs.
