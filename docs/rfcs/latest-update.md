# Latest update

**Branch:** `pr/johalley/ciscoconfig_xe`
**Date:** 2026-04-25
**Author:** Josh Halley
**Scope:** post-follow-up-review remediation. Closes every finding in [`external-review-followup.md`](external-review-followup.md) (Codex, post-fix).

This document is the "what just changed" pointer. If you've been away from the branch for a while, read this. For deeper context follow the cross-references — every finding cites the closing commit.

---

## 1. Bottom line

The branch is **shippable for day-0 AND day-2 under the per-pod topology**, with the aggregator topology exclusive-and-correct. The follow-up review identified five gaps where the first round of remediation (Waves 1–5) was either too shallow, made via a code path production didn't execute, or — in one case — both schema- and engine-rejected. **All five gaps are closed in code** with focused test coverage.

Module-wide `go test -race -count=5 ./...` clean. The previously-flaky `internal/drivers/iosxe/configdriver/writers` package is now stable across `count=20` after dropping two `t.Parallel()` calls on tests that mutated the package-global registry.

[`implementation-status.md`](implementation-status.md) §1 has re-claimed day-2 readiness. Pending work remaining is bounded and named (§5 below).

---

## 2. What changed — the follow-up waves

Nine commits in dependency order, each Wave a self-contained PR-shaped change.

| Wave | Scope | Commit | Files |
|---|---|---|---|
| Source | File the follow-up review document | `ca0b967` | `docs/rfcs/external-review-followup.md` |
| Plan | Triage + remediation plan RFC | `4e9c04d` | `docs/rfcs/external-review-followup-response.md` |
| 0-fu | Walk back `implementation-status` §1's day-2 claim | `93ac920` | `docs/rfcs/implementation-status.md` |
| 1A-fu | NETCONF candidate-aware `Fetch` + CLI in tx scope | `c30cbbc` | `transport.go`, `netconf.go`, `transactional_view.go`, `engine.go` |
| 4A-fu | configPrereqs teardown keeps families + empty source | `d7ce3c6` | `ciscodevice_controller.go`, `dhcp.go` |
| 6A | Subscribe fast-path wired into per-pod controller-runtime | `c916e17` | `config_reconciler.go`, `config_reconciler_controller.go`, `cmd/cisco-vk/config_reconciler.go` |
| 5A-fu | Structured `transport.Op.PathSpec` + production registration | `c414a51` | `transport.go`, `gnmi.go`, `gnmi_keys.go`, `keyed_list.go`, `dhcp.go`, `iosxebuilder/builder.go`, `iosxe/register.go` |
| 6B | Credential Secret rotation reconciled | `eab4175` | `ciscodevice_controller.go`, `aggregator.go` |
| Final | Writer-registry race fix + day-2 re-claim | `ad40180` | `registry_test.go`, `schema_test.go`, `implementation-status.md` |

---

## 3. Per-finding closure

| # | Severity | Title | Status | Closing commit |
|---|---|---|---|---|
| FU-1 | P1 | NETCONF transactional verify reads `running`; CLI bypasses tx | ✅ `TxFetcher` optional interface; NETCONF reads `<source><candidate/>` mid-transaction; CLI ops route through `e.applyTransport` | `c30cbbc` |
| FU-2 | P1 | configPrereqs teardown sets `ManagedFamilies=nil`, schema/engine both reject | ✅ Keep families + empty `source.inline`; engine prunes via per-family `PruneCapable.PruneDiff`; dhcp gained PruneDiff | `d7ce3c6` |
| FU-3 | P2 | Subscribe unconsumed in default per-pod controller-runtime topology | ✅ `source.Channel` + `subscribeNotifyTime` vs `cr.Status.LastDeviceCheck` distinguishes subscribe-driven ticks from CR events | `c916e17` |
| FU-4 | P2 | gNMI keyed-path mis-split on `/`; registry side-effect never fires in production | ✅ Structured `transport.Op.PathSpec`; writers populate it; production registration via `iosxebuilder.RegisterGNMIPathKeysForXE()` from the iosxe driver's `init()` | `c414a51` |
| FU-5 | P2 | CiscoDevice credential Secret rotation not reconciled | ✅ Per-pod: pod-template annotation keyed on Secret resourceVersion → ReplicaSet rolls. Aggregator: SHA-256 `passwordDigest` in `specHash` → worker restarts on rotation | `eab4175` |

Every spot-check from the follow-up review verified at the cited file:line. Nothing was contested.

---

## 4. Test additions

Four new test files, roughly 20 new test cases, all green with the race detector:

| File | Cases | Wave |
|---|---|---|
| `internal/drivers/iosxe/configdriver/engine/transactional_test.go` (extended) | `TestTransactionalVerifyReadsCandidate`, `TestCLIBlockUsesTransactionalView` | 1A-fu |
| `internal/drivers/iosxe/configdriver/transport/netconf_test.go` (extended) | `TestNETCONFFetchTxReadsCandidate` | 1A-fu |
| `internal/controller/ciscodevice_controller_test.go` (updated) | `TestReconcile_ConfigPrereqsRemovedDrivesEmptyIntentThenDeletes` rewritten to assert correct teardown shape | 4A-fu |
| `internal/provider/subscribe_perpod_test.go` (new) | 5 cases covering `subscribeFiredSince` + `NotifySubscribeFired` | 6A |
| `internal/drivers/iosxe/configdriver/transport/gnmi_keys_test.go` (extended) | `TestOpToGNMIPath_PathSpecHandlesSlashInKey`, `TestOpToGNMIPath_PathSpecPreferredOverString`, `TestOpToGNMIPath_FallsBackToStringWhenNoPathSpec` | 5A-fu |
| `internal/controller/credential_rotation_test.go` (new) | 3 cases: per-pod stamp, rotation rolls, fan-out filter | 6B |
| `internal/aggregator/credential_rotation_test.go` (new) | 4 cases: hash rotates on password change, stable on same password, empty handled, digest never cleartext | 6B |

