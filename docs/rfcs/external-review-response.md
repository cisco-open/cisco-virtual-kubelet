# Response to external review

**Branch:** `pr/johalley/ciscoconfig_xe`
**Subject of response:** [`external-review.md`](external-review.md) (Codex, 2026-04-25)
**Author:** Josh Halley
**Status:** plan, not implementation. Each finding has a triage verdict, a remediation approach, and an acceptance criterion. The order is the recommended execution sequence; nothing in this document is patched code yet.

---

## 1. Bottom-line response

The external review is **accurate and well-targeted**. Every P1 finding I spot-checked (1, 2, 3, 4) and every P2 finding I spot-checked (5, 7, 11) is real and at the cited line numbers. The P3 finding (registry test parallel + global state) reproduces immediately with `go test -race -count=20 ./internal/drivers` — same failure modes Codex described.

The review's bottom line — *"I would treat the current branch as a strong prototype or late-stage integration branch for the per-pod topology, not yet as a production-ready day-2 configuration controller"* — is the correct verdict. [`implementation-status.md`](implementation-status.md) §1 currently claims "shippable today under the per-pod topology", and that overstates the case for **day-2** management because:

- Steady-state drift detection is bypassed by a hash short-circuit (Finding 2).
- Transactional apply and `writeStartup` are inert (Finding 1).
- Aggregator mode is a hidden duplicate-writer hazard (Finding 3, 4).

Day-0 (initial apply, report-mode drift detection, apphosting bring-up) is genuinely shippable; day-2 is not. The status doc must distinguish those cases. A walk-back is part of the plan in §3.

The review did not surface anything I disagree with, and I am not contesting any finding. The plan below is execution sequencing, not negotiation.

---

## 2. Triage

Status column meaning: **confirmed** = I verified by reading the cited code; **reproduced** = I ran the cited command and saw the same failure; **accepted** = the review is correct on architectural grounds and I am not contesting it.

| # | Severity | Title | Status |
|---|---|---|---|
| 1 | P1 | Transactional/writeStartup are inert | confirmed |
| 2 | P1 | Steady-state drift detection bypassed | confirmed |
| 3 | P1 | Aggregator coexists with per-device VK pods | confirmed |
| 4 | P1 | Aggregator Helm RBAC incomplete | confirmed |
| 5 | P2 | VK RBAC cannot clear replay annotations | confirmed |
| 6 | P2 | Production reconciler drops YANG validation/defaulting | accepted |
| 7 | P2 | Conflict status only checks first family | confirmed |
| 8 | P2 | configPrereqs deletion does not revert device state | accepted |
| 9 | P2 | Bundle selector membership not watched | accepted |
| 10 | P2 | Bundle CRD requires `template.deviceRef` despite controller filling it | accepted |
| 11 | P2 | secretRefs changes don't trigger reconciliation | confirmed |
| 12 | P2 | gNMI keyed paths schema-blind | accepted |
| 13 | P3 | Registry tests parallel + global state | reproduced |

Nothing is **disputed**.

---

## 3. Remediation plan

The plan is structured in five waves. Waves 1 and 2 are gating: the P1 semantic gaps (Wave 1) and the production-path consistency issues (Wave 2) must close before the implementation-status doc can re-claim "shippable for day-2 under per-pod topology". Wave 3 hardens day-0 fleet semantics. Wave 4 closes the documentation-vs-code mismatch on lifecycle. Wave 5 is the longer-tail gNMI rework and the test-suite reliability fix.

### Wave 1 — P1 semantic gaps (must close)

#### 1A. Transactional apply + `SaveStartup` plumb (Finding 1)

**What.** The engine's per-family apply loop calls `w.Apply(ctx, transport, ops)` with no transaction context and never invokes `transport.SaveStartup`. The transport `Capabilities()` already reports `SupportsTransactions` and `SupportsSaveStartup` correctly; the engine just doesn't consult them.

**Approach.** Three parts:

