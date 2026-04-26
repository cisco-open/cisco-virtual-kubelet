# External review - Wave 8 follow-up

**Branch:** `pr/johalley/ciscoconfig_xe`
**Date:** 2026-04-25
**Reviewer:** Codex
**Scope:** post-Wave-8 review of the lease-arbitration fixes described in `latest-update.md`

This note captures the follow-up review after Wave 8 claimed closure of the Wave-7 residual findings.

The original Wave-7 residuals were reviewed again:

- **W7R-1: Lease names invalid for underscore families** - the core implementation is fixed. `leaseName` now sanitises device/family segments and adds a deterministic SHA-256 hash suffix. The generated names validate as DNS-1123 subdomains in unit tests.
- **W7R-2: Lease conflicts flow through the engine as success/failure** - the implementation is directionally correct: all-blocked intents short-circuit before the engine, partial-block intents downgrade clean `InSync` to `LeaseBlocked`, and `DeviceTouched` gates `LastDeviceCheck`.

However, two gaps remain around the new `LeaseBlocked` state. Both are production-path issues that fake-client tests do not catch.

Verification run during review:

- `GOCACHE=/tmp/cvk-gocache go test ./internal/provider ./internal/drivers/iosxe/configdriver/engine ./internal/controller ./internal/aggregator`
- `go vet ./...`
- `helm lint charts/cisco-virtual-kubelet`
- `GOCACHE=/tmp/cvk-gocache go test -race -count=5 ./...`
- `GOCACHE=/tmp/cvk-gocache go test -race -count=20` across hot packages: `internal/drivers`, IOS-XE engine/writers/transport, `internal/aggregator`, `internal/controller`, and `internal/provider`

All verification commands completed cleanly.

## Finding 1 - P1 - `LeaseBlocked` is not allowed by the IOSXEConfig status schema

**Files:**

- `api/config/v1alpha1/iosxeconfig_types.go`
- `config/crd/config.cisco.vk_iosxeconfigs.yaml`

Wave 8 adds `engine.PhaseLeaseBlocked = "LeaseBlocked"` and writes it to `IOSXEConfig.status.phase`, but the API enum and generated CRD still allow only:

```text
Pending, Validating, Planning, Applying, Verifying, InSync, Drifted, Failed, Paused
```

`LeaseBlocked` is missing from both the kubebuilder marker and the generated CRD enum.

**Operational impact:** a real apiserver should reject the status update whenever the reconciler tries to publish `status.phase: LeaseBlocked`. The fake client does not enforce CRD enum validation, so the current unit suite passes. In a live cluster the lease-blocked path can surface as a status-update error instead of the intended transient phase, defeating the Wave 8.2 operator-facing behavior.

**Recommended action:**

- Add `LeaseBlocked` to the `IOSXEConfigStatus.Phase` kubebuilder enum and comment.
- Regenerate CRDs so `config/crd/config.cisco.vk_iosxeconfigs.yaml` includes `LeaseBlocked`.
- Check any generated docs or examples that list terminal phases.
- Add a schema-aware guard. Best: envtest status update that writes `LeaseBlocked`. Near-term: a unit test that parses the generated CRD and verifies every engine phase intended for status is present in the enum.
- Consider updating `ApplyLogEntry` comments to mention `LeaseBlocked`; its CRD field is currently a plain string, so this is documentation, not admission-critical.

## Finding 2 - P2 - controller-runtime requeue still uses stale pre-update status

**Files:**

- `internal/provider/config_reconciler_controller.go`
- `internal/provider/config_reconciler.go`

Wave 8.2 adds `requeueIntervalFor(&cr)` so `LeaseBlocked` reconciles should requeue at a sub-TTL interval. The problem is that `cr` is the object fetched before `reconcileOne`. `recordResult` writes status through a deep copy:

```go
updated := cr.DeepCopy()
updated.Status.Phase = result.Phase
...
r.Client.Status().Update(ctx, updated)
```

It does not mutate the original `cr` passed into `reconcileOne`. After `reconcileOne` returns, `ConfigReconciler.Reconcile` calls:

```go
return reconcile.Result{RequeueAfter: requeueIntervalFor(&cr)}, nil
```

That means the requeue decision reads the old phase, not the just-written `LeaseBlocked` phase. The same stale object is also used for the span phase attribute.

**Operational impact:** once the CRD enum is fixed and `LeaseBlocked` status writes succeed, the controller-runtime path can still requeue at the normal drift interval, commonly 5 minutes, instead of the intended 15 seconds. That makes normal rollout contention resolve much more slowly than the Wave 8 status text claims. The polling/aggregator `Run` path is less affected because it ticks on its own interval, but the default per-pod controller-runtime path is the advertised production topology.

**Recommended action:**

- Do not compute requeue from the stale fetched CR.
- Preferred fix: have `reconcileOne` return the `engine.Result` or at least the terminal phase/device-touched metadata, then compute `RequeueAfter` from that result.
- Acceptable fix: have `recordResult` copy the updated status back into the passed object after a successful status update, then `requeueIntervalFor(&cr)` can see the new phase. Returning the result is cleaner because it avoids hidden mutation.
- Update the OTel span phase attribute from the same post-reconcile result/status source.
- Add an integration-style controller test with a seeded foreign lease that blocks all families and assert:
  - stored `status.phase == LeaseBlocked`;
  - `Reconcile` returns the sub-TTL requeue interval;
  - `LastDeviceCheck` is unchanged;
  - the engine is not called for the all-blocked case.
- Add a partial-block test that proves a clean engine result is downgraded to `LeaseBlocked` and also gets sub-TTL requeue.

## Additional improvement - P3 - Make lease-name coverage source-of-truth driven

`lease_name_test.go` duplicates a hand-maintained `allShippedFamilies` list and describes it as mirroring the shipped registry. That list can drift as families are added. The current sanitizer is generic enough that the implementation is not immediately at risk, but the test would be stronger if it enumerated the real shipped families from a single source of truth.

**Recommended action:**

- Either export/reuse a canonical family list for tests, or parse `schema/families.yaml` in the test and validate every key.
- Keep a few hostile input cases in addition to the source-of-truth family sweep.

## Status recommendation

Do not yet treat the Wave 8 `latest-update.md` day-2 re-claim as fully revalidated.

The DNS-safe lease-name implementation itself looks sound, and the first-class `LeaseBlocked` model is the right direction. The remaining blockers are integration plumbing around that new phase:

1. Admission/schema must allow `LeaseBlocked`.
2. The controller-runtime requeue decision must use the post-reconcile result, not the stale pre-update CR object.

After those are fixed, run a narrower follow-up review focused on:

- real-apiserver or envtest acceptance of `status.phase=LeaseBlocked`;
- controller-runtime all-blocked and partial-block behavior;
- live underscore-family lease creation, for example `interface_ethernet`.
