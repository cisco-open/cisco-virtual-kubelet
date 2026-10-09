# Completed automatic SWIM qualification — 9 October 2026

**PASS:** the empty single-RP `.101` lab switch completed **17.18.04 → 26.02.01**
through Catalyst Center, including actual automatic retired-archive cleanup,
both inventory synchronizations, distribution, activation, native committed-image
verification, health soak and maintenance release. No intervention occurred after
approval; the rollout retained control revision zero. The earlier
[merge-readiness checkpoint](../merge-readiness/README.md) is historical.

## Runtime and support scope

- Branch `pr/johalley/catc-support`; PR #204 is stacked on ND PR #203.
- Qualified runtime: `4c6c529343c24e224ba7ae7203349c9e5e37c0ea`.
- Binary SHA-256: `80b2b050d75497b66acf24e9077936f6d472d826c94f6a645afdd63bf66a0f7a`.
- Lab image: `cvk-catc-handoff:20261009-merge-admission`; build metadata records
  `vcs.modified=false`. Later commits correct test fixtures and add evidence;
  they do not change the qualified runtime code.
- Device `cvk-live/cat9k-lab-101`, UID
  `20f3e68e-b127-4f2e-9a36-b66945135051`, serial `FOC2520L6H1`.
- Catalyst Center `3.2.3-75346.100`, controller UID
  `e4506c10-8a8f-40b2-89b7-4187940627fb`; explicit normal-reload profile.
- The target archive and extracted packages were already cached. The fixture's
  zero incremental distribution reserve is specific to that verified state.
  This is **not fresh-transfer or xFSU qualification**. The general example still
  reserves 2.8 GB for an incoming archive plus extraction, in addition to the
  unchanged 3,070,230,528-byte pre-activation floor.
- The lab binary overlay image is not the production image security artifact;
  CI separately builds and scans the production Dockerfile for both architectures.

## Uninterrupted campaign

The [approved manifest](unattended-manifest.json), [frozen plan](unattended-frozen.json),
[approval](unattended-approval.json) and [immutable policy](unattended-policy.json)
bind the device, controller, images and cleanup limits. The rollout was
`catc-merge-unattended-20261009-101`, UID
`c668b953-90bf-4ceb-82a3-a2dda381a5fa`; its frozen hash was
`sha256:013c1e20c007f28bcd1b29c5154d6356c30f2c8c850676ffeb6d1ad13b756461`.
Approval occurred at 16:53:26 UTC; final independent checks passed at 17:20:52 UTC.

| Stage | Recorded result |
| --- | --- |
| Pre-distribution preparation | Removed only `flash:gNOI_iosxe_17.18.02.0.4112.1766116039.bin`, 1,247,897,709 bytes, backed by invalidated receipt UID `fcd5a376-5524-41f5-a939-4de69696fee6` |
| Native free space | 1,859,096,576 → 3,108,220,928 bytes; exact size and SHA-256 checked before the claimed deletion |
| First inventory sync | `01a12199-943a-7825-95f5-e51d6a779495`, completed 16:59:22 UTC |
| Distribution | `01a1219d-32c2-7e21-97a6-a4757ad4ae16`, completed and correlated to the target device |
| Pre-activation preparation | Completed as a no-op; target archive/packages remained protected and the capacity floor still held |
| Second inventory sync | `01a1219e-e708-742a-a044-46c11ea0a8bc`, completed 17:05:10 UTC |
| Activation | `01a121a3-0d6f-7773-a24d-852741686d70`; Catalyst Center owned the upgrade and reboot |
| Final state | Rollout, upgrade and handoff Succeeded; native 26.02.01 committed; auto-abort timer inactive; maintenance Settled; mutation lease empty; Node Ready and schedulable |

See the [completed snapshot](unattended-completed.json),
[qualification result](qualification-result.json), [native checks](final-native.jsonl),
[final Node](final-node.json) and [timeline](timeline.jsonl).
The handoff is `swim-371d48cd10e41a516498a98682e14729`. Direct/gNOI was not an
upgrade fallback: the existing device worker performed constrained cleanup and
native verification while the Catalyst Center worker executed SWIM.
The optional CLI history query in the native capture was unsupported; it was
not used for the result. Version/install-summary checks and CVK's native
operational inventory establish the committed state.