1. **Transaction wrapping in the engine.** `Engine.Reconcile` opens one transaction per device-tick when both `spec.transactional` and `transport.Capabilities().SupportsTransactions` are true. The handle threads through the family-apply loop. Commit on full success, discard on first error. NETCONF gets candidate+commit; gNMI gets a single batched Set; RESTCONF returns `ErrUnsupported` from `StartTransaction` and the engine gracefully falls back to per-op.
2. **Writer signature change.** `writers.SectionWriter.Apply(ctx, transport, ops)` becomes `Apply(ctx, transport, tx TxHandle, ops)`. Writers that don't care pass the handle through untouched. Single-grep refactor; no semantic change at the writer level.
3. **`SaveStartup` post-success.** After verify-diff is clean for every managed family AND `spec.writeStartup == true` AND `transport.Capabilities().SupportsSaveStartup`, call `transport.SaveStartup(ctx)`. Failure of `SaveStartup` does not roll back the apply (already committed); it surfaces as a non-fatal `Warning` event and a `WriteStartupFailed` condition.

**Acceptance.**
- New unit tests: NETCONF mock asserting `<commit/>` after writes, `<discard-changes/>` on error, lock/unlock around the candidate datastore. gNMI mock asserting one batched `SetRequest`. RESTCONF mock asserting `StartTransaction` returns `ErrUnsupported` and per-op continues.
- New integration test (envtest, see Wave 5 §1B): `spec.transactional=true` + NETCONF stub, force one writer to error, assert no partial state lands.
- Live retest of the smoke loop with `spec.transactional=true` + `spec.writeStartup=true` against the lab Cat9K via NETCONF (port 830, separately enabled). Assert `show running-config` mid-apply does not show partial state.

**Effort.** ~3 engineer-days for code, ~1 engineer-day for tests, ~0.5 engineer-day for live retest.

#### 1B. Steady-state drift detection (Finding 2)

**What.** `recordResult` writes `LastAppliedHash + ObservedGeneration + Phase=InSync`; `Reconcile` on the next tick observes those three matching the current generation/hash and returns before any `Fetch`. RESTCONF/NETCONF polling is therefore disabled after the first clean apply, and `SubscribeNotify` events re-enter the same short-circuit.

**Approach.** Separate "intent freshness" from "device freshness".

- Add `status.lastDeviceCheck` (timestamp). Distinct from `status.lastAppliedHash`.
- On every reconcile entry, compute `dueForDriftCheck := time.Since(status.lastDeviceCheck) >= spec.driftDetectInterval`.
- The hash short-circuit fires only when `applied == false && hash unchanged && phase == InSync && !dueForDriftCheck && !subscribeTriggered`.
- A subscribe notification (`SubscribeNotify` channel) sets a one-shot bypass flag that the reconciler checks before the short-circuit.
- `spec.driftDetectInterval` validation: minimum 30s, default 5m, accepts Go duration strings.
- The reconcile loop is already requeued via controller-runtime's `Result.RequeueAfter`; set it to the next-due interval.

**Acceptance.**
- Unit test: hash unchanged + last-check stale → reconcile proceeds, calls Fetch, updates last-check.
- Unit test: hash unchanged + last-check fresh → short-circuit, no Fetch.
- Unit test: subscribe event fired → bypasses short-circuit even when fresh.
- Live retest: change device hostname out of band on the lab Cat9K, wait one `driftDetectInterval`, observe drift surfacing in `IOSXEConfig.status`.

**Effort.** ~2 engineer-days code, ~0.5 engineer-day tests, ~0.5 engineer-day live retest.

#### 1C. Aggregator/per-pod exclusivity (Finding 3)

**What.** `cisco-vk-controller` always creates a per-device `Deployment`, and the spawned `cisco-vk run` always starts its in-pod ConfigReconciler. With aggregator mode also running its own ConfigReconciler against the same `(device, family)` lease *namespace*, a single device gets two writers competing for the same lease *name* but holding different lease *identities* — the lease prevents simultaneous writes inside one process but not across two.

