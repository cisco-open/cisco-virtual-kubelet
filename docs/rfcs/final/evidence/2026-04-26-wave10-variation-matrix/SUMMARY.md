# Wave 10 variation matrix — playbook + envtest extension

Companion to [`../2026-04-26-wave10-implementation/SUMMARY.md`](../2026-04-26-wave10-implementation/SUMMARY.md). Captures the gate state after the Wave 10 variation-matrix extension that lands per the option C plan: extend playbook + envtest coverage rather than execute live tests autonomously.

## Why this bundle exists

The user requested testing "absolutely every variation against 10.1.1.1." The hook policy denies all forms of agent-driven contact with 10.1.1.1, regardless of authorization phrasing — that constraint did not change for this round (and Wave 10's deliberate-management-session-break test 08 makes it more sensitive, not less).

Option C of the alternatives I surfaced was: extend the playbook + envtest coverage so the operator can drive the variation matrix end-to-end during a maintenance window. This bundle is the gate run for that extension.

## Variation matrix coverage added

Three new live test packages under [`../../release-blocker-tests/`](../../release-blocker-tests/):

| Package | Variation | What it proves |
|---|---|---|
| [`10-confirmed-commit-happy-path/`](../../release-blocker-tests/10-confirmed-commit-happy-path/) | `confirmTimeoutSeconds=30` + NETCONF + clean apply | Auto-revert path engages successfully end-to-end. `ConfirmedCommitUsed` event fires; `outcome="confirmed"` counter increments. **Catches the silent-fallback regression** where an opt-in operator silently drops to plain Commit. |
| [`11-confirmed-commit-restconf-fallback/`](../../release-blocker-tests/11-confirmed-commit-restconf-fallback/) | `confirmTimeoutSeconds=30` + non-transactional CR | `ConfirmedCommitFallback` Warning event surfaces with reason `non-transactional reconcile`. **Lowest-risk Wave-10 live test**; no auto-revert path engaged. Backward-compat assertion. |
| [`13-atomic-replace-with-confirmed-commit/`](../../release-blocker-tests/13-atomic-replace-with-confirmed-commit/) | `atomicReplace=true` + `confirmTimeoutSeconds=30` combined | The recommended-default proof. Two `ConfirmedCommitUsed` events (one per phase), zero fallbacks, zero auto-reverts. |

Three new envtest admission cases added to [`../../../../internal/provider/envtest_apiserver_smoke_test.go`](../../../../internal/provider/envtest_apiserver_smoke_test.go):

| Test | What it pins |
|---|---|
| `TestEnvtest_ConfirmTimeoutSecondsBoundaryValues` | Min=0 and Max=300 boundary values accepted; `-1` rejected. (>300 was already covered by `TestEnvtest_ConfirmTimeoutSecondsMaximumEnforced`.) |
| `TestEnvtest_AtomicReplaceWithConfirmedCommitCombined` | The combined-mode CR shape (atomicReplace=true + confirmTimeoutSeconds=30) is admissible. Catches a future kubebuilder rule that accidentally forbids the combination. |
| `TestEnvtest_NonTransactionalCRWithConfirmTimeoutAdmissible` | Non-transactional + confirmTimeoutSeconds is admissible. The fallback the engine emits is a runtime concern, not an admission concern; this test catches a future CEL rule that would break the documented fallback contract. |

## Gate result

| Gate | Outcome | Exit |
|---|---|---:|
| `go test ./...` | ✅ PASS | 0 |
| `go test -race -count=5 ./...` | ✅ PASS | 0 |
| `go vet ./...` | ✅ clean | 0 |
| `helm lint` | ✅ clean | 0 |
| `make test-envtest` | ✅ **9/9** PASS (was 6/6 before this commit) | 0 |

Total release-blocker test packages on disk: **12** (reserves 12 for a future "older IOS-XE without `:confirmed-commit:1.0`" test that requires a second device with an older image).

## Operator handoff

When the operator schedules a maintenance window for Wave 10 live validation, the runbook execution order ([`../../release-blocker-tests/RUNBOOK.md`](../../release-blocker-tests/RUNBOOK.md) §4) now spans 12 tests. Approximate per-test times sum to ~90–120 minutes if every test passes first try.

The §6.D.ii row count in [`../architectural-review-final.md`](../architectural-review-final.md) remains 8 🔒 release-tag blockers — tests 10, 11, and 13 are all variations of the existing Wave-10 release-blocker entries (rows 7 and 8) and don't add new release blocker rows; they enrich the playbook for the existing ones.

## What this bundle does NOT prove

Same as the prior post-Wave-10-implementation bundle: the unit + envtest coverage proves the engine takes the right path; only live execution against the Cat9K can prove the device behaves as RFC 6241 §8.4 specifies. That remains operator-scheduled per §6.D.ii.
