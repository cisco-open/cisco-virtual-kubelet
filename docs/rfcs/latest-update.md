# Latest update

**Branch:** `pr/johalley/ciscoconfig_xe`
**Date:** 2026-04-26
**Author:** Josh Halley
**Scope:** post-Wave-8-followup remediation. Closes both findings in [`external-review-wave8-followup.md`](external-review-wave8-followup.md) (Codex, post-Wave-8).

This document is the "what just changed" pointer. If you've been away from the branch for a while, read this. For deeper context follow the cross-references — every finding cites the closing commit.

For the prior rounds (Wave 1–5, Wave 1A-fu through 6B, Wave 7A through 7B, Wave 8.1 + 8.2), see the response-RFC chain in §7 below.

---

## 1. Bottom line

The branch is **shippable for day-0 AND day-2 under the per-pod topology**, with the aggregator topology exclusive-and-correct. Five rounds of external review have all closed: original (Waves 1–5), follow-up (Waves 1A-fu through 6B), next-actions (Waves 7A through 7B), Wave-7 residuals (Waves 8.1 and 8.2), and Wave-8 follow-up (Waves 9.1 and 9.2).

The Wave-8 follow-up round identified two integration gaps around the new `LeaseBlocked` phase introduced in Wave 8.2. Both lived in code paths `fake.Client` doesn't validate or doesn't exercise the way a real apiserver does:

- **`LeaseBlocked` not in the IOSXEConfig CRD enum** — Wave 8.2 added `engine.PhaseLeaseBlocked` and wrote it to `IOSXEConfig.status.phase`, but the kubebuilder `+kubebuilder:validation:Enum` marker still listed only the original nine phases. A real apiserver would have rejected every lease-blocked status update; `fake.Client` skips enum validation, so unit suites passed. Same family of "fake-client doesn't validate" hazard as W7R-1 (object names) and FU-2 (MinItems) — that's three flavours of the same lesson now.
- **Stale `cr.Status.Phase` on the controller-runtime requeue path** — `reconcileOne` wrote status via a deep copy in `recordResult`; the original `cr` passed in was never mutated. The caller in `Reconcile` then read `cr.Status.Phase` to compute `RequeueAfter`, seeing the previous tick's phase. Result: a tick that just wrote `LeaseBlocked` requeued at the normal 5m drift interval instead of the intended 15s sub-TTL — defeating Wave 8.2's contention-aware behaviour exactly on the production-default per-pod topology.

**Both gaps are closed in code** with focused test coverage. Module-wide `go test -race -count=5 ./...` clean.

[`implementation-status.md`](implementation-status.md) §1 has re-claimed day-2 readiness with a per-finding closure table.

---

## 2. What changed — the Wave-9 commits

Three commits in dependency order, each Wave a self-contained PR-shaped change.

| Wave | Scope | Files |
|---|---|---|
| Source + Plan + Walk-back | File the follow-up review + triage RFC + walk back day-2 claim | `docs/rfcs/external-review-wave8-followup.md`, `docs/rfcs/external-review-wave8-followup-response.md`, `docs/rfcs/implementation-status.md` |
| 9.1 | Schema admission for `LeaseBlocked` | `api/config/v1alpha1/iosxeconfig_types.go`, `config/crd/config.cisco.vk_iosxeconfigs.yaml`, `charts/cisco-virtual-kubelet/crds/config.cisco.vk_iosxeconfigs.yaml`, `internal/provider/iosxeconfig_phase_enum_test.go` |
| 9.2 | Post-reconcile requeue plumbing | `internal/provider/config_reconciler.go`, `internal/provider/config_reconciler_controller.go`, `internal/provider/lease_blocked_test.go` |
| 9C | docs sweep + day-2 re-claim | `docs/rfcs/implementation-status.md`, `docs/rfcs/latest-update.md` |

---

## 3. Per-finding closure

| # | Severity | Title | Status |
|---|---|---|---|
| W8FU-1 | P1 | `LeaseBlocked` is not allowed by the IOSXEConfig status schema | ✅ kubebuilder enum extended; CRD + Helm chart copy regenerated; schema-aware unit test parses the CRD and verifies every status-bound engine phase is enumerated |
| W8FU-2 | P2 | controller-runtime requeue still uses stale pre-update status | ✅ `reconcileOne` returns `(engine.Result, error)`; `requeueIntervalFor` takes phase as an explicit argument; controller-runtime caller passes `result.Phase`, not `cr.Status.Phase`; OTel span phase + drift attribution moved to the engine result |