**Approach.** Make the topology choice exclusive in two places:

1. **Controller side.** When `aggregator.enabled=true` (chart value, propagated as a controller flag/env), the CiscoDevice controller skips Deployment creation for devices whose `spec.driver` has a registered config driver. Apphosting-only devices (no config-driver registration) still get a pod — that's the per-pod-apphosting path.
2. **VK pod side.** A new env `DISABLE_IN_POD_CONFIG_RECONCILER=true`, set by the controller when in aggregator mode for those devices that *do* still get a pod (apphosting-only). When set, `cisco-vk run` starts no ConfigReconciler — apphosting only.

The chart's `aggregator.enabled` is the single switch.

Lease identity also gains a topology suffix (`aggregator-pod` vs `vk-pod-<podName>`) so a misconfigured cluster (both topologies set true) at least loses the second writer's apply attempts loudly via lease-conflict events instead of silently double-writing.

**Acceptance.**
- Unit test: controller in aggregator mode + config-driver-registered device → no Deployment created.
- Unit test: controller in aggregator mode + apphosting-only device → Deployment created with `DISABLE_IN_POD_CONFIG_RECONCILER=true`.
- Live retest: enable aggregator mode in the lab kind cluster, observe that the per-device VK pod is gone for the FAKE-driver-registered case, and the aggregator's in-process worker is the sole writer.
- Document the topology choice in `docs/CONFIGURATION.md`.

**Effort.** ~2 engineer-days code, ~1 engineer-day live retest, ~0.5 engineer-day docs.

#### 1D. Aggregator Helm RBAC (Finding 4)

**What.** `aggregator.go` uses `FamilyLeaser` and `ConfigReconciler.Run` against a controller-owned client. The chart's controller ClusterRole lacks `coordination.k8s.io/leases` and the scope CRDs (`IOSXEConfigDefaults`, `IOSXEDeviceGroupConfig`, `IOSXEInterfaceGroupConfig`, `IOSXETemplate`).

**Approach.** Two acceptable paths; the project should pick one explicitly.

- **Path A (preferred).** Make aggregator a fully-supported topology. Extend the controller's `+kubebuilder:rbac` markers in `internal/aggregator/` to cover leases and the scope CRDs. Regenerate `role.yaml`. Add a chart-rendered RBAC test asserting these are present when `aggregator.enabled=true`.
- **Path B.** Mark aggregator experimental in `values.yaml` and `values.schema.json`, gate it behind a Helm value with a "this is experimental" warning, and document the manual RBAC steps for opting in.

**Recommendation.** **Path A**, packaged with 1C above. The aggregator is a real architectural option (single-manager topology, lower resource footprint at fleet scale) and shouldn't ship as a half-supported feature.

**Acceptance.**
- Chart RBAC includes the missing rules when `aggregator.enabled=true` (the controller-gen output is conditional on a build tag, OR the chart unconditionally grants the broader set with `aggregator.enabled` simply gating the *behaviour*; the second is simpler).
- New `.github/workflows/smoke.yml` test path: `helm install --set aggregator.enabled=true`, apply CRs, assert the in-process worker reconciles. (Adds a 2nd matrix dimension to the smoke job.)
- A scope-objects-backed CR (one with `IOSXEConfigDefaults` populated) reconciles successfully under aggregator mode.

**Effort.** ~1 engineer-day code, ~1 engineer-day smoke-workflow extension.

---

### Wave 2 — production-path consistency

#### 2A. Replay annotation cleanup RBAC (Finding 5)

**What.** VK ClusterRole grants `get/list/watch` on `iosxeconfigs` only. `recordResult` patches the CR to remove `config.cisco.vk/replay-from-log` after a successful replay; that patch returns 403.

**Approach.** Add `patch` (and `update`, since some controller-runtime client paths upgrade patches to updates internally) to the VK ClusterRole's `iosxeconfigs` rule. Mirror what's already on `iosxeconfigs/status`.

