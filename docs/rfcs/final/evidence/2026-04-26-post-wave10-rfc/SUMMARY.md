# Post-Wave-10-RFC gate re-run

Companion to [`../2026-04-26-pr-promotion/SUMMARY.md`](../2026-04-26-pr-promotion/SUMMARY.md). Captures the gate state immediately after commit `f4281c8` (Wave 10 RFC + cross-reference updates) was added on top of the pre-PR enrichment work.

The Wave 10 commit was docs-only: a new markdown plan plus cross-reference updates in three other markdown files. Re-running the gates was the responsible "after-doc-commit" check — a doc-only change should not regress any code path, but the assertion is worth making explicit when the documentation in question is canonical (`architectural-review-final.md` is the merge-decision register).

## Result

| Gate | Outcome | Exit | Evidence |
|---|---|---:|---|
| `go test ./...` | ✅ PASS | 0 | [`01-static-gates/go-test.out`](./01-static-gates/go-test.out) |
| `go test -race -count=5 ./...` | ✅ PASS | 0 | [`01-static-gates/go-test-race-count5.out`](./01-static-gates/go-test-race-count5.out) |
| `go vet ./...` | ✅ clean | 0 | [`01-static-gates/go-vet.out`](./01-static-gates/go-vet.out) |
| `helm lint charts/cisco-virtual-kubelet` | ✅ clean | 0 | [`01-static-gates/helm-lint.out`](./01-static-gates/helm-lint.out) |
| CRD/Helm-chart sync | ✅ 8/8 in sync | — | [`01-static-gates/crd-chart-sync.out`](./01-static-gates/crd-chart-sync.out) |
| `make test-envtest` (3 tests) | ✅ 3/3 PASS | 0 | [`02-envtest/run.out`](./02-envtest/run.out) |

All six gates green. The Wave 10 RFC commit caused zero behavioural change, as expected for a docs-only commit.

## What this run does NOT prove

It does **not** exercise the Wave 10 design itself. The RFC describes a `ConfirmedCommitter` transport interface, a `spec.confirmTimeoutSeconds` per-CR knob, a `spec.atomicReplace` per-CR knob, an engine-level commit-confirm-or-revert state machine, and two new live release-blocker tests (`08-confirmed-commit-auto-revert`, `09-atomic-replace-cross-family`). **None of those are implemented in code on this branch** — they're a deferred PR plan. This evidence bundle is therefore a "the docs commit didn't break anything" check, not a "Wave 10 works" check.

When Wave 10 lands as its own PR, that PR's evidence bundle will:
- Add unit-test outputs for the six new tests in §3.1 of the Wave 10 RFC.
- Add envtest admission-test outputs for `confirmTimeoutSeconds` and `atomicReplace`.
- Add release-blocker test outputs for tests 08 and 09 once the operator runs them in a maintenance window (test 08 specifically requires deliberately breaking the management session to prove auto-revert).

Until that PR lands, the Wave 10 design is testable in concept but not in execution.

## How to use this bundle

If a future PR review is asking *"did the Wave 10 RFC commit regress anything?"* the answer is in the table above: no.

If the same review is asking *"is Wave 10 working?"* the answer is "Wave 10 is filed as a plan, not implemented; this branch is not the place to ask."