## Setup and preceding recovery evidence

The [baseline reset](reset-completed.json) completed 26.02.01 → 17.18.04 through
Catalyst Center before the clean campaign. A supervised fixture copy then
restored the exact retired archive, checked its source SHA-256 and independently
verified its native SHA-512 and size; [before](fixture-setup-before.json) and
[after](fixture-setup-after.json) records distinguish setup from automatic cleanup.
The final binary/chart were installed and their worker revision and compiled
admission policy verified **before** creating the clean campaign; see
[qualified runtime](qualified-runtime.json) and [build metadata](runtime-build.txt).

An earlier [baseline campaign](baseline-completed.json) qualified a controller
worker restart after activation receipt persistence. [Before/after evidence](controller-restart.json)
shows different Pod UIDs but the same activation task and claim, with no duplicate
submission. [Pause](baseline-pause.json), [paused state](baseline-paused-state.json)
and [resume](baseline-resume.json) retain the ordinary control revisions.
The controller execution, handoff and maintenance implementation did not change
between that recovery test and the final runtime.

The subsequent [recovery campaign](recovery-completed.json) exposed a cached-target
assumption: redistribution reused XE's `Present` catalog instead of producing a
new `install add`. The corrected observer accepts only the quiescent retained
cat9k archive/package shape with matching native filesystem sizes. It protects
all target references and does not claim content authenticity or installation;
controller readiness and final native verification remain mandatory. That run
used the existing paused worker-replacement path before its second preparation,
then completed successfully. It is recovery evidence, not the uninterrupted run.

## Regression and CI

- Full uncached repository race suite passed on the cached-target runtime
  `6d01950f`; [log](runtime-race.log). Subsequent production changes were the
  type-safe admission expression and its matching compiled digest.
- Targeted lifecycle, provider, handoff and controller race suites passed.
  Added coverage includes cached-target protection, malformed/missing files,
  active/uncommitted install state, stale pause/resume grants and controller
  observation outages across executor restart without duplicate submissions.
- Full Kubernetes 1.35 native admission/default-scheduler suite passed with
  kubectl 1.35.0 and Helm 3.21.4; [log](native-admission.log).
- Handoff ownership envtest covers absent/present optional metadata and rejects
  controller takeover of journal metadata; [log](handoff-ownership-envtest.log).
- The complete shared-worker suite passed, including historical-manager rejection,
  incomplete-contract rejection, interrupted bootstrap, restart, lease binding,
  stale-token rejection, split worker permissions and native deletion;
  [log](shared-worker-admission.log). Its local `dirty` marker includes the
  untracked evidence draft; CI runs the committed review candidate separately.
- CI exposed stale policy counts, missing release labels, CEL type warnings and
  partial shared-worker fixtures. These were fixed without weakening admission
  or startup checks. Historical managers now fail at the earlier, exact SWIM-aware
  upgrade-contract digest rather than reaching the former CA-policy failure.
- [Candidate CI](https://github.com/cisco-open/cisco-virtual-kubelet/actions/runs/37962801491)
  had passed unit/envtest, security, Terraform, YANG, Helm, native TAS and both
  admission suites at capture time; production image steps were still running.
  The PR description records the subsequent final-review-commit CI outcome.

## Other nodes and remaining merge gates

The [post-run cluster check](other-node-health.json) records `.100`, `.103` and
`nexus9300v-live` Ready and `.100`'s two existing workloads Running/Ready. The
unrelated `cat9k-node` legacy entry has been Unknown since 31 January 2026 and
was not changed. ND adapter files are unchanged relative to the prerequisite.

Remaining repository gates are ND PR #203, retargeting and revalidation against
current `main`, required main-targeted lab checks, and independent review. This
record does not waive those gates or broaden the qualified platform/version scope.

Snapshots omit managed fields and replace transient handoff tokens with SHA-256
fingerprints. Controller restart evidence projects Pod identity, status and image
fields and contains no credential data. Source receipts remain in the lab under
`/tmp/cvk-swim-merge-qualification/final-unattended`.
