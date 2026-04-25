# Latest update

**Branch:** `pr/johalley/ciscoconfig_xe`
**Date:** 2026-04-25
**Author:** Josh Halley
**Scope:** post-next-actions remediation. Closes every finding in [`external-review-next-actions.md`](external-review-next-actions.md) (Codex, post-`latest-update.md`).

This document is the "what just changed" pointer. If you've been away from the branch for a while, read this. For deeper context follow the cross-references — every finding cites the closing commit.

For the prior round (post-Wave-5 follow-up review), see commits `ca0b967` through `ad40180` and the closure table in [`implementation-status.md`](implementation-status.md). For the original round, see commits `d75bc95` through `2e83ac9`.

---

## 1. Bottom line

The branch is **shippable for day-0 AND day-2 under the per-pod topology**, with the aggregator topology exclusive-and-correct. Three rounds of external review have all closed: original (Waves 1–5), follow-up (Waves 1A-fu through 6B), and next-actions (Waves 7A.1 through 7B).

The next-actions round identified five gaps the post-follow-up verification missed. Two were subtle race/lifecycle hazards (configPrereqs teardown freshness vs stale status, lease identity overlap during Wave-6B-driven pod rollouts); three were correctness gaps (NETCONF CLI bypassing transactions, `pruneOnRelinquish` triggering authoritative pruning on every steady-state reconcile, handwritten interface writers missing structured `PathSpec`).

**All five next-actions gaps are closed in code** with focused test coverage. Module-wide `go test -race -count=5 ./...` clean; `count=20` stable across the previously-flaky packages.

[`implementation-status.md`](implementation-status.md) §1 has re-claimed day-2 readiness with a per-finding closure table.

---

## 2. What changed — the next-actions waves

Eight commits in dependency order, each Wave a self-contained PR-shaped change.

| Wave | Scope | Commit | Files |
|---|---|---|---|
| Source | File the next-actions review document + triage/plan RFC | `e6b3f62` | `docs/rfcs/external-review-next-actions.md`, `docs/rfcs/external-review-next-actions-response.md` |
| 0-na | Walk back `implementation-status` §1's day-2 claim | `eff2d28` | `docs/rfcs/implementation-status.md` |
| 7A.3 | Lease holder runtime identity (FIRST — acutely dangerous post-Wave-6B) | `c2315b6` | `config_reconciler.go`, `cmd/cisco-vk/config_reconciler.go`, `aggregator.go`, `ciscodevice_controller.go` |
| 7A.1 | NETCONF transactional+CLI rejection (fail-closed) | `71f5505` | `engine.go`, `transactional_test.go` |
| 7A.2 | configPrereqs teardown ObservedGeneration gate | `0ac63c3` | `ciscodevice_controller.go`, `ciscodevice_controller_test.go` |
| 7A.4 | `pruneOnRelinquish` only during teardown | `965f80a` | `ciscodevice_controller.go`, `iosxeconfig_types.go`, CRDs |
| 7B | PathSpec on handwritten interface writers | `3388628` | `interface_ethernet.go`, `interface_switchport.go`, `nested_keyed.go`, `keyed_list.go` |
| 7C | docs sweep + day-2 re-claim | (this commit) | `implementation-status.md`, `latest-update.md` |

---

## 3. Per-finding closure

| # | Severity | Title | Status | Closing commit |
|---|---|---|---|---|
| NA-1 | P1 | NETCONF CLI bypasses transaction | ✅ engine fail-fast: `Phase=Failed` + `ErrTransactionalCLIUnsupported` before any transport mutation | `71f5505` |
| NA-2 | P1 | configPrereqs teardown stale-status-vulnerable | ✅ gate now requires `ObservedGeneration == Generation` AND `Phase == InSync` | `0ac63c3` |
| NA-3 | P1 | Lease identity per-CR; pod rollouts overlap (acutely worsened by Wave 6B) | ✅ runtime-suffixed identity; per-pod via `POD_UID` downward-API, aggregator via crypto-rand UUID; CR identity preserved for status messages | `c2315b6` |
| NA-4 | P2 | `pruneOnRelinquish=true` on steady-state → silent authoritative deletion of operator-added entries | ✅ flag = false on upsert (additive day-0); = true only during teardown step 1; API docstring rewritten | `965f80a` |
| NA-5 | P2 | gNMI handwritten interface writers (`interface_ethernet`, `interface_switchport`) miss `PathSpec` | ✅ structured `PathSpec` populated; `nestedKeyedListWriter` also covered after audit; lab case `GigabitEthernet=0/0/0` preserves the slash | `3388628` |