**Acceptance.**
- `helm template` shows `patch` and `update` on `iosxeconfigs` for the VK SA.
- New unit test or integration test: trigger a replay, assert annotation is gone after success.
- Live retest in the existing smoke loop.

**Effort.** ~0.5 engineer-day.

#### 2B. YANG defaulting/validation in production reconciler (Finding 6)

**What.** `ConfigReconciler.Reconcile` constructs `intent.Resolver{Client, KeyRules}`, missing `SupportedYANGVersions` and `DefaultYANGVersion`. The polling/aggregator paths set them; the production controller-runtime path doesn't.

**Approach.** One-line fix: add the two fields to the resolver in `Reconcile`, mirroring `reconcileAll`.

**Acceptance.**
- `internal/provider/config_reconciler_controller.go` resolver construction matches the polling-path version.
- Unit test: an `IOSXEConfig` with `spec.targetYangVersion` outside the supported set is rejected with the same error in both paths.

**Effort.** ~0.5 engineer-day.

#### 2C. Multi-family conflict status (Finding 7)

**What.** `recordResult` queries `conflicts[familiesKey(cr)]` where `familiesKey` returns `cr.Spec.ManagedFamilies[0]`. CRs whose conflict is on a non-first family report `Conflict=False` falsely.

**Approach.** Iterate every entry in `cr.Spec.ManagedFamilies`, accumulate all owners across all conflicting families, deduplicate, and emit a single `Conflict=True` condition with the aggregated message. The status setter already exists; only the lookup loop changes.

**Acceptance.**
- Unit test: CR claims `[system, vlan]`, second CR claims `[vlan]` → first CR's `Conflict` condition lists the second CR.
- Unit test: CR claims `[system, vlan]` and overlaps on both → message lists both owners (deduped if same).

**Effort.** ~0.5 engineer-day.

#### 2D. Secret watch (Finding 11)

**What.** `SetupWithManager` watches ConfigMap but not Secret; `spec.secretRefs[]` rotations don't trigger reconcile.

**Approach.** Add a `Watches(&corev1.Secret{}, mapAll)` to the controller-runtime builder. The existing `mapAll` enqueues every IOSXEConfig in the cluster; an indexer keyed on `spec.secretRefs[].name + namespace` would be tighter but adds complexity. Start with the broad map (consistent with the existing ConfigMap pattern), refine to indexed mapping in a follow-up if the wake volume becomes a problem.

VK ClusterRole already includes `secrets` get/list/watch; no chart change needed.

**Acceptance.**
- Unit test: Secret update enqueues every IOSXEConfig that references it.
- Live retest: rotate a secret in the lab cluster, observe an immediate reconcile in the cisco-vk pod logs.

**Effort.** ~0.5 engineer-day.

---

### Wave 3 — day-0 fleet constructs

#### 3A. Bundle selector membership watch (Finding 9)

**What.** Bundle controller's `SetupWithManager` watches only `IOSXEConfigBundle` and owned `IOSXEConfig` children. New CiscoDevices matching a bundle's selector — and label changes that move a device in or out of selector membership — don't trigger fan-out/prune until something else requeues the bundle.

**Approach.** Watch `CiscoDevice` with a `mapDeviceToBundles` mapping function:

```go
.Watches(&ciskov1.CiscoDevice{}, handler.EnqueueRequestsFromMapFunc(r.mapDeviceToBundles))
```

The mapper lists bundles in the device's namespace, evaluates each bundle's selector against the device's labels (and against the *old* labels on update events to catch leaving), enqueues every bundle whose selector now matches OR previously matched.

For namespace-scoped clusters with high churn, an indexer on `IOSXEConfigBundle.spec.deviceSelector` could narrow this — defer until the broad mapper proves too noisy.

