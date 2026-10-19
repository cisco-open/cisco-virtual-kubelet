# SSID credential known-task recovery and exact fixture cleanup

## Product recovery fix

Native qualification exposed a real gap: an SSID credential update with an
accepted task retained as `Unknown` stayed Waiting indefinitely. The generic
apply loop routed only Accepted tasks to observation, and the common completion
reader independently refused any non-Accepted phase, even though the existing
journal supports Unknown-to-Verifying without submission.

The adapter now supports that read-only task observation for fully bound global
SSID credential operations. `ssidKnownTaskRecoverySupported` scopes the shared
entry points to known task/submission time, valid Create/Update identity,
credential versions and complete digest-bound history. Physical/group/cancelled,
missing-task, pruned-history and other executor paths stay fenced. Public
reconciliation also checks current owner/controller/source generation and projected
version bindings before observation. Custom completion callbacks are not invoked
through this SSID-only extension.

The same correlated native task reader determines complete/pending/failure. The
original phase remains Unknown during observation; no Accepted phase is fabricated
and no submission closure is invoked. Only a complete task permits the existing
CAS transition to Verifying. Native UUID/metadata and the atomic credential receipt
must then verify before release. Failed/pending/foreign/stale tasks cannot create a
success receipt. A timed-out Accepted SSID task retains its task ID and can be
observed after entering Unknown; this is recovery of an existing effect, not a
new timeout/submission permit. Other provider/operation behavior is unchanged.

## Qualification and initial failed fixture

The initial native run reproduced the gap in 82.97 s, retaining disabled SSID
`CVK_PSK_5967c900b62c5914`, native ID
`mdi0q1zl-x1bt-s181-oty3-yZKWmgi2mMm1ote0qZbwmuSYxZnq`, with task
`01a14174-0677-7a0e-8026-3ce16867a645` in Unknown. It submitted one create and one
update. Projected passwords were temporary and were not reconstructed to resume
that failed test. The exact cleanup helper waited for the retained task, checked
source/owner/native identity and original baselines, then removed only the disabled,
unassociated fixture using delete task `01a14178-831b-7560-9b5a-b6a27ce66e9b`.
Cleanup passed in 2.95 s (package 4.896 s). Private artifacts:
`/private/tmp/cvk-ssid-manifest-1348129769/`.

## Fixed native uncertain-phase lifecycle

The corrected public-manifest cycle passed in **38.27 s** (package 40.291 s).
Each accepted update was deliberately written as Unknown with its original task,
then JSON journal restoration/rebuilt adapter reconciliation recovered it without
another mutation. Native API/tasks/projection files are real; Kubernetes persistence
and the uncertain-phase injection are simulated. This is not an actual lost-HTTP-
response or installed worker crash test.

| Stage | Desired active counters | Retained native task |
| --- | --- | --- |
| Rotation | main 1; priority 0=1, 4=2 | `01a14179-362b-79a1-9c5a-e35d290116b0` |
| Remove priority 4 | main 1; priority 0=1 | `01a14179-5346-771c-9656-7a0cd1e7c713` |
| Advanced return | main 1; priority 0=1, 4=3 | `01a14179-6df6-701b-ac8b-5de16466e743` |
| Explicit empty set | main 1 | `01a14179-84fb-76c2-98e2-53596a6c00c0` |

The run also checks list reorder without mutation and refusal of old counters and
stale projections. It recorded exactly **one POST, four PUTs and one DELETE**.
Fixture `CVK_PSK_7f42ef55a2f366bb`, ID
`mdi0q1zl-x1bt-s183-zJqY-zwy1nweYzJm2nMjIqZbwmuSYxZnq`, was removed with task
`01a14179-9c16-79f5-af3a-e08cc643add5`; original baselines were restored. Private
artifacts: `/private/tmp/cvk-ssid-manifest-4030639456/`. Four Accepted-stage checkpoints
retain original task/source/journal metadata. Independent artifact checks verified
all four history digests, expected active counters, exact final mutation counts,
completed cleanup, one receipt and no final operation.

## Bounded standalone cleanup recovery

The qualification helper now validates the exact generated fixture and each of
four expected credential/history transitions before cleanup. A matching known
pending update can explain temporary inventory absence at any retirement stage;
a missing/replaced task, target, owner, source, stage or history cannot.

Cleanup is still a test utility, not public product deletion/adoption/pruning.
After task success it polls selected metadata readback, repeats exact identity,
disabled/authentication state and profile-reference checks, and compares original
saved baselines. It creates an exclusive mode-600 local write-ahead file before
DELETE. A restart/concurrent caller cannot claim a second request. The accepted
delete task/time is saved before completion observation. A known recorded delete
resumes through reads only, with a transport guard rejecting all native mutations.
If the DELETE acknowledgement/task is lost, the marker holds for investigation;
it never grants replay. This private local process fence is not a cluster lease
or host-loss recovery protocol.

A second native scenario retained the acknowledged priority-4 removal (stage 2),
using `CVK_PSK_1389ce7762319d8f`, ID
`mdi0q1zl-x1bt-s18X-mZG5-y2u3nZyYmZe5zdHMqZbwmuSYxZnq`, with original update task
`01a1417b-ab74-7a7f-b8d5-1a5543995bdc`. Creation/rotation/retention passed in 19.11 s
(package 21.208 s). Exact cleanup passed in 3.04 s (package 5.066 s), delete task
`01a1417c-03a1-746d-a5df-9fc2d8aa6f8f`. Re-running the helper passed in 0.64 s
(package 2.427 s), observing that same delete task and baselines with **zero native
mutation attempts**. Private artifacts: `/private/tmp/cvk-ssid-manifest-2057989226/`.
Both this fixture and the earlier failed fixture are gone. All three artifact
directories/files were independently checked as 700/600; cleanup evidence is true.
Historical retained journals are test snapshots, not active lab cluster operations.
Standalone cleanup of stages 3/4 has fixture proof coverage, not native deletion
qualification at those stages.

## Regression and remaining scope

The focused SSID race suite passed in **4.425 s**. It includes public four-stage
Unknown-with-task reconciliation, missing-task fencing with unchanged mutation
counts and version receipt, known/pending/failed/foreign/stale task observation,
legacy receipt support, refusal to invoke unrelated/custom completion observers,
all bounded recovery stage proofs and malformed evidence. Eight concurrent cleanup
claims yield exactly one file winner; a restart cannot obtain another permit, and
an attempted delete without its acknowledged task remains blocked.

A separate public four-stage timeout regression passed in **3.305 s**: it
expires the retained Accepted submission time, lets the real apply loop enter
Unknown, then observes the retained completed task and reaches InSync without
additional mutations. This is a fixture timeout, not an induced native hour-long
task.

ND and provider race regressions passed from cache; controller upgrade/operation
regressions passed in **33.836 s**. The full Catalyst Center adapter race regression passed in **604.911 s**
on the production recovery fix. Additional canonical proof/observation/cleanup-claim
cases passed in **2.237 s**, and the later added timeout case passed separately
as described above. Strict documentation build passed in 4.99 s before this
final evidence update. No direct-device/gNOI/SWIM implementation or
shared operation schema changed in this step.

Known task IDs are required; lost/unclaimed task recovery is not inferred from
write-only metadata. Installed worker restart, real projected-volume rotation,
lab schema/actor rollout, actual WLC/AP/client authentication, further auth/cipher/
key combinations, full auth-mode transitions, adoption and pruning remain pending.
These changes do not establish full pinned NetAsCode model parity.
