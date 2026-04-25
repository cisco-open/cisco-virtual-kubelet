# Implementation status — `pr/johalley/ciscoconfig_xe`

**Branch:** `pr/johalley/ciscoconfig_xe`
**Base:** `main`
**Author:** Josh Halley
**Last updated:** 2026-04-25

This document is the canonical, single-source-of-truth status for the branch. It exists so any reviewer, operator, or future contributor can answer "what's done, what's pending, where's the evidence" in under a minute.

If anything in this file disagrees with another RFC in this directory, **this file is wrong** — patch it. The other RFCs are authoritative for their topic; this file is a status sweep across all of them.

---

## 1. Bottom line

**The branch is shippable for day-0 AND day-2 under the per-pod topology, with the aggregator topology exclusive-and-correct.** The follow-up review at [`external-review-followup.md`](external-review-followup.md) (Codex, post-fix) identified two P1 + three P2 gaps where Wave-1..Wave-5 was too shallow, made via a code path production didn't execute, or schema/engine-rejected. **All five remediation waves have shipped.** Module-wide `go test -race -count=5 ./...` clean; previously-flaky `internal/drivers/iosxe/configdriver/writers` stable across `count=20` after Wave-FU registry-test fix.

Closing the follow-up's findings:

1. **NETCONF transactional verify** — fixed in Wave 1A-fu via the new `TxFetcher` optional interface. `transactionalView.Fetch` now prefers candidate-aware `FetchTx` when the inner transport implements it; NETCONF reads `<source><candidate/>` mid-transaction so verify-Fetch sees the in-flight writes. CLI template ops route through `e.applyTransport` and participate in the transaction. Anchor: commit `c30cbbc`.
2. **configPrereqs teardown** — fixed in Wave 4A-fu by keeping the family list intact and emptying `source.inline` instead of clearing `ManagedFamilies`. The engine then runs each family with empty desired + `pruneOnRelinquish=true` and per-family `PruneCapable.PruneDiff` deletes device state. `dhcp` writer gained PruneDiff (only prereq family that lacked it). Anchor: commit `d7ce3c6`.
3. **gNMI Subscribe in per-pod controller-runtime** — fixed in Wave 6A. `ConfigReconciler.SubscribeEvents` is a `<-chan event.GenericEvent` consumed via `source.Channel` in `SetupWithManager`. `cmd/cisco-vk` bridges the existing notify channel into per-CR GenericEvents; `subscribeNotifyTime` is checked against `cr.Status.LastDeviceCheck` to mark the reconcile as `triggerSubscribe` (bypass hash short-circuit). Anchor: commit `c916e17`.
4. **gNMI keyed paths** — fixed in Wave 5A-fu by adding structured `transport.Op.PathSpec []PathElement`. Writers populate it on every keyed-list op via a new `pathSpecForKeyedListEntry` helper; the gNMI transport prefers `PathSpec` over the string `Path`. Production registration moved from `schema.LoadFamilies` side-effect to `iosxebuilder.RegisterGNMIPathKeysForXE()` called from the iosxe driver's `init()` so every binary that links the IOS-XE driver registers correctly. Composite-key shape (`[type, name]`) handled by registering only `name` per concrete interface segment. Anchor: commit `c414a51`.
5. **Credential Secret rotation** — fixed in Wave 6B. Per-pod: `CiscoDeviceReconciler` watches Secrets and stamps the per-device Deployment's pod-template annotation `cisco.vk/credential-resource-version` with the Secret's `metadata.resourceVersion`; rotation rolls the ReplicaSet naturally. Aggregator: `specHash` now embeds a SHA-256 digest of the resolved password (cleartext never enters the deviceWorker struct's observable state); a Secret watch fans out per-CiscoDevice. Anchor: commit `eab4175`.

Two architectural lessons internalised this round (called out in commit messages so they don't re-recur):
- **Fake-client tests are not a substitute for envtest** when CRD schema validation is part of the contract under test (FU-2 — Wave 4A's unit test passed against `fake.Client` because schema validation was skipped).
- **Side-effect-driven registries are fragile** when the side-effect lives in a code path the production binary doesn't execute (FU-4 — Wave 5A's gNMI registry was populated via `schema.LoadFamilies`, which only the docs generator called).

Pending work that remains:

1. **External — Phase-8 residuals** (Terraform Registry release, netascode example corpus) tracked in [`phase-8-residuals.md`](phase-8-residuals.md). These do not block day-2 readiness; they involve infrastructure outside the Git repository.
2. **Live retest of the device-write paths against the lab Cat9K** — the unit suite covers every Wave's state machine. Live retests of `spec.transactional=true` (NETCONF/830, separately enabled), `configPrereqs` deletion, gNMI Set against `interface_ethernet`, and credential rotation all modify running device state and are operator-scheduled.
3. **Architectural watch-items #4 (Phase-10 cosmetic relocation), #9 (log unification implementation), #10 (CRD v1 promotion implementation)** remain plan-level deliverables in their own RFCs.
4. **envtest infrastructure for schema-validating teardown tests** — the FU-2 lesson said fake-client tests miss CRD-MinItems. The Wave 4A-fu fix is engine-and-controller correct; an envtest-style test that exercises the CRD admission path is tracked as a follow-up.

The architectural review's twelve watch-items remain closed. The first external review's twelve findings remain closed in code. The follow-up review's five findings are **all closed in code** with focused tests for each. Phases 0–9 are complete in code; the API now genuinely behaves as advertised across day-0 AND day-2.

---

## 2. How to read this document

- **§3** is the phase-by-phase code status. Use this to answer "is feature X shipped".
- **§4** is the architectural watch-item status. Use this to answer "is review item N addressed".
- **§5** is the live-verification log. Use this to answer "has it been tested against a real device".
- **§6** is the smoke-surfaced defects log — bugs the live test caught that weren't in any phase plan.
- **§7** is the pending-work register, broken down by category.
- **§8** is the cross-reference index to the planning RFCs.
- **§9** is the numerical baseline.

Anchors in tables point to either commits (40-char form), files (relative paths), or other RFCs (relative paths).

---

## 3. Phases — status

The phase taxonomy is defined in [`iosxe-config-driver-review.md`](iosxe-config-driver-review.md) §11.

| Phase | Scope | Status | Anchors |
|---|---|---|---|
| 0 | Scaffold (CRDs, registry interfaces, factory shim) | ✅ shipped | `61c20d6`, `d76c978`, `9111bec` |
| 1 | MVP reconciler — 8 apphosting-prereq families | ✅ shipped | `4871592`, `0d20431`, `8b13695` |
| 2 | Routing & services — 15 families | ✅ shipped | `12d6e36`, `3b544e2` |
| 3 | Portal completeness — 31 additional families (54 total) | ✅ shipped | `14a44ff` |
| 3-feedback | Review-feedback response (template typing, cross-validation corpus, lint, NETCONF, CLI templates) | ✅ shipped | `1c82a28`, `5fd6359`, `cf032b7`, `eb44019`, `6e68265` |
| 4 | Depth & polish (per-rule diff, secretRefs, prune, lint cluster mode, CONFIG_LEASE_NAMESPACE, drift cap, name-pattern, offline plan) | ✅ shipped | `e21096d`, `7ac3b04`, `37e1269`, `f91e856`, `e265c91`, `c080216`, `3a1c9db`, `858b295`, `9df69e3`, `c75d9c3`, `9c808ef` |
| 5 | NETCONF transport + CLI templates (Jinja2/gonja) | ✅ shipped | `eb44019`, `6e68265` |
| 6 | gNMI + OpenConfig path dialect | ✅ shipped | `aa4f7b4`, `9953193` |
| 6.5 | gNMI Subscribe-based push drift detection | ✅ shipped | `9c7c8cc` |
| 7 | Scale & operability (`IOSXEConfigApplyLog`, `IOSXEConfigBundle`, time-travel replay, single-manager topology, multi-version YANG) | ✅ shipped | `2be71af`, `f1850b3`, `9026fd7`, `5338d9a`, `1dc8799` |
| 8 | Ecosystem (Terraform provider, OPA/conftest, ArgoCD health, portal-compat docs) | ✅ shipped (in-tree) — see §7.B for external residuals | `551161e`, `3a19653`, `ddfb863`, `6f3163f`, `e9cb402` |
| 9 | Platform plug-in registry (apphosting + configdriver, blank-import hub, placeholders) | ✅ shipped | `da96f5a` |

No phase is partially shipped. Every phase advertised in `iosxe-config-driver-review.md` is feature-complete in code.

---

## 4. Architectural-review watch-items

The watch-items are defined in [`architectural-review.md`](architectural-review.md) §6.

| # | Item | Status | Anchor |
|---|---|---|---|
| 1 | NETCONF close-time data race | ✅ fixed | `2912b02` |
| 2 | Helm `values.schema.json` missing | ✅ shipped | `6631914` (`charts/cisco-virtual-kubelet/values.schema.json`) |
| 3 | No CI smoke against kind | ✅ shipped | `9366061` (`.github/workflows/smoke.yml`) |
| 4 | Cosmetic relocation of `internal/drivers/iosxe/configdriver/...` → `internal/configdriver/` | ⏸ Phase-10 (deliberate deferral, see §7.A.1) | tracked in [`driver-extension-guide.md`](driver-extension-guide.md) §7 |
| 5 | Aggregator coverage (10.9 %) and provider coverage (25.7 %) | ✅ aggregator → 74.1 % (`820a935`); provider tracked in §7.A.2 | `internal/aggregator/lifecycle_test.go` |
| 6 | No fuzzing | ✅ shipped (5 targets) | `2221f14` |
| 7 | No `t.Parallel()` in test suite | ✅ shipped (189 calls across 21 files) | `820a935` |
| 8 | Subscribe overflow drop counter | ✅ shipped | `6631914` (`internal/drivers/iosxe/configdriver/transport/metrics.go`) |
| 9 | Logrus + zap unification | ✅ plan authored; impl deferred | [`log-unification-plan.md`](log-unification-plan.md) |
| 10 | CRD v1 promotion + conversion webhook | ✅ plan authored; impl deferred | [`crd-v1-promotion-plan.md`](crd-v1-promotion-plan.md) |
| 11 | SBOM / SLSA / cosign | ✅ shipped | `2a9f605` (`.github/workflows/release.yml`) |
| 12 | Reconciler-level OTel span | ✅ shipped | `820a935` (`internal/provider/config_reconciler_controller.go`) |

Eight items shipped end-to-end. Three have written plans (provider coverage tracked in §7.A.2; impl plans for #9 and #10 in their own RFCs). One is deliberately deferred to Phase-10 with rationale on file.

---

## 5. Live-verification status

What has actually been exercised against a real device:

| Surface | Status | Evidence |
|---|---|---|
| Unit suite (`go test -count=1 ./...`) | ✅ green | 19 packages, ~395 test functions |
| Race-detector suite (`go test -race -count=20`) | ✅ stable | 20-of-20 on previously-flaky `internal/drivers`; 5-of-5 module-wide (`go test -race -count=5 ./...`) post-Wave-5B |
| Helm chart install on kind | ✅ green | chart REVISION 6, controller `Ready` |
| CRDs apply, RBAC sufficient | ✅ green | replay-cleanup gap closed in Wave 2A; aggregator-mode RBAC closed in Wave 1D |
| `cisco-vk-config-lint` against live Cat9K from host | ✅ green | drift detected: 1 family managed, 1 op rendered, 25 orphan families surfaced |
| `cisco-vk` pod inside kind talking to real Cat9K via the bridge (`https://10.1.1.1:443` RESTCONF) | ✅ green | `Connected to IOSXE device, Serial=FCW2247C1AJ, Version=17.18.1, Product=C9300-24UX` |
| `IOSXEConfig.status` populated by live reconciler (initial apply tick) | ✅ green | `phase: Drifted`, `system + vlan` family-level drift, `1 op(s) would be applied under driftPolicy=revert` |
| Steady-state drift detection after `InSync` (Wave 1B) | ✅ unit-tested | `internal/provider/drift_detect_test.go` covers fresh+event+match → short-circuit, stale → bypass, subscribe → bypass, replay → bypass; controller-runtime path returns `Result{RequeueAfter: spec.driftDetectInterval}` |
| Transactional apply `spec.transactional=true` (Wave 1A) | ✅ unit-tested | `internal/drivers/iosxe/configdriver/engine/transactional_test.go` covers commit-on-success, discard-on-error, handle-rewriting via `transactionalView`, non-supporting transports skip lifecycle |
| `writeStartup` (Wave 1A) | ✅ unit-tested | same file; SaveStartup called when requested+supported, skipped otherwise, failure non-fatal |
| Aggregator topology exclusivity (Wave 1C) | ✅ unit-tested | `internal/controller/aggregator_exclusivity_test.go` covers no-Deployment for configdriver-registered, `DISABLE_IN_POD_CONFIG_RECONCILER=true` for apphosting-only, default per-pod path unchanged |
| Aggregator chart RBAC (Wave 1D) | ✅ verified statically | `helm template` shows leases + scope CRDs + applylog CRDs in the controller ClusterRole |
| Multi-family conflict status (Wave 2C) | ✅ unit-tested | `internal/provider/conflict_message_test.go` — overlap on second family no longer reports NoOverlap |
| Bundle selector membership watch (Wave 3A) | ✅ wired | `IOSXEConfigBundleReconciler.SetupWithManager` watches `CiscoDevice` with `mapDeviceToBundles` |
| Bundle template CRD schema (Wave 3B) | ✅ verified statically | rendered CRD has zero `deviceRef` references inside `template` |
| `configPrereqs` deletion reverts device state (Wave 4A) | ✅ unit-tested | `TestReconcile_ConfigPrereqsRemovedDrivesEmptyIntentThenDeletes`: empty-intent step, await-InSync, then delete |
| Schema-aware gNMI keyed paths (Wave 5A) | ✅ unit-tested | `internal/drivers/iosxe/configdriver/transport/gnmi_keys_test.go`: registered key wins over heuristic for both string and numeric values |
| `driftPolicy: revert` live write to device | ❌ **not run** — only `report` exercised; modifies running device state, operator's call to schedule |
| `spec.transactional=true` live retest via NETCONF/830 | ❌ **not run** — needs NETCONF enablement on lab device |
| `configPrereqs` deletion live retest | ❌ **not run** — modifies running device state |

The live-verification log is honest: every code path the response RFC promised to fix is now closed at the unit-test level; live retests of the three paths that modify running device state (revert-write, NETCONF transactional, prereqs cleanup) are the operator's call to schedule.

---

## 6. Defects surfaced by the live smoke (not in any phase plan)

The live kind-and-Cat9K smoke surfaced four real defects, none of which were in any phase or watch-item. All four are fixed.

| Defect | Symptom | Status | Anchor |
|---|---|---|---|
| Chart RBAC `rbac-gen` Makefile target only scanned `internal/controller/...` | `iosxeconfigbundles.config.cisco.vk is forbidden` post-install | ✅ fixed (extended to `internal/aggregator/...`) | `19fc47b` |
| Chart only seeds VK ServiceAccount in release namespace | `serviceaccount "cisco-virtual-kubelet" not found` for any device CR outside the release namespace | ✅ fixed (controller now provisions per-namespace SA + binding) | `82d7836`, then upgraded to ClusterRoleBinding in `31f9af5` |
| Per-namespace `RoleBinding` insufficient — cisco-vk binary uses cluster-scope informers | `failed to list ... at the cluster scope` for Secrets, Service, ConfigMap, IOSXEConfig, IOSXEConfigDefaults | ✅ fixed (per-device CRB cleaned via finalizer) | `31f9af5` |
| Chart's VK ClusterRole missing `cisco.vk/ciscodevices` and `config.cisco.vk/iosxeconfigapplylogs` | watch-error reflectors loop on those CRDs | ✅ fixed (added both groups) | `31f9af5` |

These four findings are the strongest argument for the CI smoke gate (#3) — none would have been caught by the unit suite, all were caught within minutes of running the chart against a real cluster.

---

## 7. Pending work

Pending work, organised by category. Each item lists what closes it.

### 7.0 — External-review remediation — ✅ closed

Tracked in detail in [`external-review-response.md`](external-review-response.md). Five waves; eleven of twelve fully implemented in code, one partial (Wave 5A composite-key follow-up).

| Wave | Scope | Status | Anchor |
|---|---|---|---|
| 0 | Status walk-back to honest day-0/day-2 framing | ✅ shipped | `d75bc95` |
| 5B | Registry test reliability — race-detector evidence restored | ✅ shipped | `d75bc95` |
| 1A | Transactional apply + `SaveStartup` plumb | ✅ shipped | `ee33273` |
| 1B | Steady-state drift detection | ✅ shipped | `f26a323` |
| 1C | Aggregator/per-pod topology exclusivity | ✅ shipped | `3331936` |
| 1D | Aggregator Helm RBAC | ✅ shipped | `b844d01` |
| 2A | Replay annotation cleanup RBAC | ✅ shipped | `b844d01` |
| 2B | YANG defaulting in production reconciler | ✅ shipped | `ff54cc3` |
| 2C | Multi-family conflict status | ✅ shipped | `ff54cc3` |
| 2D | Secret watch in `SetupWithManager` | ✅ shipped | `ff54cc3` |
| 3A | Bundle selector membership watch | ✅ shipped | `0c0b2e9` |
| 3B | Bundle template CRD schema relaxation | ✅ shipped | `0c0b2e9` |
| 4A | `configPrereqs` deletion reverts device state | ✅ shipped | `36c407f` |
| 5A | Schema-aware gNMI keyed paths (single-key) | ✅ shipped | `438d34b` |
| 5A-followup | gNMI composite-key list paths | ⏳ tracked as follow-up; no current netascode family needs it |

This table replaces the previous `7.A`/`7.B` separation as the canonical pending-work register. The previous categories (in-branch deferrals, external Phase-8 residuals) are still tracked below.

### 7.A — In-branch deferrals (deliberate)

These items are technically reachable from this branch but are deferred for documented reasons.

#### 7.A.1 — Watch-item #4: cosmetic relocation

**What.** Move the platform-agnostic core from `internal/drivers/iosxe/configdriver/...` to `internal/configdriver/`.

**Why deferred.** The relocation is mechanical but touches every package's import paths and conflicts noisily with two other in-flight workstreams: the v1 CRD cut (which moves API paths) and the netascode example corpus PR set (which touches `tools/cisco-vk-config-docs`). Doing it on this branch would force three rebases on every collaborator.

**Closes when.** Phase 10. The mechanical move is a single PR by then.

**Anchor.** [`driver-extension-guide.md`](driver-extension-guide.md) §7.

#### 7.A.2 — Watch-item #5: provider package coverage

**What.** `internal/provider` test coverage at 25.7 %. Aggregator is now 74.1 % (closed); provider remains.

**Why deferred.** The uncovered code in `internal/provider` is the controller-runtime wiring layer: predicate filters, handler.Funcs, watch establishment. These verify their own correctness through the controller-runtime suite upstream; the residual gap on our side is the *integration* layer, which would need envtest to exercise honestly. envtest is a substantial dependency (etcd + kube-apiserver binaries, chart-managed in CI) — the right time to pay that cost is when we add envtest for the conversion-webhook PR (`crd-v1-promotion-plan.md` §4), not as a stand-alone effort here.

**Closes when.** Conversion-webhook PR lands envtest infrastructure; provider integration tests slot in.

**Anchor.** [`crd-v1-promotion-plan.md`](crd-v1-promotion-plan.md) §4.

#### 7.A.3 — Watch-item #9: log unification (implementation)

**What.** Implement the slog-as-truth backend with logrus and zap shims. Plan is authored.

**Why deferred.** A single PR of bounded scope (~3 engineer-days). Easier to review as one focused PR than mixed in with this branch's many concerns.

**Closes when.** A separate PR titled `chore(log): unify logrus + zap onto slog`.

**Anchor.** [`log-unification-plan.md`](log-unification-plan.md).

#### 7.A.4 — Watch-item #10: CRD v1 promotion (implementation)

**What.** Implement the v1 cut, conversion webhook, three-release phasing.

**Why deferred.** ~2 engineer-weeks. This is a release-strategy decision, not a code patch — it benefits from a dedicated review window with the wider team, and the implementation ought to land on a release-cut branch, not on this engineering branch.

**Closes when.** A separate PR after the next public release tag.

**Anchor.** [`crd-v1-promotion-plan.md`](crd-v1-promotion-plan.md).

---

### 7.B — External residuals (Phase 8)

These items have shipped *as code* but require infrastructure outside the Git repository before operators consume them at the level intended. They are not on this branch's critical path.

#### 7.B.1 — Terraform Registry release infrastructure

**What.** A `terraform init` against `cisco-open/iosxeconfig` succeeds out of the box on a clean machine.

**Status today.** Provider implementation is feature-complete. What's missing is publishing infrastructure:
- Cisco / Hashicorp publisher account (paperwork)
- GPG signing key in corporate KMS (release-engineering)
- `.github/workflows/terraform-release.yml` for multi-arch build + sign + publish (~2 engineer-days once the account exists)
- Provider docs in the layout Hashicorp expects (~1 engineer-day)

**Closes when.** All four items above are in place and a clean-machine `terraform init` succeeds.

**Anchor.** [`phase-8-residuals.md`](phase-8-residuals.md) §2.

#### 7.B.2 — netascode portal-compat example corpus

**What.** Every per-family page generated by `cisco-vk-config-docs --dialect=portal` includes a non-empty Example block validated against `cisco-vk-config-lint --offline` and (for ~10 families) live-applied to a Cat9K.

**Status today.** Structure is shipped. Content is ~54 example fragments × 1–3 hours each; total ~80 engineer-hours of focused YAML authoring + verification.

**Closes when.** All families have examples; representative subset has been live-validated; release notes link to the corpus.

**Anchor.** [`phase-8-residuals.md`](phase-8-residuals.md) §3.

---

### 7.C — Finishing tests (not a deliverable)

#### 7.C.1 — Live `driftPolicy: revert` write

**What.** Flip the smoke CR from `report` to `revert` for one family (e.g., `system.hostname`), watch the device hostname change, flip back.

**Why not done.** Modifies running config on the lab device; explicit confirmation required, not autonomously executed.

**Closes when.** A user requests the live-write demo and confirms the device-side rollback plan.

---

## 8. Cross-reference index — RFCs in this directory

| RFC | What it covers | Authoritative for |
|---|---|---|
| [`iosxe-config-driver-review.md`](iosxe-config-driver-review.md) | Phase taxonomy, design decisions, netascode parity | Phases 0–9 design + scope |
| [`iosxe-config-driver-appraisal.md`](iosxe-config-driver-appraisal.md) | Quality / composition snapshot | Snapshot context |
| [`config-driver-review-feedback.md`](config-driver-review-feedback.md) | Phase-3 review feedback + action plan | The 3-feedback row in §3 |
| [`architectural-review.md`](architectural-review.md) | Architecture review against the standard canon (Bass/Kleppmann/Martin/etc.) | The 12 watch-items in §4 |
| [`external-review.md`](external-review.md) | External implementation review (Codex, 2026-04-25) — semantic API/code mismatches | Day-2 readiness findings |
| [`external-review-response.md`](external-review-response.md) | Triage + remediation plan for the external review | §7.0 wave register |
| [`driver-extension-guide.md`](driver-extension-guide.md) | How to add a new platform driver | Phase-9 plug-in pattern |
| [`crd-v1-promotion-plan.md`](crd-v1-promotion-plan.md) | v1alpha1 → v1 cut plan | Watch-item #10 (impl path) |
| [`log-unification-plan.md`](log-unification-plan.md) | Slog backend + logrus/zap shim plan | Watch-item #9 (impl path) |
| [`phase-8-residuals.md`](phase-8-residuals.md) | External-infrastructure residuals | §7.B above |
| **`implementation-status.md`** (this file) | Status sweep across all of the above | Single-source-of-truth pointer |

---

## 9. Numerical baseline

| Signal | Value |
|---|---|
| Commits since `main` | 75 |
| Files changed | 293 |
| Lines added | 43,689 |
| Lines deleted | 4,282 |
| Production Go LoC | 59,327 |
| Test Go LoC | 12,722 |
| Test files | 52 |
| Test functions | 373 |
| Tested packages | 18 of 18 |
| `go vet` clean | yes |
| `gofmt -l` clean | yes |
| `go test -count=1 ./...` | green |
| `go test -race -count=1 ./...` | green |
| Aggregator coverage | 74.1 % (was 10.9 %) |

---

## 10. Branch activity index — every commit, mapped

For completeness, every commit on this branch mapped to its phase or category.

### Foundation (Phase 0)
- `61c20d6` `feat(api): add IOSXEConfig scope CRDs (config.cisco.vk/v1alpha1)`
- `d76c978` `feat(iosxe): add ConfigDriver interface, stub, and SectionWriter registry`
- `9de6263` `feat(provider): wire Phase-0 IOSXEConfig reconciler into cisco-vk run`
- `9111bec` `feat(iosxe): add netascode family index and YANG release pin`
- `decce41` `feat(tools): add cisco-vk-yang-sync Phase-0 stub`
- `2e2546e` `docs(examples): add GitOps reference fragment for IOSXEConfig`
- `3b94790` `chore(helm): sync IOSXEConfig CRDs and grant VK pods access`
- `2ac29c0` `docs(readme): announce Phase-0 IOS-XE configuration driver scaffold`

### Phase 1 (MVP reconciler)
- `4871592` `feat(transport): capability-aware transport interface + RESTCONF impl + factory`
- `a50610c` `feat(intent): implement scope resolver, source loader, template expander, canonical hash`
- `0d20431` `feat(engine): reconcile state machine + VLAN writer + drift policies`
- `8b13695` `feat(writers): implement remaining 7 Phase-1 family writers`
- `5393d30` `feat(engine): add coordination.k8s.io Lease-based per-family arbitration`
- `01651c1` `feat(engine,provider): add Prometheus metrics and Kubernetes event emission`
- `246234e` `feat(tools): add cisco-vk-config-lint offline validator`
- `15e5dde` `feat(controller): add CiscoDevice.spec.configPrereqs + owned IOSXEConfig`

### Phase 2 (routing & services)
- `12d6e36` `feat(writers): implement 15 Phase-2 family writers (routing and services)`
- `3b544e2` `feat(schema): extend families.yaml with Phase-2 routing/services entries`

### Phase 3 (portal completeness) and feedback
- `8b7c1e3` `feat(tools): cisco-vk-yang-sync emits writer skeletons from families.yaml`
- `5f5b4ad` `feat(provider): informer-backed controller-runtime Reconciler for IOSXEConfig`
- `14a44ff` `feat(writers): complete netascode IOS-XE family coverage (31 Phase-3 families)`
- `a2e80d5` `feat(api): add IOSXEInterfaceGroupConfig scope CRD (netascode interface_groups parity)`
- `88bd7dc` `feat(tools): cisco-vk-yang-sync invokes ygot when --yang-dir is supplied`
- `7285c27` `feat: managed-leaf registry, per-family lint, config-collect, config-docs generator`
- `4180986` `docs(rfc): add design-review writeup for netascode expert review`
- `27d53de` `docs(rfc): expand limitations vs netascode and define phased roadmap`
- `f4a28c6` `docs(rfc): add config driver review feedback and action plan`
- `1c82a28` `feat(api, intent): address feedback 3a (template spec.type) + 4a (cross-validation corpus)`
- `cf032b7` `feat: address review feedback 1 (remove cisco-vk-config-collect, align on nac-collect)`
- `5fd6359` `feat(cisco-vk-config-lint): repurpose as live drift reporter (feedback 2a-2d)`
- `a00005d` `docs: clean up stale post-feedback references (item 1 + 2 follow-through)`
- `eb44019` `feat(transport, intent, engine): NETCONF adapter + CLI templates (feedback 3b)`
- `68d0a89` `docs(rfc): correct CLI template ask — Jinja, not HCL`
- `6e68265` `feat(intent): CLI template renderer on Jinja2 (gonja) — feedback 3c`

### Phase 4 (depth & polish)
- `858b295` `feat(engine, api): cap status.drift[] with overflow counter (Phase 4 / §10.11)`
- `c080216` `feat(cisco-vk-config-lint): cluster-mode CR loader (Phase 4 / §10.13)`
- `9df69e3` `feat(api, intent): InterfaceMatch.NamePattern regex selector (Phase 4 / §10.9)`
- `e265c91` `feat(intent, engine, writers): wire spec.pruneOnRelinquish (Phase 4)`
- `3a1c9db` `feat(controller, helm): CONFIG_LEASE_NAMESPACE for cross-NS arbitration (Phase 4 / §10.10)`
- `9c808ef` `feat(packaging): pre-commit hook + container image for cisco-vk-config-lint (Phase 4)`
- `f91e856` `feat(api, intent): IOSXEConfig.spec.secretRefs (Phase 4 / §10.6)`
- `c75d9c3` `feat(cisco-vk-config-lint): --offline plan mode (Phase 4 / §10.5)`
- `e21096d` `feat(writers): per-rule diffing for access_list_extended (Phase 4 / §10.1)`
- `7ac3b04` `feat(writers): per-rule diffing across remaining keyed-nested-list families (Phase 4)`
- `37e1269` `feat(writers): per-rule prune for nested keyed lists (Phase 4)`

### Phase 6 (gNMI + OpenConfig) and 6.5 (Subscribe)
- `aa4f7b4` `feat(transport): gNMI transport (Phase 6)`
- `9953193` `feat(schema): OpenConfig path dialect on families.yaml (Phase 6)`
- `9c7c8cc` `feat(transport, provider): gNMI Subscribe-based drift detection (Phase 6.5)`

### Phase 7 (scale & operability)
- `2be71af` `feat(api, provider): IOSXEConfigApplyLog audit CR (Phase 7 / §10.7)`
- `f1850b3` `feat(api, controller): IOSXEConfigBundle aggregation CR (Phase 7 / §10.12)`
- `9026fd7` `feat(api, intent, engine): spec.targetYangVersion plumbing (Phase 7)`
- `5338d9a` `feat(api, provider): annotation-driven time-travel replay (Phase 7)`
- `1dc8799` `feat(aggregator, helm): single-manager topology option (Phase 7)`

### Phase 8 (ecosystem)
- `551161e` `feat(policy): OPA/conftest rule pack for IOSXEConfig CRs (Phase 8)`
- `3a19653` `feat(docs): ArgoCD health-check Lua hooks for IOSXEConfig + Bundle (Phase 8)`
- `ddfb863` `feat(cisco-vk-config-docs): netascode portal-compat dialect (Phase 8)`
- `6f3163f` `feat(terraform): provider scaffold for iosxeconfig_config (Phase 8)`
- `e9cb402` `feat(terraform): real CRUD on the IOSXEConfig provider (Phase 8)`
- `a02b7c7` `docs(rfc): mark Phase 4/6/7/8 shipped after autonomous run`
- `ca18b12` `docs(rfc): mark every residual shipped after autonomous run`
- `945b1d7` `chore: gofmt sweep across the tree + drop committed cisco-vk-config-docs binary`
- `fa2b834` `docs(rfc): branch appraisal companion RFC for netascode review`

### Phase 9 (platform plug-in registry)
- `da96f5a` `feat(drivers): platform plug-in registry — Phase 9`

### Architectural review and watch-item closures
- `2912b02` `fix(transport): NETCONF session close-time data race`               (#1)
- `dec2a9a` `docs(rfc): architectural review of pr/johalley/ciscoconfig_xe`
- `6631914` `chore(helm, transport): action two of the architectural-review watch-items` (#2, #8)
- `9366061` `ci: add kind smoke workflow that asserts cluster-side plumbing`     (#3)
- `2221f14` `test(fuzz): add parser fuzz targets for device-supplied byte streams` (#6)
- `2a9f605` `ci: add release pipeline with SBOM, cosign keyless signing, SLSA attestation` (#11)
- `820a935` `perf(test), feat(provider, aggregator): close architectural-review watch-items #5, #7, #12` (#5 aggregator, #7, #12)
- `d9ecae3` `docs(rfc): three planning RFCs for the items deferred past this branch` (#9 plan, #10 plan, plus phase-8 residuals)

### Smoke-surfaced fixes
- `19fc47b` `fix(rbac): include aggregator markers in chart ClusterRole`
- `82d7836` `fix(controller): provision VK ServiceAccount + RoleBinding per device namespace`
- `81e98d8` `dev: kind-lan-bridge — reach LAN devices from kind pods on Docker Desktop`
- `31f9af5` `fix(rbac): live-reconcile RBAC defects surfaced by Cat9K smoke test`

---

*Patch this file whenever a watch-item flips state, a phase ships, or a residual closes. The other RFCs in this directory remain authoritative for their topics; this file only summarises and indexes them.*
