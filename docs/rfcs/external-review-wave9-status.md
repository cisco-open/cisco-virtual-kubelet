# External review - Wave 9 status

**Branch:** `pr/johalley/ciscoconfig_xe`
**Date:** 2026-04-26
**Reviewer:** Codex
**Audience:** Claude / implementation follow-up
**Scope:** current status after Wave 9 remediation of [`external-review-wave8-followup.md`](external-review-wave8-followup.md)

## Bottom line

The Wave 9 remediation has been reviewed. I found **no new blocking findings** in the Wave 9 code path.

The two Wave 8 follow-up findings are now closed in code:

1. **W8FU-1 - `LeaseBlocked` missing from IOSXEConfig status enum**
   - Fixed by `0390b99`.
   - `LeaseBlocked` is now present in:
     - `api/config/v1alpha1/iosxeconfig_types.go`
     - `config/crd/config.cisco.vk_iosxeconfigs.yaml`
     - `charts/cisco-virtual-kubelet/crds/config.cisco.vk_iosxeconfigs.yaml`
   - New schema-aware test `internal/provider/iosxeconfig_phase_enum_test.go` parses the generated CRD and verifies status-bound engine phases are enumerated.

2. **W8FU-2 - `LeaseBlocked` requeue read stale pre-update status**
   - Fixed by `e3be657`.
   - `reconcileOne` now returns `(engine.Result, error)`.
   - Controller-runtime `Reconcile` uses `result.Phase` for:
     - `requeueIntervalFor(&cr, result.Phase)`
     - OTel span phase attribution
   - This avoids reading stale `cr.Status.Phase` after `recordResult` writes status through a deep copy.
   - The headline test `TestReconcile_AllBlockedReturnsSubTTLRequeueAndLeaseBlockedPhase` now covers the all-blocked path in one tick: foreign lease, `LeaseBlocked` phase, sub-TTL requeue, unchanged `LastDeviceCheck`, and no engine/device call.

## Review verification

Commands run during the Wave 9 review:

- `GOCACHE=/tmp/cvk-gocache go test ./...`
- `GOCACHE=/tmp/cvk-gocache go test -race -count=5 ./...`
- Hot-package race pass across:
  - `internal/drivers`
  - IOS-XE engine/writers/transport
  - `internal/aggregator`
  - `internal/controller`
  - `internal/provider`
- `go vet ./...`
- `helm lint charts/cisco-virtual-kubelet`
- `diff -u config/crd/config.cisco.vk_iosxeconfigs.yaml charts/cisco-virtual-kubelet/crds/config.cisco.vk_iosxeconfigs.yaml`

All completed cleanly.

The Git worktree was clean after the review. At the time of review the branch was **48 commits ahead of origin** and had not been pushed.

## Current assessment

The prior blocking review chain is closed from a code-review perspective. The branch can reasonably retain the current `latest-update.md` / `implementation-status.md` claim that it is shippable for day-0 and day-2 under the per-pod topology, with the aggregator topology exclusive-and-correct.

This is still subject to the live/operator-scheduled validation already listed in `latest-update.md`.

## Remaining non-blocking actions

These are recommended before release tagging or external announcement, but I do not consider them blockers to the Wave 9 closure.

1. **Live apiserver validation**
   - Confirm `status.phase=LeaseBlocked` is accepted against a real apiserver.
   - Confirm Lease creation succeeds for an underscore family such as `interface_ethernet`.

2. **Live device/write-path retests**
   - NETCONF transactional structured-only apply.
   - NETCONF transactional + CLI rejection path.
   - configPrereqs deletion cleanup.
   - gNMI Set against `interface_ethernet[GigabitEthernet=0/0/0]`.
   - CiscoDevice credential Secret rotation with overlap, confirming transient `LeaseBlocked` and clean lease takeover.

3. **envtest infrastructure**
   - Add envtest when practical to cover recurring fake-client blind spots:
     - CRD field validation / `MinItems`
     - object-name admission
     - status enum admission
   - The current CRD-parse tests are useful near-term guards, but envtest is the durable closure.

4. **Documentation cleanup**
   - `implementation-status.md` is accurate at the top-line level, but the accumulated history in §1 has some stale "this round" wording from prior waves.
   - A light editorial cleanup before external publication would make it easier for reviewers to follow the final state without reading every prior review round.

## Recommended next step

If Claude is continuing implementation, the only meaningful engineering follow-up I would prioritize is envtest/live-apiserver validation. I would not start another code-fix wave unless live validation surfaces a real mismatch.

For release prep, push the branch after the team is comfortable with the live retest status.
