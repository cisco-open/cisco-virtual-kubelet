# Latest update

**Branch:** `pr/johalley/ciscoconfig_xe`
**Date:** 2026-04-25
**Author:** Josh Halley
**Scope:** post-Wave-7-residuals remediation. Closes both findings in [`external-review-wave7-residuals.md`](external-review-wave7-residuals.md) (Codex, post-Wave-7).

This document is the "what just changed" pointer. If you've been away from the branch for a while, read this. For deeper context follow the cross-references — every finding cites the closing commit.

For the prior rounds (Wave 1–5, Wave 1A-fu through 6B, Wave 7A through 7B), see the response-RFC chain in §7 below.

---

## 1. Bottom line

The branch is **shippable for day-0 AND day-2 under the per-pod topology**, with the aggregator topology exclusive-and-correct. Four rounds of external review have all closed: original (Waves 1–5), follow-up (Waves 1A-fu through 6B), next-actions (Waves 7A through 7B), and Wave-7 residuals (Waves 8.1 and 8.2).

The Wave-7 residuals round identified two lease-arbitration gaps the post-Wave-7 verification missed. Both lived in code paths `fake.Client` doesn't validate the way a real apiserver does:

- **Lease names with underscores** — `cvk-edge-01-interface_ethernet` violates DNS-1123 subdomain rules. Real apiserver rejects every such Lease.create with a name-validation error. Affects every shipped IOS-XE family except the few without underscores (`vlan`, `vrf`, `dhcp`). The fake client skipped name validation so existing tests passed.
- **Lease conflicts routed through the engine** — `reconcileOne` always called `eng.Reconcile`, even when every family was lease-blocked. Empty `ManagedFamilies` tripped `engine.validate`'s "empty" check → `Phase=Failed`. Or partial-block returned `Phase=InSync`, masking the missed families. Plus `recordResult` bumped `LastDeviceCheck` regardless. Wave 7A.3's runtime-suffixed identity made the contention window normal during rollouts (every credential rotation), so this misleading-status path was hit routinely.

**Both gaps are closed in code** with focused test coverage. Module-wide `go test -race -count=5 ./...` clean; `count=20` stable across all hot packages.

[`implementation-status.md`](implementation-status.md) §1 has re-claimed day-2 readiness with a per-finding closure table.

---

## 2. What changed — the Wave-8 commits

Three commits in dependency order, each Wave a self-contained PR-shaped change.

| Wave | Scope | Commit | Files |
|---|---|---|---|
| Source + Plan + Walk-back | File the residuals review + triage RFC + walk back day-2 claim | `4ba0c2c` | `docs/rfcs/external-review-wave7-residuals.md`, `docs/rfcs/external-review-wave7-residuals-response.md`, `docs/rfcs/implementation-status.md` |
| 8.1 | DNS-1123-safe lease names | `5879a89` | `engine/lease.go`, `engine/lease_name_test.go`, `engine/lease_test.go`, `provider/lease_identity_test.go` |
| 8.2 | Lease conflicts as first-class arbitration state | `ec79777` | `engine/engine.go`, `provider/config_reconciler.go`, `provider/config_reconciler_controller.go`, `provider/lease_blocked_test.go` |
| 8C | docs sweep + day-2 re-claim | (this commit) | `docs/rfcs/implementation-status.md`, `docs/rfcs/latest-update.md` |

---

## 3. Per-finding closure

| # | Severity | Title | Status | Closing commit |
|---|---|---|---|---|
| W7R-1 | P1 | Lease names invalid for underscore families | ✅ sanitise + SHA-256 hash suffix; validated against `IsDNS1123Subdomain` for every shipped family | `5879a89` |
| W7R-2 | P2 | Lease conflicts surface as engine success/failure | ✅ new `PhaseLeaseBlocked`; short-circuit before engine when all-blocked; `Result.DeviceTouched` gates `LastDeviceCheck` bump; sub-TTL requeue | `ec79777` |

Every spot-check from the Wave-7 residuals review verified at the cited file:line. Nothing was contested.

---

## 4. Test additions

Two new test files, eleven new test cases, all green with the race detector.

| File | Cases | Wave |
|---|---|---|
| `internal/drivers/iosxe/configdriver/engine/lease_name_test.go` (new) | `TestLeaseName_AllShippedFamiliesAreDNS1123Subdomain` (HEADLINE — every phase1/2/3 family validated against `IsDNS1123Subdomain`), `TestLeaseName_HostileInputsAreSanitised`, `TestLeaseName_DistinctInputsProduceDistinctNames`, `TestLeaseName_DeterministicAcrossInvocations`, `TestSanitiseLeaseSegment` | 8.1 |
| `internal/provider/lease_blocked_test.go` (new) | `TestRequeueIntervalFor_LeaseBlockedIsSubTTL`, `TestRequeueIntervalFor_NormalUsesDriftInterval`, `TestRequeueIntervalFor_VeryShortDriftIntervalPassesThrough`, `TestEngineResult_DeviceTouchedSetWhenManagedFamilies`, `TestRecordResult_LeaseBlockedDoesNotBumpLastDeviceCheck` (HEADLINE), `TestRecordResult_DeviceTouchedBumpsLastDeviceCheck` | 8.2 |

The existing lease tests (`internal/drivers/iosxe/configdriver/engine/lease_test.go`, `internal/provider/lease_identity_test.go`) were updated to use `leaseName(device, family)` rather than hardcoded `"cvk-edge-01-vlan"` strings — fixtures should reach into the real composition function rather than encode the format.

---