The Wave 4A `fake.Client` test was rewritten — its prior assertions were "passing" only because the fake client skips CRD schema validation. The replacement asserts the schema-valid teardown shape (families intact, empty source, `pruneOnRelinquish=true`).

---

## 5. Remaining pending work

None of these block day-2 readiness.

- **External Phase-8 residuals** (Terraform Registry release, netascode example corpus). Tracked in [`phase-8-residuals.md`](phase-8-residuals.md). Outside the Git repository's reach (publisher accounts, signing keys, content authoring).
- **Live retests of device-write paths against the lab Cat9K.** All three modify running device state and are operator-scheduled:
  - `spec.transactional=true` via NETCONF/830 (separately enabled).
  - `configPrereqs` deletion-driven device-side cleanup.
  - gNMI Set against `interface_ethernet[GigabitEthernet=0/0/0]` via gNMI/6030.
  - Credential rotation against a CiscoDevice with `credentialSecretRef`.
- **Architectural watch-items #4/#9/#10** remain plan-level deliverables in their own RFCs ([`architectural-review.md`](architectural-review.md), [`log-unification-plan.md`](log-unification-plan.md), [`crd-v1-promotion-plan.md`](crd-v1-promotion-plan.md)).
- **envtest infrastructure for schema-validating teardown tests** (the FU-2 follow-up). The fix is correct at the engine and controller level; an envtest that exercises the CRD admission path closes the architectural lesson.

---

## 6. Architectural lessons from this round

Two lessons that the follow-up review made explicit. They're in commit messages and inline code comments so they don't re-recur.

### Lesson 1 — Fake-client tests are not a substitute for envtest

Wave 4A's teardown set `ManagedFamilies=nil`. The CRD has `+kubebuilder:validation:MinItems=1` on managedFamilies, so a real API server rejects the update at admission. The Wave 4A unit test passed only because `sigs.k8s.io/controller-runtime/pkg/client/fake` skips CRD schema validation.

Implication: when CRD field constraints are part of the contract under test, fake-client tests are insufficient. envtest (which spins up a real `kube-apiserver` + `etcd`) is the correct level. Wave-FU adds an envtest follow-up to the pending list explicitly.

### Lesson 2 — Side-effect-driven registries are fragile when the side-effect lives in a code path production doesn't execute

Wave 5A populated the gNMI keyed-list registry as a side effect of `schema.LoadFamilies`. Production startup uses `iosxebuilder.KeyRulesForXE()` and `LoadYANGReleaseTags`; only `tools/cisco-vk-config-docs` (the docs generator) called `LoadFamilies`. The Wave 5A registry was therefore unpopulated in the running cisco-vk binary. Tests passed because tests called `LoadFamilies` directly.

Implication: registrations that the production binary depends on must live on the production startup path explicitly. Wave 5A-fu wires the registration through `iosxebuilder.RegisterGNMIPathKeysForXE()` called from the IOS-XE driver's `init()` — every binary that links the IOS-XE driver registers correctly, regardless of which docs/lint tooling it links alongside.

---

## 7. Cross-references

| RFC | Authoritative for |
|---|---|
| [`external-review.md`](external-review.md) | Original review (Codex) |
| [`external-review-response.md`](external-review-response.md) | Wave 1–5 remediation plan |
| [`external-review-followup.md`](external-review-followup.md) | Follow-up review (Codex, post-fix) |
| [`external-review-followup-response.md`](external-review-followup-response.md) | Wave-FU remediation plan (this update's source) |
| [`implementation-status.md`](implementation-status.md) | Single-source-of-truth status sweep |
| [`architectural-review.md`](architectural-review.md) | Architectural watch-items |
| [`crd-v1-promotion-plan.md`](crd-v1-promotion-plan.md) | v1alpha1 → v1 cut plan |
| [`log-unification-plan.md`](log-unification-plan.md) | Slog backend plan |
| [`phase-8-residuals.md`](phase-8-residuals.md) | External Phase-8 residuals |
| [`driver-extension-guide.md`](driver-extension-guide.md) | Phase-9 plug-in pattern + Phase-10 relocation tracking |

---

## 8. Numerical snapshot

| Signal | Value |
|---|---|
| Commits this update | 9 (`ca0b967` → `ad40180`) |
| Files changed | 22 |
| Lines added | ~1,800 |
| New test files | 4 |
| New test cases (approx) | 20 |
| `go test -race -count=5 ./...` | green module-wide |
| `go test -race -count=20 ./internal/drivers/iosxe/configdriver/writers/` | green (post-FU registry-test fix) |
| `go test -race -count=20 ./internal/drivers` | green (Wave 5B fix from previous round still holds) |

---

## 9. What to read next

If you're trying to ship a release tag from this branch:
1. Read [`implementation-status.md`](implementation-status.md) §1 for the "what is and isn't shippable" summary.
2. Read §5 of THIS document for the operator-scheduled live retests; do at least one against the lab Cat9K before tagging.
3. Read [`phase-8-residuals.md`](phase-8-residuals.md) §2 if the Terraform Registry publish path is on the release plan.

If you're picking up a planned implementation item:
1. [`crd-v1-promotion-plan.md`](crd-v1-promotion-plan.md) for the v1 cut — independent of this branch.
2. [`log-unification-plan.md`](log-unification-plan.md) for the slog backend — independent.

If you're investigating a specific finding's closure:
- Use the table in §3 above; every finding has a closing commit hash.