**Acceptance.**
- envtest (added in Wave 5 §1B): create a bundle with selector `role=access`, create a CiscoDevice with that label, assert child `IOSXEConfig` appears within the test's tick budget.
- envtest: change the device's label so the selector no longer matches, assert child is pruned.

**Effort.** ~1 engineer-day code, ~1 engineer-day envtest setup (which also helps Wave 5).

#### 3B. Bundle CRD `template.deviceRef` schema relaxation (Finding 10)

**What.** Generated CRD requires `template.deviceRef` even though the bundle controller fills it in `upsertChild`. Operators authoring a selector-based bundle have to write a dummy `deviceRef` to clear admission.

**Approach.** Two options, both clean:

- **Option A.** Introduce a `IOSXEConfigBundleTemplate` type that mirrors `IOSXEConfigSpec` minus `DeviceRef`, used only as `Bundle.spec.template`. Standalone CRs keep requiring `DeviceRef`.
- **Option B.** Mark `DeviceRef` as `// +kubebuilder:validation:Optional` on the `IOSXEConfigSpec` itself, and add a CEL `XValidation` (or webhook validation) that enforces "required when not used as a bundle template". CEL is now stable in K8s 1.30+ which our chart targets.

**Recommendation.** **Option A**. CEL is fine but adds runtime cost; a separate type is structurally clearer and has zero runtime overhead. Conversion-webhook plan (`crd-v1-promotion-plan.md`) anticipates v1 shape changes anyway — fold this in.

**Acceptance.**
- A bundle manifest with no `template.deviceRef` passes admission.
- A standalone IOSXEConfig with no `deviceRef` is rejected.
- CRD round-trip property test (per `crd-v1-promotion-plan.md` §7) covers the new template type.

**Effort.** ~1 engineer-day code (new type + DeepCopy gen), ~0.5 engineer-day tests.

---

### Wave 4 — lifecycle correctness

#### 4A. configPrereqs deletion semantics (Finding 8)

**What.** `reconcileConfigPrereqs` deletes the owned `IOSXEConfig` immediately when `spec.configPrereqs` is removed. There's no finalizer, no `pruneOnRelinquish`, and no empty-intent reconcile. The API/RFC promise that deletion reverts prereq families on the device is not implemented.

**Approach.** This is an **architectural decision**, not just a bug fix. Two viable shapes:

- **Shape A.** Implement real cleanup. Set `pruneOnRelinquish=true` on the controller-owned IOSXEConfig (already supported in the engine since Phase 4). On removal of `spec.configPrereqs`:
  1. Set the owned CR's `spec.configuration` to empty intent.
  2. Wait (via finalizer + status gate) for the engine to apply the empty intent — i.e., revert the prereq families on the device.
  3. Then delete the CR.
- **Shape B.** Document that prereq teardown leaves device state in place. Operator is responsible for explicit cleanup. Update the API comments and `architectural-review.md` to match.

**Recommendation.** **Shape A**, since the API field `pruneOnRelinquish` is already in the codebase precisely for this case and the user-mental-model match ("delete = revert") is the safer default. Shape B is a fallback if Shape A's finalizer + wait dance proves too operationally fragile.

**Acceptance.**
- Unit test: deletion path sets empty intent, waits for state, then removes.
- Live retest: apply a CiscoDevice with prereqs that bring up a VPG, delete the CiscoDevice, assert the VPG is gone from device's running-config.
- Update `examples/gitops-reference/devices/edge-01/ciscodevice.yaml` documentation.

**Effort.** ~2 engineer-days code, ~1 engineer-day tests + live retest.

---

### Wave 5 — gNMI schema-awareness + test reliability

#### 5A. gNMI keyed paths (Finding 12)

**What.** `parseGNMIPath` guesses list keys: `name` for strings, `id` for numbers. Many writers use `tag`, `prefix`, `first`, composite interface type+name shapes, etc. gNMI Set/Delete paths are wrong for any family whose YANG list keys aren't `name`/`id`.

**Approach.** Move path conversion out of the transport-blind parser and into the writer/path metadata layer.

