# Wave 10 implementation — gate run

Companion to [`../2026-04-26-pr-promotion/SUMMARY.md`](../2026-04-26-pr-promotion/SUMMARY.md) and [`../2026-04-26-post-wave10-rfc/SUMMARY.md`](../2026-04-26-post-wave10-rfc/SUMMARY.md). Captures the gate state after the four-commit Wave 10 implementation: confirmed-commit + atomic replace landed end-to-end with backward-compat fallbacks.

The Wave 10 implementation was scoped per [`../../wave10-confirmed-commit-and-atomic-replace.md`](../../wave10-confirmed-commit-and-atomic-replace.md):

- **10.1** (`f887e85`) — transport layer. `ConfirmedCommitter` optional interface, NETCONF `clientCapabilities` advertises `:confirmed-commit:1.0`, `Capabilities.SupportsConfirmedCommit` populated from server hello, transport-level `CommitConfirmed` and `ConfirmCommit` methods with timeout clamping.
- **10.2** (`6142e76`) — engine state machine + `spec.confirmTimeoutSeconds` + `spec.atomicReplace` fields. New `Result.ConfirmedCommitFallback` and `Result.ConfirmedCommitUsed`. Five engine unit tests (happy path + auto-revert + two fallback flavours + non-transactional). Three envtest admission tests including the `Maximum=300` negative control.
- **10.3** (`a5b05c0`) — atomic-replace engine path. `Engine.FamilyOrder` callback, `iosxebuilder.FamilyOrderForXE()` topo-sort over schema's `depends_on` declarations, `ConfigDriverContext.FamilyOrder` field threaded into both production wiring sites (per-pod + aggregator). Three engine unit tests (atomic-replace-implies-prune + family-order-applied + nil-preserves-input).
- **10.4** — release-blocker tests 08 + 09 authored as operator-runnable artefacts under [`../../release-blocker-tests/`](../../release-blocker-tests/), RUNBOOK execution order updated, architectural review §6.B Wave-10 row flipped from `⏸` to `✅`, and reconciler integration: the recorder now emits `ConfirmedCommitFallback` (Warning) and `ConfirmedCommitUsed` (Normal) Kubernetes events so operators see why their auto-revert safety net did or didn't engage.

## Result

| Gate | Outcome | Exit | Evidence |
|---|---|---:|---|
| `go test ./...` | ✅ PASS | 0 | [`01-static-gates/go-test.out`](./01-static-gates/go-test.out) |
| `go test -race -count=5 ./...` | ✅ PASS | 0 | [`01-static-gates/go-test-race-count5.out`](./01-static-gates/go-test-race-count5.out) |
| `go vet ./...` | ✅ clean | 0 | [`01-static-gates/go-vet.out`](./01-static-gates/go-vet.out) |
| `helm lint charts/cisco-virtual-kubelet` | ✅ clean (1 info) | 0 | [`01-static-gates/helm-lint.out`](./01-static-gates/helm-lint.out) |
| CRD/Helm-chart sync | ✅ 8/8 in sync | — | [`01-static-gates/crd-chart-sync.out`](./01-static-gates/crd-chart-sync.out) |
| `make test-envtest` (6 tests) | ✅ 6/6 PASS | 0 | [`02-envtest/run.out`](./02-envtest/run.out) |

All six gates green.

## Test counts after Wave 10

| Test category | Count before Wave 10 | Count after Wave 10 |
|---|---:|---:|
| Engine unit tests (transactional + family-order + new confirmed-commit) | ~25 | **+8** (5 confirmed-commit + 3 atomic-replace) |
| NETCONF transport tests | ~8 | **+4** (capability + round-trip + timeout-clamp + capability-rejection) |
| envtest admission cases | 3 | **+3** (confirm-timeout-admitted + max-enforced + atomic-replace-admitted) |
| Live-device release-blocker tests | 6 | **+2** (08 confirmed-commit auto-revert + 09 atomic-replace cross-family) |
| **Total Wave 10 additions** | — | **17 new tests** |

## What this bundle proves

1. **The full Wave 10 implementation builds cleanly** across the whole module. Zero new vet warnings; no helm-lint regressions; CRDs regenerated and synced to the chart.
2. **Race-detector at count=5 stable** — 22 packages × 5 iterations × all goroutines, no data races introduced by the new state machine or the new transport methods.
3. **Real-apiserver admission is enforced** for the new `confirmTimeoutSeconds` field including the `Maximum=300` upper bound, and for the new `atomicReplace` boolean.
4. **Backward compatibility holds** at three layers, each tested:
   - `ConfirmTimeoutSeconds=0` (existing CRs): plain Commit path unchanged.
   - Transport doesn't implement `ConfirmedCommitter` (RESTCONF, gNMI today): engine falls back to plain Commit AND surfaces `Result.ConfirmedCommitFallback="transport does not implement ConfirmedCommitter"` for the recorder to event-warn.
   - Device didn't advertise `:confirmed-commit:1.0` (older IOS-XE images): engine falls back AND surfaces `Result.ConfirmedCommitFallback="device did not advertise confirmed-commit:1.0"`.

## What this bundle does NOT prove

The Wave 10 design's single most-important assertion — that the Cat9K's own auto-revert timer actually fires when the controller can't `ConfirmCommit` — is **not** in this bundle. It cannot be: that test (release-blocker test 08) is a live-device test by design, requiring the operator to deliberately break the management session and observe the device recover itself. It is filed as a 🔒 release-tag blocker in [`../../architectural-review-final.md`](../../architectural-review-final.md) §6.D.ii row 7, with the operator-runnable playbook at [`../../release-blocker-tests/08-confirmed-commit-auto-revert/`](../../release-blocker-tests/08-confirmed-commit-auto-revert/).

Same for release-blocker test 09 (atomic replace cross-family): the engine's transactional ordering is unit-tested here, but the actual VRF-binding-removal-then-VRF-removal sequence on a real device is a live-test concern. Playbook at [`../../release-blocker-tests/09-atomic-replace-cross-family/`](../../release-blocker-tests/09-atomic-replace-cross-family/).

## How to use this bundle

If a reviewer asks *"is Wave 10 implemented and well-tested at the unit/integration level?"* the answer is in the table above: yes, all six gates green, 17 new tests added, backward-compat preserved across three layers.

If the same reviewer asks *"has Wave 10 been validated against a real Cat9K?"* the answer is "the operator has the playbook; live validation is a release-tag prerequisite, not a merge prerequisite, per the §6.D.ii framing."
