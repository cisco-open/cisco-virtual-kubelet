# Version-changing automatic cycle: blocked at preflight

Date: 2026-10-09 UTC. Target: `cat9k-lab-101` (`198.51.100.101`), IOS XE
17.18.04 → 26.02.01 through Catalyst Center.

**Result: not qualified.** Live Catalyst Center readiness checks were run, but
no CVK rollout was submitted and no distribution, activation, or reboot was
requested. `rollout-not-submitted.json` is a blocked candidate, not an execution
record. No runtime code was changed during this attempt.

## Live findings

- Native inspection confirmed 17.18.04 committed, `flash:packages.conf`, no
  application-hosting containers, and 3,281,833,984 free bytes on `.101`.
- The lab golden-image assignment at `Global/CVK/MUC07`, family `286315874`,
  role `ACCESS`, was temporarily changed to image
  `86159098-3ca1-47af-8456-ff2fc3e0d549` (26.02.01.0.263).
- Readiness task `01a11e8b-e021-778e-aa47-47c62168da0e` completed, but its
  individual results did not all pass. Parent task completion is not readiness
  success. See `preflight-26-results.json`.
- Startup configuration, configuration register, flash, and image applicability
  checks succeeded. The transfer warning matched the existing narrowly scoped
  HTTPS/SCP fallback. The additional xFSU compatibility warning reported that
  upgrading to 26.02.01 through xFSU requires a running version of 26.1.x or later.
  Current CVK validation blocks that warning even for the candidate `Reload`
  strategy. This is not evidence that an ordinary reload upgrade is unsupported.
- Capacity is a second, independent blocker under the current immutable policy.
  The target archive is 1,264,266,123 bytes. Subtracting this from current free
  space predicts 2,017,567,861 bytes remaining after distribution. The policy
  requires 2,936,012,800 bytes plus 134,217,728 bytes of headroom at both
  preparation stages: 3,070,230,528 bytes total, a projected shortfall of
  1,052,662,667 bytes. No eligible retired archive remains on `.101`.
- Catalyst Center's actual pre-distribution flash check passed: required
  2,651 MB, available 3,129 MB. **No post-distribution flash failure was observed**;
  distribution was not attempted. The capacity blocker above is a projection
  against the existing CVK policy.
- `.103` was inspected as an alternative: 17.18.03 committed, 17.18.02 inactive,
  2,504,589,312 bytes free, and overlapping retired/unretired preparation
  receipts protecting the remaining archive. It was not mutated. `.100` retained
  its two running workloads.

## Required work before retry

1. Qualify an explicit ordinary-reload activation contract. The
   [Cisco Update Images API](https://developer.cisco.com/docs/catalyst-center/update-images-on-the-network-device/)
   accepts `compatibleFeatures` with `ENABLE`/`DISABLE` values, but the documentation
   does not establish the precise xFSU key or omitted-feature behavior. Discover
   and test appliance-supported mode selection, bind it to the execution intent,
   and handle a mode-specific xFSU warning only with evidence that xFSU is disabled.
   Do not globally allow warnings.
2. Resolve the storage budget before distribution. Account for the target image
   while preserving the running baseline and protected files. Either qualify
   additional supported cleanup with explicit policy authorization and native
   verification, or prepare adequate lab capacity. Lowering the existing threshold
   or deleting protected content is not a qualified remedy.
3. Repeat through a submitted CVK rollout and collect automatic cleanup,
   inventory synchronization, readiness, distribution, activation, committed
   version, and maintenance recovery evidence. This attempt supplies none of the
   distribution/activation or version-change evidence required for that pass.

## Restoration and validation

Golden-image restoration task `01a11e8e-222a-75c8-b706-8a66bc3ec8b0` completed
without error. Readback confirmed 17.18.04 image
`058d4e65-d4d7-41a4-9f6e-ee5ac63c9276` as the sole SYSTEM golden image for
the lab scope and as the installed image. See `restore-task.json` and
`device-images-restored.json`.

At 02:46:01 UTC, `.101` was Ready, schedulable, untainted, and maintenance was
`Settled`. Both `.100` workloads were Running/Ready with zero restarts. See
`final-health.json`. No mutation leases or status fields were manually changed.

Focused race-enabled readiness regression tests passed:

```sh
go test -race ./internal/controlleradapter/catalystcenter \
  -run 'TestReadiness|TestTransferFallbackIsNarrowAndOptIn|TestModernSWIMDoesNotTrustParentTaskOrPartialReadiness' \
  -count=1
```

Output is in `readiness-tests.log`. This test pass does not establish live
end-to-end qualification. The earlier supervised version change and automatic
cleanup qualification remain separate evidence in the 2026-10-08 directory.