- The `families.yaml` index already carries family-level metadata. Add a `gnmiKeys` field (list-of-list-of-strings) that names each list level's key fields explicitly.
- `transport.Op.Path` becomes structured: `transport.PathSpec{Segments []PathSegment}` where each segment carries its keys as a typed map, not a string.
- `parseGNMIPath` is retired for the writer-driven path; it stays as a utility for the lint tool's offline mode (which has no schema).

This is a wider change than the other findings — it's a transport-API refactor.

**Acceptance.**
- Every family's gNMI Set produces a path that round-trips through the device successfully (against the lab Cat9K, port 6030 if available).
- Cross-validation test corpus (`internal/drivers/iosxe/configdriver/intent/merge_cross_validation_test.go`) gains gNMI-path round-trip cases.
- `cisco-vk-config-lint --offline` continues to work using the legacy `parseGNMIPath` for lint-only consumers.

**Effort.** ~5 engineer-days. The largest single line item in this plan; can run in parallel with Waves 1–4.

#### 5B. Registry test reliability (Finding 13)

**What.** `internal/drivers/registry_test.go` calls `t.Parallel()` while `resetRegistry` swaps the package-global registry. `go test -race -count=20` reproduces immediately. The "race suite green" claim in the implementation-status doc is therefore fragile.

**Approach.** Two-part fix:

1. **Drop `t.Parallel()`** from tests in `registry_test.go` that call `resetRegistry`. Keep `t.Parallel()` on the few tests that don't mutate the registry (a handful read-only assertions).
2. **Audit other parallelised files** for the same antipattern. The Wave-1 t.Parallel() sweep was done with a curated whitelist; revisit it. `internal/drivers/placeholders_test.go` and `internal/drivers/iosxe/configdriver/transport/netconf_framing_test.go` are candidates worth re-checking.

**Acceptance.**
- `go test -race -count=20 ./internal/drivers` passes 20-of-20 runs.
- Same for the full module: `go test -race -count=5 ./...` passes 5-of-5.
- Implementation-status §9 numerical baseline gets a `race-detector reproducibility` line.

**Effort.** ~0.5 engineer-day.

---

## 4. Status doc walk-back (immediate)

Before any of Waves 1–5 land, [`implementation-status.md`](implementation-status.md) needs a correction. Specifically:

- **§1 Bottom line.** Replace "shippable today under the per-pod topology" with: "shippable today for **day-0 apply + report-mode drift detection** under the per-pod topology. **Day-2 continuous-reconcile, transactional apply, and aggregator topologies are not yet ready** — see [`external-review-response.md`](external-review-response.md) for the gating findings and remediation plan."
- **§5 Live-verification status.** The row "*per-device IOSXEConfig drift report*" is honest, but a new row must say steady-state drift after `InSync` has not been verified (because of Finding 2 — it would not work if tested).
- **§7 Pending work** gains a Wave 1 / Wave 2 / Wave 3 / Wave 4 / Wave 5 register that mirrors §3 of this doc.
- The architectural-review §6 watch-item table is unchanged — those items remain closed; the *new* findings here are at a different level (semantic API/code mismatch, not architectural review items).

This walk-back is a separate, immediate commit; it goes in **before** Wave 1 starts so anyone consulting the status doc gets the corrected view.

---

## 5. Sequencing and effort

Total estimate: **~25 engineer-days** for the full plan, achievable by 1 engineer in ~5 weeks or 2 engineers in ~3 weeks. Wave-by-wave:

| Wave | Scope | Engineer-days | Gating |
|---|---|---|---|
| 1 | P1 semantic gaps (1A–1D) | ~10 | Required before "day-2 ready" claim |
| 2 | Production-path consistency (2A–2D) | ~2.5 | Required before "day-2 ready" claim |
| 3 | Day-0 fleet constructs (3A–3B) | ~3.5 | Required before bundle/selector workflow is honestly advertised |
| 4 | configPrereqs deletion semantics (4A) | ~3 | Required to honour API/docs promises |
| 5 | gNMI rework + test reliability (5A–5B) | ~5.5 | Required before gNMI is broadly recommended; test reliability is immediate |

