# Floor/AP geometry: length, two axes, height and units

## Production correction

The protected floor predictor now reproduces Site Design's binary32
metre-to-feet multiplication before shortest-decimal readback. Its prior
binary64 conversion predicted an unchanged 12.654-metre width as 41.515747 feet;
the native observable width is 41.51575. That difference incorrectly predicted
an X effect during a length-only change, preventing the expected-effect root
from matching and fencing publication. Binary32 division by 0.3048 also fails
the observed width/height contract; the regression rejects that model.

The AP effect clone now rewrites only coordinates whose physical axes change.
Untouched X/Y and Z retain exact raw JSON representations, including trailing
zeroes, while modeled keyed comparison retains its existing numeric semantics.
Opaque fields/numbers, radio state and IDs remain exact. Neither requested
payloads, comparison tolerances, checkpoints nor observed baselines are rebased.
The existing journal, original task verification, owner/source guards,
publication and correction consumers are reused.

## Qualified execution

[Native cycle](planned-ap-floor-dimensions-native.log) passes in 304.71 seconds,
including exact AP and hierarchy cleanup. Starting with a new isolated private
site hierarchy and planned AP, the production planner executes:

1. A length-only change to 14 metres and a same-UUID AP correction. Measured
   predicted Y is 5.554454 feet; unchanged X stays exactly 5.5.
2. A simultaneous width/length change to 16/18 metres and AP correction.
3. A height-only change to 4 metres, with a new floor checkpoint and **no AP
   correction request**. Native AP coordinates and complete object stay unchanged.
4. A switch to feet with dimensions 60/70/12 and AP correction.

All four checkpoints preserve the original observed AP anchor. Thirteen
acknowledged tasks recover across simulated worker restarts without resubmission.
Eleven design writes retain seven bounded rows. Complete native AP state is
unchanged after every required correction; three final no-op passes issue no
writes. AP and hierarchy cleanup are independently verified.

[Focused regressions](planned-ap-floor-dimensions-focused.log) pass in 14.910
seconds, including literal measured conversion/coordinate expectations and raw
untouched-number preservation. [Nine real Kubernetes API cases](planned-ap-floor-dimensions-api.log)
pass in 34.094 seconds, retaining two-AP, source/opt-in drift, task uncertainty and
atomic publication checks. [Affected regressions](planned-ap-floor-dimensions-regression.log)
pass in 91.446 seconds. [Source fingerprints](planned-ap-floor-dimensions-sources.json)
record the final production and harness state; [strict docs build](planned-ap-floor-dimensions-docs.log).

## Diagnostics and limits

The [initial length diagnostic](planned-ap-floor-dimensions-diagnostic-length.log)
withheld publication after native effects differed from the prebound prediction.
The [division-model regression](planned-ap-floor-dimensions-diagnostic-division.log)
rejects the intermediate candidate; an [intermediate native run](planned-ap-floor-dimensions-diagnostic-intermediate.log)
failed before the dimension sequence and is not qualification evidence. Both
isolated diagnostic drawings/hierarchies were removed only after original
completed task trees, exact generated identities/coordinates and hierarchy
checks; [cleanup evidence](planned-ap-floor-dimensions-diagnostic-cleanups.log).
No failed operation's expected root was rewritten or replayed.

The live driver uses real Catalyst Center APIs/tasks and simulated Kubernetes
persistence/restarts. Real API-server persistence is qualified separately by the
API cases. No installed actor, physical AP/WLC deployment, arbitrary controller
version, replacement/adoption or public destructive lifecycle is claimed.
Earlier native two-AP cohort evidence predates this conversion correction; this
phase covers two-AP behavior through the real-API cases, not a new native cohort
run. Full pinned NetAsCode parity remains incomplete.