Every spot-check from the next-actions review verified at the cited file:line. Nothing was contested.

---

## 4. Test additions

Three new test files plus targeted updates, ~15 new test cases, all green with the race detector:

| File | Cases | Wave |
|---|---|---|
| `internal/provider/lease_identity_test.go` (new) | `TestStripRuntimeIDSuffix`, `TestAcquireLeases_RuntimeIDDistinguishesHolders` (headline), `TestAcquireLeases_SameRuntimeIDRenewsOwnLease`, `TestAcquireLeases_EmptyRuntimeIDPreservesLegacyBehaviour` | 7A.3 |
| `internal/drivers/iosxe/configdriver/engine/transactional_test.go` (replaced 1 test, added 2) | `TestTransactionalCLIRejected` (replaces the prior `TestCLIBlockUsesTransactionalView` whose contract was the exact bug), `TestNonTransactionalCLISucceeds` | 7A.1 |
| `internal/controller/ciscodevice_controller_test.go` (extended, 1 new test) | `TestReconcile_ConfigPrereqsSteadyStateIsAdditive` (new), updated `TestReconcile_ConfigPrereqsRemovedDrivesEmptyIntentThenDeletes` to cover stale-status branch | 7A.2 + 7A.4 |
| `internal/drivers/iosxe/configdriver/writers/interface_pathspec_test.go` (new) | `TestEthernetWriter_DiffEmitsPathSpecPreservingSlash` (headline), `TestSwitchportWriter_DiffEmitsPathSpecWithChild`, `TestPathSpecForInterface_Helper`, `TestPathSpecForInterfaceChild_Helper`, `TestEthernetWriter_PathSpecRoundTripsThroughGNMI` | 7B |

The Wave 1A-fu `TestCLIBlockUsesTransactionalView` was replaced — its assertion (CLI Mutate carries the candidate handle) reflected the exact buggy contract NA-1 identified. The replacement asserts the corrected contract (rejection before any mutation).

---

## 5. Remaining pending work

None of these block day-2 readiness.

- **External Phase-8 residuals** (Terraform Registry release, netascode example corpus). Tracked in [`phase-8-residuals.md`](phase-8-residuals.md).
- **Live retests of device-write paths against the lab Cat9K** — all modify running device state and are operator-scheduled:
  - NETCONF transactional commit + structured-only intent (no CLI) → `Phase=InSync` end-to-end.
  - NETCONF transactional + CLI block → `Phase=Failed` with the new error message; no device-side mutation.
  - configPrereqs deletion-driven cleanup → device's `show running-config` clean of any prereq state the controller created.
  - gNMI Set against `interface_ethernet[GigabitEthernet=0/0/0]` (gNMI/6030 separately enabled).
  - Credential rotation against a CiscoDevice with `credentialSecretRef` → new pod takes the lease cleanly without overlap.
- **Architectural watch-items #4/#9/#10** remain plan-level deliverables in their own RFCs ([`architectural-review.md`](architectural-review.md), [`log-unification-plan.md`](log-unification-plan.md), [`crd-v1-promotion-plan.md`](crd-v1-promotion-plan.md)).
- **envtest infrastructure for schema-validating tests** (the FU-2 follow-up). The fix is correct at the engine and controller level; an envtest that exercises the CRD admission path closes the architectural lesson. NA-2 strengthened the in-tree test by adding the stale-status branch, but a true envtest is the durable closure.

---

## 6. Architectural lessons internalised this round

Two new lessons land in commit messages and inline code comments so they don't re-recur. (The previous two — "fake-client tests are not envtest" and "side-effect-driven registries fail in production" — remain in force from the FU round.)

### Lesson 3 — Async status subresources mean `Status.X` and `Spec.Y` can disagree during a reconcile cycle