## 5. Remaining pending work

None of these block day-2 readiness.

- **External Phase-8 residuals** (Terraform Registry release, netascode example corpus). Tracked in [`phase-8-residuals.md`](phase-8-residuals.md).
- **Live retests of device-write paths against the lab Cat9K** — all modify running device state and are operator-scheduled:
  - NETCONF transactional commit + structured-only intent → `Phase=InSync` end-to-end (Wave 1A-fu).
  - NETCONF transactional + CLI block → `Phase=Failed` with `ErrTransactionalCLIUnsupported` (Wave 7A.1).
  - configPrereqs deletion-driven cleanup → device clean of any prereq state the controller created (Wave 4A-fu + Wave 7A.2 + Wave 7A.4).
  - gNMI Set against `interface_ethernet[GigabitEthernet=0/0/0]` (Wave 5A-fu + Wave 7B).
  - Credential rotation against a CiscoDevice with `credentialSecretRef` → new pod takes the lease cleanly without overlap; status surfaces `PhaseLeaseBlocked` during the contention window (Wave 6B + Wave 7A.3 + Wave 8.2).
  - Lease creation against a real apiserver for any underscore family (e.g. `interface_ethernet`) — confirms the sanitisation works end-to-end (Wave 8.1).
- **Architectural watch-items #4/#9/#10** remain plan-level deliverables in their own RFCs ([`architectural-review.md`](architectural-review.md), [`log-unification-plan.md`](log-unification-plan.md), [`crd-v1-promotion-plan.md`](crd-v1-promotion-plan.md)).
- **envtest infrastructure for schema-validating + name-validating tests** — the recurring follow-up. Wave 8.1's `IsDNS1123Subdomain` test is the lighter form of envtest's name-validation check; an envtest is the durable closure across both name validation (W7R-1) and schema validation (FU-2).

---

## 6. Architectural lesson internalised this round

One new lesson lands in commit messages and inline comments. The previous architectural lessons (fake-client tests are not envtest; side-effect-driven registries fail in production; async status subresources need ObservedGeneration check; cross-process lease arbitration needs process-unique runtime identity) all remain in force.

### Lesson 5 — `fake.Client` does not enforce Kubernetes object-name validation

`leaseName(device, family)` produced literal `cvk-<device>-<family>` strings. For underscore-bearing family names that's a DNS-1123 subdomain rule violation; a real apiserver rejects with a name-validation error. `sigs.k8s.io/controller-runtime/pkg/client/fake` does NOT run that validation, so `Lease.Create` succeeded in tests and failed in production.

Implication: when a Kubernetes object name is composed from arbitrary strings, the test must explicitly validate the result against `k8s.io/apimachinery/pkg/util/validation.IsDNS1123Subdomain` (or the appropriate per-resource validator). Otherwise the bug surfaces only in a live cluster.

This is the second flavour of the broader "fake-client tests are not envtest" lesson — FU-2 was field validation; W7R-1 is name validation. The durable closure for both is envtest infrastructure (recurring follow-up).

Wave 8.1's `TestLeaseName_AllShippedFamiliesAreDNS1123Subdomain` exercises the validator directly against every family in the shipped registry; future regressions in the `leaseName` composition function fail the unit suite even when `fake.Client` is the only k8s client in the test process.

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
| [`external-review-wave7-residuals.md`](external-review-wave7-residuals.md) | Wave-7 residuals review (this round's source) |
| [`external-review-wave7-residuals-response.md`](external-review-wave7-residuals-response.md) | Wave 8 remediation plan |
| [`implementation-status.md`](implementation-status.md) | Single-source-of-truth status sweep |
| [`architectural-review.md`](architectural-review.md) | Architectural watch-items |
| [`crd-v1-promotion-plan.md`](crd-v1-promotion-plan.md) | v1alpha1 → v1 cut plan |
| [`log-unification-plan.md`](log-unification-plan.md) | Slog backend plan |
| [`phase-8-residuals.md`](phase-8-residuals.md) | External Phase-8 residuals |

---

## 8. Numerical snapshot

| Signal | Value |
|---|---|
| Commits this round | 4 (`4ba0c2c` → 8C) |
| New test files | 2 |
| New test cases | 11 |
| `go test -race -count=5 ./...` | green module-wide |
| `go test -race -count=20` on hot packages | green |
| `go vet ./...` | clean |
| `helm lint charts/cisco-virtual-kubelet` | clean |
| External-review findings closed across four rounds | 24 of 24 |

---

## 9. What to read next

If you're trying to ship a release tag from this branch:
1. Read [`implementation-status.md`](implementation-status.md) §1 for the "what is and isn't shippable" summary.
2. Read §5 of THIS document for the operator-scheduled live retests; do at least one against the lab Cat9K before tagging — pay particular attention to the new "lease creation against a real apiserver for any underscore family" check, which is the durable validation of Wave 8.1's fix.
3. Read [`phase-8-residuals.md`](phase-8-residuals.md) §2 if the Terraform Registry publish path is on the release plan.

If you're picking up a planned implementation item:
1. [`crd-v1-promotion-plan.md`](crd-v1-promotion-plan.md) for the v1 cut.
2. [`log-unification-plan.md`](log-unification-plan.md) for the slog backend.
3. envtest infrastructure for the recurring "fake-client doesn't validate" gap (FU-2 + W7R-1).

If you're investigating a specific finding's closure:
- Use the table in §3 above for Wave-8 (this round) findings.
- Use the closure tables in [`implementation-status.md`](implementation-status.md) §1 for the prior rounds.