Every spot-check from the Wave-8 follow-up review verified at the cited file:line. Nothing was contested.

---

## 4. Test additions

One new test file, six new test cases plus one updated existing one, all green with the race detector.

| File | Cases | Wave |
|---|---|---|
| `internal/provider/iosxeconfig_phase_enum_test.go` (new) | `TestCRDEnumIncludesEveryStatusBoundEnginePhase` (HEADLINE — parses the generated CRD YAML and asserts every status-bound engine phase is enumerated) | 9.1 |
| `internal/provider/lease_blocked_test.go` (extended) | `TestRequeueIntervalFor_StalePhaseIgnored` (proves stale `cr.Status.Phase` cannot leak into the requeue decision), `TestReconcile_AllBlockedReturnsSubTTLRequeueAndLeaseBlockedPhase` (HEADLINE — end-to-end controller test: foreign-leased family, asserts sub-TTL requeue + LeaseBlocked phase + LastDeviceCheck unchanged + engine never called) | 9.2 |

The existing `TestRequeueIntervalFor_*` cases were updated to pass the phase argument explicitly — the production caller passes `result.Phase`, so tests should match that calling convention.

---

## 5. Remaining pending work

None of these block day-2 readiness.

- **External Phase-8 residuals** (Terraform Registry release, netascode example corpus). Tracked in [`phase-8-residuals.md`](phase-8-residuals.md).
- **Live retests of device-write paths against the lab Cat9K** — all modify running device state and are operator-scheduled:
  - NETCONF transactional commit + structured-only intent → `Phase=InSync` end-to-end (Wave 1A-fu).
  - NETCONF transactional + CLI block → `Phase=Failed` with `ErrTransactionalCLIUnsupported` (Wave 7A.1).
  - configPrereqs deletion-driven cleanup → device clean of any prereq state the controller created (Wave 4A-fu + Wave 7A.2 + Wave 7A.4).
  - gNMI Set against `interface_ethernet[GigabitEthernet=0/0/0]` (Wave 5A-fu + Wave 7B).
  - Credential rotation against a CiscoDevice with `credentialSecretRef` → new pod takes the lease cleanly without overlap; status surfaces `PhaseLeaseBlocked` during the contention window with a sub-TTL requeue against a real apiserver (Wave 6B + Wave 7A.3 + Wave 8.2 + Wave 9.2).
  - Lease creation against a real apiserver for any underscore family (e.g. `interface_ethernet`) — confirms the sanitisation works end-to-end (Wave 8.1).
  - Real-apiserver acceptance of `status.phase=LeaseBlocked` writes — confirms Wave 9.1's enum admission end-to-end (Wave 9.1).
- **Architectural watch-items #4/#9/#10** remain plan-level deliverables in their own RFCs ([`architectural-review.md`](architectural-review.md), [`log-unification-plan.md`](log-unification-plan.md), [`crd-v1-promotion-plan.md`](crd-v1-promotion-plan.md)).
- **envtest infrastructure for schema-validating + name-validating tests** — the recurring follow-up. Wave 9.1's CRD-parse test is the lighter form of envtest's enum-validation check; an envtest is the durable closure across name validation (W7R-1), MinItems/required validation (FU-2), and field-enum validation (W8FU-1).

---

## 6. Architectural lesson internalised this round