Wave 4A-fu's teardown gate read `Status.Phase` to decide whether the per-device reconciler had finished applying the empty-source intent. `Status.Phase` is updated asynchronously: a previous reconcile's `InSync` value can persist after the controller mutates Spec but before the per-device reconciler observes the mutation. The gate then passed incorrectly and the controller deleted the CR before any device-side prune ran.

Implication: gates that read both `Spec.X` and `Status.X` must explicitly verify they refer to the same generation. `Status.ObservedGeneration == metadata.Generation` is the canonical check.

Wave 7A.2 adds this check; the unit test now exercises both a stale-status case (gate must wait) and a matching-generation case (gate may proceed).

### Lesson 4 — A lease that protects in-process duplicate writers does NOT protect cross-process overlap during pod/worker rollouts

Wave 6B's credential-rotation rollout produces routine overlap windows: the new pod stamped with a fresh annotation comes up while the old pod is still running. Both pods derive lease identity from the same CR namespace+name; both `lease.Renew()` succeed; both write the same `(device, family)`.

Implication: cross-process lease arbitration requires process-unique holder identity. Per-pod via downward API (`metadata.uid`); aggregator via UUID per worker start. The CR identity stays the operator-visible string for status; the runtime suffix lives in the lease only.

Wave 7A.3 adds the runtime suffix and tests pin the contract. The reviewer correctly flagged this as the most acutely dangerous post-Wave-6B finding — closing it first removes the duplicate-writer hazard credential rotation now exposes.

---

## 7. Cross-references

| RFC | Authoritative for |
|---|---|
| [`external-review.md`](external-review.md) | Original review (Codex) — Waves 1–5 |
| [`external-review-response.md`](external-review-response.md) | Wave 1–5 remediation plan |
| [`external-review-followup.md`](external-review-followup.md) | Follow-up review — Waves 1A-fu through 6B |
| [`external-review-followup-response.md`](external-review-followup-response.md) | Wave 1A-fu through 6B remediation plan |
| [`external-review-next-actions.md`](external-review-next-actions.md) | Next-actions review (this round's source) |
| [`external-review-next-actions-response.md`](external-review-next-actions-response.md) | Wave 7A/7B remediation plan |
| [`implementation-status.md`](implementation-status.md) | Single-source-of-truth status sweep |
| [`architectural-review.md`](architectural-review.md) | Architectural watch-items |
| [`crd-v1-promotion-plan.md`](crd-v1-promotion-plan.md) | v1alpha1 → v1 cut plan (incl. potential `pruneOnRelinquish` rename) |
| [`log-unification-plan.md`](log-unification-plan.md) | Slog backend plan |
| [`phase-8-residuals.md`](phase-8-residuals.md) | External Phase-8 residuals |

---

## 8. Numerical snapshot

| Signal | Value |
|---|---|
| Commits this round | 8 (`e6b3f62` → Wave 7C) |
| New test files | 3 |
| New test cases (approx) | 15 |
| `go test -race -count=5 ./...` | green module-wide |
| `go test -race -count=20` on hot packages (`internal/drivers`, `writers`, `transport`, `engine`, `aggregator`, `controller`, `provider`) | green |
| `go vet ./...` | clean |
| `helm lint charts/cisco-virtual-kubelet` | clean |
| External-review findings closed across three rounds | 22 of 22 |

---

## 9. What to read next

If you're trying to ship a release tag from this branch:
1. Read [`implementation-status.md`](implementation-status.md) §1 for the "what is and isn't shippable" summary.
2. Read §5 of THIS document for the operator-scheduled live retests; do at least one against the lab Cat9K before tagging.
3. Read [`phase-8-residuals.md`](phase-8-residuals.md) §2 if the Terraform Registry publish path is on the release plan.

If you're picking up a planned implementation item:
1. [`crd-v1-promotion-plan.md`](crd-v1-promotion-plan.md) for the v1 cut — including the potential `pruneOnRelinquish` → `authoritativePrune` rename.
2. [`log-unification-plan.md`](log-unification-plan.md) for the slog backend.

If you're investigating a specific finding's closure:
- Use the table in §3 above for next-actions findings; every finding has a closing commit hash.
- Use the closure table in [`implementation-status.md`](implementation-status.md) §1 for the original and follow-up rounds.