Recommended execution order (parallelism in parens):
1. **Status walk-back** (~0.5d, immediate, single PR).
2. **Wave 5B test reliability** (~0.5d, immediate, single PR — restores the race-detector evidence the rest of the plan leans on).
3. **Wave 1** in series (1A and 1B can land independently; 1C and 1D should land together).
4. **Wave 2** in parallel with Wave 1 (each item is small and self-contained).
5. **Wave 3** after Wave 1C+1D (depends on the topology decisions there).
6. **Wave 4** after Wave 1B (drift detection has to be honest before deletion can wait on it).
7. **Wave 5A** in parallel with everything (independent surface area).

---

## 6. What "shippable for day-2" means after this plan

Concrete acceptance criteria for re-claiming day-2 readiness:

1. `go test -race -count=20 ./...` passes 20-of-20 (Wave 5B).
2. Live smoke against the lab Cat9K demonstrates:
   - **Steady-state drift detection.** Out-of-band device change is detected within `spec.driftDetectInterval` (Wave 1B).
   - **Transactional apply via NETCONF.** Mid-apply error rolls back fully; `show running-config` mid-apply doesn't show partial state (Wave 1A).
   - **`writeStartup`.** Post-apply `show running-config | include ^! Last config` reflects the save (Wave 1A).
   - **configPrereqs deletion reverts device state** (Wave 4A).
   - **Bundle selector live-update.** New CiscoDevice with matching label triggers fan-out within one reconcile tick (Wave 3A).
   - **Aggregator mode is exclusive with per-pod ConfigReconciler.** No duplicate writers under any combination of `aggregator.enabled` + pod presence (Wave 1C).
3. CI smoke workflow includes:
   - `aggregator.enabled=true` matrix dimension (Wave 1D).
   - Replay-annotation cleanup assertion (Wave 2A).
   - Bundle selector watch assertion (Wave 3A).
4. Status doc §1 returns to a "shippable" claim, but with the day-0/day-2 distinction explicit.

When all six items above are green, the implementation-status doc can re-claim "shippable for day-2 under the per-pod topology" — and not before.

---

## 7. What this plan does NOT address

Out of scope, intentionally:

- **CRD v1 promotion.** Tracked in [`crd-v1-promotion-plan.md`](crd-v1-promotion-plan.md). Independent of this remediation plan.
- **Log unification.** Tracked in [`log-unification-plan.md`](log-unification-plan.md). Independent.
- **Phase-8 external residuals.** Tracked in [`phase-8-residuals.md`](phase-8-residuals.md). Independent.
- **Cosmetic relocation to `internal/configdriver/`.** Phase-10 placeholder per the architectural review. Conflict-prone with this plan; defer until Wave 5 lands.
- **CRD v1's overlap with §3B (bundle template).** The CRD-v1 PR will fold the bundle-template type change in. This response RFC names it; the v1 PR implements it.

---

## 8. Process notes

- This document is a **plan** at the request of the branch author. It does not change code. It does change one document — [`implementation-status.md`](implementation-status.md) §1 walk-back, per §4 above — which lands as a single small commit alongside this RFC.
- Each Wave is intended to be a separate PR, reviewable on its own. Wave 1A and 1B are large enough they may split further at PR time.
- Live retest of the lab Cat9K is in scope for the engineer executing each Wave; the kind-lan-bridge tooling (`scripts/dev/kind-lan-bridge.sh`) makes that achievable from a developer laptop.
- The external review's bottom line — "the fastest path to readiness is to close the P1 semantic gaps and then add integration tests that exercise the exact operator workflows the RFCs advertise" — is the operating principle of this plan. Wave 1 closes the P1s; integration tests live alongside each Wave's code.