One new lesson lands in commit messages and inline comments. The previous architectural lessons (fake-client tests are not envtest; side-effect-driven registries fail in production; async status subresources need ObservedGeneration check; cross-process lease arbitration needs process-unique runtime identity; fake-client doesn't validate object names) all remain in force.

### Lesson 6 — Status writes via DeepCopy do not mutate the caller's CR

`recordResult` writes to a `cr.DeepCopy()`; the original `cr` is never mutated. Calling code that reads `cr.Status.Phase` after `reconcileOne` returns sees the *previous* tick's value, not the just-written one. Any decision derived from the post-reconcile phase (controller-runtime `RequeueAfter`, OTel span attribution, conditional follow-on writes) must source the phase from the engine result, not from the CR object that was passed into the reconcile.

Implication: when a function writes status via a deep copy and the caller needs the post-write state, the function must return that state explicitly. Reading the caller's still-stale CR is silent: there's no error, no warning, just stale-data behaviour that fake-client tests don't catch because they don't traverse the multi-tick state machine the production controller-runtime path traverses.

This is a different shape from the four "fake-client doesn't validate" lessons — it's not about validation, it's about caller-callee state visibility. But it shares the underlying property that the pre-existing test discipline didn't catch it: fake-client unit tests of `recordResult` and the engine pass; the bug only manifests across a tick boundary on the controller-runtime path.

Wave 9.2's `reconcileOne` now returns `(engine.Result, error)`; the controller-runtime caller uses `result.Phase` for both `RequeueAfter` and span attribution. The headline regression test (`TestReconcile_AllBlockedReturnsSubTTLRequeueAndLeaseBlockedPhase`) seeds a foreign lease, calls `Reconcile`, and asserts all four properties at once — sub-TTL requeue, status.phase set to LeaseBlocked, LastDeviceCheck unchanged, engine never called.

---

## 7. Cross-references

| RFC | Authoritative for |
|---|---|
| [`external-review.md`](external-review.md) | Original review (Codex) — Waves 1–5 |
| [`external-review-response.md`](external-review-response.md) | Wave 1–5 remediation plan |
| [`external-review-followup.md`](external-review-followup.md) | Follow-up review — Waves 1A-fu through 6B |
| [`external-review-followup-response.md`](external-review-followup-response.md) | Wave 1A-fu through 6B remediation plan |
| [`external-review-next-actions.md`](external-review-next-actions.md) | Next-actions review — Waves 7A through 7B |
| [`external-review-next-actions-response.md`](external-review-next-actions-response.md) | Wave 7A through 7B remediation plan |
| [`external-review-wave7-residuals.md`](external-review-wave7-residuals.md) | Wave-7 residuals review |
| [`external-review-wave7-residuals-response.md`](external-review-wave7-residuals-response.md) | Wave 8 remediation plan |
| [`external-review-wave8-followup.md`](external-review-wave8-followup.md) | Wave-8 follow-up review (this round's source) |
| [`external-review-wave8-followup-response.md`](external-review-wave8-followup-response.md) | Wave 9 remediation plan |
| [`implementation-status.md`](implementation-status.md) | Single-source-of-truth status sweep |
| [`architectural-review.md`](architectural-review.md) | Architectural watch-items |
| [`crd-v1-promotion-plan.md`](crd-v1-promotion-plan.md) | v1alpha1 → v1 cut plan |
| [`log-unification-plan.md`](log-unification-plan.md) | Slog backend plan |
| [`phase-8-residuals.md`](phase-8-residuals.md) | External Phase-8 residuals |

---

## 8. Numerical snapshot

| Signal | Value |
|---|---|
| Commits this round | 4 (Wave 0-w8fu → 9C) |
| New test files | 1 |
| New test cases | 3 (one HEADLINE schema parse, one HEADLINE end-to-end controller test, one stale-phase regression pin) |
| Existing test cases updated for new signature | 3 (`TestRequeueIntervalFor_*`) |
| `go test ./...` | green module-wide |
| `go test -race -count=5` on hot packages | green |
| `go vet ./...` | clean |
| `helm lint charts/cisco-virtual-kubelet` | clean |
| External-review findings closed across five rounds | 26 of 26 |

---

## 9. What to read next

If you're trying to ship a release tag from this branch:
1. Read [`implementation-status.md`](implementation-status.md) §1 for the "what is and isn't shippable" summary.
2. Read §5 of THIS document for the operator-scheduled live retests; do at least one against the lab Cat9K before tagging — pay particular attention to the new "real-apiserver acceptance of `status.phase=LeaseBlocked`" check, which is the durable validation of Wave 9.1's fix.
3. Read [`phase-8-residuals.md`](phase-8-residuals.md) §2 if the Terraform Registry publish path is on the release plan.

If you're picking up a planned implementation item:
1. [`crd-v1-promotion-plan.md`](crd-v1-promotion-plan.md) for the v1 cut.
2. [`log-unification-plan.md`](log-unification-plan.md) for the slog backend.
3. envtest infrastructure for the recurring "fake-client doesn't validate" gap (FU-2 + W7R-1 + W8FU-1).

If you're investigating a specific finding's closure:
- Use the table in §3 above for Wave-9 (this round) findings.
- Use the closure tables in [`implementation-status.md`](implementation-status.md) §1 for the prior rounds.
