# `pr/johalley/ciscoconfig_xe` — implementation reference

**Branch:** `pr/johalley/ciscoconfig_xe`
**Base:** `main`
**Author:** Josh Halley
**Status:** mergeable; release-tag-blocker live-device validation operator-scheduled (see §7).
**Audience:** maintainers and downstream operators evaluating the implementation, tests, and forward roadmap.

This document is the single implementation reference for the branch. It supersedes the wave-by-wave review chain that produced it. Everything you need to understand what changed, how it was tested, and what remains is here or in the linked supporting material.

---

## 1. Summary

The branch transforms `cisco-virtual-kubelet` from an apphosting-only virtual kubelet into an apphosting **plus** declarative IOS-XE configuration driver. Both subsystems coexist inside the same per-device `cisco-vk` pod and share only the pre-existing `CiscoDevice` CR and an optional OpenTelemetry sink.

| Signal | Value |
|---|---:|
| Commits diverged from `main` | 132 |
| Files changed | 438 |
| Lines added | 60,677 |
| Lines deleted | 4,311 |
| New CRDs in `config.cisco.vk/v1alpha1` | 7 |
| Test functions on the branch | ≈ 449 |
| Race-detector iterations passed module-wide | `-race -count=5` clean |
| envtest real-apiserver cases | 9 / 9 PASS |
| Operator-runnable live-device test packages | 12 |

---

## 2. Implementation architecture

### 2.1 What was on `main` before this branch

`cisco-virtual-kubelet` made an IOS-XE device appear as a Kubernetes Node so containerised workloads scheduled through Cisco's app-hosting (Cat9k IOx) feature could be lifecycle-managed by `kubectl`. The shape was:

- **One CRD** — [`CiscoDevice`](../../api/v1alpha1/types.go) — declares a device's connection details (address, credentials reference, driver enum).
- **Cluster controller manager** ([`internal/controller/ciscodevice_controller.go`](../../internal/controller/ciscodevice_controller.go)) — watches `CiscoDevice` CRs and provisions a per-device `Deployment` plus its RBAC.
- **Per-device pod** (`cisco-vk run`) — runs a virtual-kubelet HTTP server bound to an `AppHostingProvider` (Pod lifecycle) and `AppHostingNode` (capacity reporting). Both delegate to the IOS-XE driver in [`internal/drivers/iosxe/`](../../internal/drivers/iosxe/) which speaks RESTCONF.
- **Helm chart** under [`charts/cisco-virtual-kubelet/`](../../charts/cisco-virtual-kubelet/) for the manager + RBAC + the CRD.

The apphosting subsystem on this branch is unchanged in semantics. Minor additions (a `cisco.vk/credential-resource-version` annotation for credential-Secret rotation rolling, a per-namespace VK ServiceAccount + RoleBinding fix surfaced by live smoke testing) are the only edits to the original code paths.

### 2.2 What this branch adds — the configuration-management subsystem

A complete declarative configuration driver for IOS-XE running over RESTCONF, NETCONF, and gNMI, with cross-vendor extension points designed in.

#### 2.2.1 New CRDs (`api/config/v1alpha1`)

| CRD | Purpose |
|---|---|
| [`IOSXEConfig`](../../api/config/v1alpha1/iosxeconfig_types.go) | Per-device standalone configuration declaration. Carries `managedFamilies`, `source` (inline or ConfigMapRef), `driftPolicy`, `transactional`, `writeStartup`, `pruneOnRelinquish`, `targetYangVersion`, `secretRefs[]`, plus the Wave-10 fields `confirmTimeoutSeconds` and `atomicReplace`. |
| [`IOSXEConfigBundle`](../../api/config/v1alpha1/iosxeconfigbundle_types.go) | Selector-based fan-out → N child `IOSXEConfig` CRs. |
| [`IOSXEConfigDefaults`](../../api/config/v1alpha1/iosxeconfigdefaults_types.go) | Cluster-wide base configuration (lowest-precedence merge layer). |
| [`IOSXEDeviceGroupConfig`](../../api/config/v1alpha1/iosxedevicegroupconfig_types.go) | Device-group-scoped defaults. |
| [`IOSXEInterfaceGroupConfig`](../../api/config/v1alpha1/iosxeinterfacegroupconfig_types.go) | Interface-group-scoped configuration shared across devices. |
| [`IOSXETemplate`](../../api/config/v1alpha1/iosxetemplate_types.go) | Reusable parameterised configuration fragment with typed parameters (`string`, `int`, `bool`, `ipv4`, `ipv6`, `cidr`). |
| [`IOSXEConfigApplyLog`](../../api/config/v1alpha1/iosxeconfigapplylog_types.go) | Per-device circular audit log of apply outcomes; enables time-travel replay via the `config.cisco.vk/replay-from-log` annotation. |

The `IOSXEConfig` status subresource carries a `phase` enum (`Pending|Validating|Planning|Applying|Verifying|InSync|Drifted|Failed|Paused|LeaseBlocked`), `observedGeneration`, `lastAppliedHash`, `lastDeviceCheck`, per-family `familyStatus[]`, drift entries, and standard `conditions[]`.

**Resolution chain.** Per-device reconciles deep-merge in this fixed order: defaults → device groups → interface groups → templates → per-device source → per-family secret refs. The deep-merge is keyed by family with `KeyRules` (per-list identity declarations).

#### 2.2.2 Subsystem package layout (`internal/drivers/iosxe/configdriver/`)

| Package | Responsibility |
|---|---|
| [`engine/`](../../internal/drivers/iosxe/configdriver/engine/) | Per-tick reconcile state machine. `Engine.Reconcile(ctx, ResolvedIntent) → Result` walks Validating → Planning → Applying → Verifying → terminal. Terminal phases include `LeaseBlocked` (Wave 8.2). The auto-revert path (Wave 10.2) inserts CommitConfirmed → runningVerify → ConfirmCommit between candidate-verify and the existing terminal phases when `spec.confirmTimeoutSeconds > 0`. |
| [`engine/lease.go`](../../internal/drivers/iosxe/configdriver/engine/lease.go) | Per-`(device, family)` `coordination.k8s.io/v1.Lease` arbitration. Wave 8.1 sanitises lease names to DNS-1123 with a SHA-256 collision suffix; Wave 7A.3 adds runtime-suffixed identity so two pods in the same Deployment rollout cannot both renew. |
| [`writers/`](../../internal/drivers/iosxe/configdriver/writers/) | ~52 family-specific implementations of `SectionWriter` (`Family/YANGPaths/Fetch/Diff/Apply`). Optional `PruneCapable.PruneDiff` for delete-only ops. |
| [`transport/`](../../internal/drivers/iosxe/configdriver/transport/) | Three transports (RESTCONF / NETCONF / gNMI) behind one `Interface`. Optional `TxFetcher` for candidate-side reads (Wave 1A-fu); optional `SubscribeCapable` for gNMI on-change push; optional `ConfirmedCommitter` (Wave 10.1, NETCONF only today) for RFC 6241 §8.4 auto-revert. |
| [`intent/`](../../internal/drivers/iosxe/configdriver/intent/) | `Resolver`, `ResolvedIntent`, `KeyRules`, `CanonicalHash`, parameterised template expansion, `secretRefs[]` merge. |
| [`schema/`](../../internal/drivers/iosxe/configdriver/schema/) | Embedded `families.yaml` (per-family YANG paths, OpenConfig paths, key fields, `depends_on` declarations, portal hints). Used at runtime by `iosxebuilder.FamilyOrderForXE()` for cross-family dependency ordering during atomic replace. |
| [`iosxebuilder/`](../../internal/drivers/iosxe/configdriver/iosxebuilder/) | IOS-XE-specific wiring: `KeyRulesForXE()`, `LookupWriter`, `LoadYANGReleaseTags`, `RegisterGNMIPathKeysForXE`, `FamilyOrderForXE`. |

#### 2.2.3 New controllers and reconcilers

- **`IOSXEConfigBundleReconciler`** ([`internal/controller/iosxeconfigbundle_controller.go`](../../internal/controller/iosxeconfigbundle_controller.go)) — selector → child-CR fan-out with owner-reference garbage collection.
- **`configPrereqs` flow** on `CiscoDeviceReconciler` — bootstraps a synthetic `IOSXEConfig` from `CiscoDevice.spec.configPrereqs` for day-0 prerequisites; teardown empties source + sets `pruneOnRelinquish=true` to revert device-side state on `CiscoDevice` deletion.
- **`AggregatedReconciler`** ([`internal/aggregator/aggregator.go`](../../internal/aggregator/aggregator.go)) — alternative single-manager topology; one `deviceWorker` goroutine per device replaces N per-device pods.
- **`ConfigReconciler`** ([`internal/provider/config_reconciler.go`](../../internal/provider/config_reconciler.go)) — per-pod reconciler; both polling `Run()` and controller-runtime `Reconcile()` paths share `reconcileOne`.

#### 2.2.4 Cross-cutting features

- **Family-leaser arbitration.** Two CRs claiming the same family on the same device do not race: the second goes to `Phase=LeaseBlocked` with a sub-TTL requeue (15 s). Holder identity is `<ns>/<name>#<runtime-id>` so cross-process duplicate-writer hazards close.
- **Hash-based reconcile short-circuit.** Generation + canonical-hash + `Phase=InSync` + drift-detect interval not elapsed + trigger ≠ Subscribe → skip device I/O.
- **gNMI Subscribe fast-path.** On-change events arrive on `ConfigReconciler.SubscribeEvents` (`<-chan event.GenericEvent`); `SetupWithManager` registers a `source.Channel` so the per-pod controller-runtime path picks them up.
- **Replay annotation.** `config.cisco.vk/replay-from-log: <log>:<index|hash>` overrides the resolver with a historical body retained on an `IOSXEConfigApplyLog`.
- **OpenTelemetry tracing.** Per-tick root span on `ConfigReconciler.Reconcile` with device, CR identity, phase, drift count, apply outcomes.
- **Wave 10 confirmed-commit (RFC 6241 §8.4).** Per-CR `spec.confirmTimeoutSeconds` opt-in; engine drives `CommitConfirmed → runningVerify → ConfirmCommit` against NETCONF; backward-compat fallback to plain Commit with a `ConfirmedCommitFallback` Warning event when transport / capability is unavailable.
- **Wave 10 atomic replace.** Per-CR `spec.atomicReplace` opt-in; engine treats the resolved intent as authoritative for the managed families and emits adds + deletes in one transaction with cross-family ordering taken from `schema/families.yaml` `depends_on` declarations.

#### 2.2.5 Tooling additions (`tools/`)

- [`cisco-vk-config-lint`](../../tools/cisco-vk-config-lint/) — offline + cluster-mode validator for IOSXEConfig overlap, selector collisions, missing key fields, circular template refs, drift detection.
- [`cisco-vk-config-docs`](../../tools/cisco-vk-config-docs/) — HTML reference doc generator from `families.yaml` + CRD OpenAPI.
- [`cisco-vk-yang-sync`](../../tools/cisco-vk-yang-sync/) — Cisco-IOS-XE YANG tarball ingestion; emits Go types and writer skeletons.
- [`terraform-provider-iosxeconfig`](../../tools/terraform-provider-iosxeconfig/) — Terraform provider wrapping `IOSXEConfig` CRD CRUD; in-tree pending Terraform Registry publish (see §7.3).

### 2.3 How the two subsystems coexist

```
┌─────────────────────── cisco-vk pod (one per CiscoDevice) ──────────────────────┐
│   AppHostingProvider :10250 ─┐         ┌─ ConfigReconciler                       │
│   AppHostingNode (Capacity)  │         │   • Reconcile + polling Run             │
│   pod_lifecycle.go           │         │   • SubscribeEvents source.Channel      │
│   reconciler.go              │         │   • Engine + Writers + Transport        │
│   Transport: RESTCONF only   │         │   Transport: RESTCONF/NETCONF/gNMI     │
│                              ▼         ▼                                         │
│                    shared CiscoDevice CR (read-only)                             │
│                                  │                                               │
│                                  ▼                                               │
│             ┌───────────── Cat9K device ─────────────┐                           │
│             │  RESTCONF :443  •  NETCONF :830  •  gNMI :57400 │                  │
│             └───────────────────────────────────────────────┘                    │
└──────────────────────────────────────────────────────────────────────────────────┘
```

The two subsystems share at runtime exactly two things: (1) the read-only `CiscoDevice` CR (for address/credentials/transport selection), (2) the OTel exporter when enabled. Distinct goroutine pools, distinct metric namespaces, distinct device-side surfaces (apphosting → IOx; configuration → running-config). The `configPrereqs` flow on `CiscoDevice` is the documented one-way junction between them: a device can declare day-0 configuration that lands before apphosting Pods schedule.

**Two topologies, exclusive-and-correct.** Per-pod (default) spawns one pod per device. Aggregator (`--enable-config-aggregator`) collapses all per-device reconcile into a single manager process. The topology-exclusivity test pins that aggregator-mode never spawns a per-device Deployment with the in-pod ConfigReconciler enabled. The per-pod default is the recommended topology until the aggregator has more soak time.

---

## 3. Phases and waves — what landed, in order

The branch landed in two layers: the original phase plan (0–9) plus a series of wave-numbered closures driven by five external review rounds and one in-house design wave.

### 3.1 Phases 0–9 (original implementation plan)

Phase taxonomy mirrors [`iosxe-config-driver-review.md`](../iosxe-config-driver-review.md) §11.

| Phase | Scope |
|---|---|
| 0 | Scaffold — CRDs, registry interfaces, factory shim |
| 1 | MVP reconciler — 8 apphosting-prereq families |
| 2 | Routing & services — 15 families |
| 3 | Portal completeness — 31 additional families (54 total) |
| 3-feedback | Review-feedback response — template typing, cross-validation corpus, lint, NETCONF, CLI templates |
| 4 | Depth & polish — per-rule diff, secretRefs, prune, lint cluster mode, drift cap, name-pattern, offline plan |
| 5 | NETCONF transport + CLI templates (Jinja2/gonja) |
| 6 | gNMI + OpenConfig path dialect |
| 6.5 | gNMI Subscribe-based push drift detection |
| 7 | Scale & operability — `IOSXEConfigApplyLog`, `IOSXEConfigBundle`, time-travel replay, single-manager topology, multi-version YANG |
| 8 | Ecosystem — Terraform provider, OPA/conftest, ArgoCD health, portal-compat docs |
| 9 | Platform plug-in registry — apphosting + configdriver, blank-import hub, placeholders (`iosxr/`, `nxos/`, `openconfig/`) |

### 3.2 Wave-numbered closures

Five review rounds produced focused remediation waves; the sixth wave (Wave 10) is in-house design. **Twenty-six** external-review findings closed across the chain; nothing was contested.

| Wave | Scope | Closing-wave anchors |
|---|---|---|
| 1A–5A | Original review (12 findings) | Transactional apply + `SaveStartup` (1A), steady-state drift detection (1B), aggregator/per-pod exclusivity (1C), aggregator Helm RBAC (1D), replay annotation cleanup RBAC (2A), YANG defaulting (2B), multi-family conflict status (2C), Secret watch wiring (2D), bundle selector watch (3A), bundle template CRD relaxation (3B), `configPrereqs` deletion (4A), schema-aware gNMI keyed paths (5A) |
| 1A-fu / 4A-fu / 5A-fu / 6A / 6B | Follow-up review (5 findings) | NETCONF transactional verify via candidate-aware `TxFetcher` (1A-fu), `configPrereqs` teardown that keeps the family list and empties source (4A-fu), structured `PathSpec` on gNMI ops with production-bound registration (5A-fu), gNMI Subscribe in per-pod controller-runtime (6A), credential Secret rotation across both topologies (6B) |
| 7A.1 / 7A.2 / 7A.3 / 7A.4 / 7B | Next-actions review (5 findings) | NETCONF transactional + CLI engine-side fail-fast (7A.1), `configPrereqs` teardown freshness gate on `ObservedGeneration + Phase` (7A.2), runtime-suffixed lease identity for cross-process arbitration (7A.3), `pruneOnRelinquish` set only on teardown step (7A.4), `PathSpec` on handwritten interface writers (7B) |
| 8.1 / 8.2 | Wave-7 residuals (2 findings) | DNS-1123-safe `leaseName` with sanitisation + SHA-256 hash, validated against `IsDNS1123Subdomain` for every shipped family (8.1); `PhaseLeaseBlocked` as a first-class arbitration state with all-blocked short-circuit, partial-block downgrade, `Result.DeviceTouched` gating `LastDeviceCheck`, sub-TTL requeue (8.2) |
| 9.1 / 9.2 | Wave-8 follow-up (2 findings) | `LeaseBlocked` admission in the IOSXEConfig CRD enum with a schema-aware test that parses the generated CRD (9.1); `reconcileOne` returns `(engine.Result, error)` so the controller-runtime caller and OTel span attribution source the phase from the engine result rather than the deep-copied pre-update CR (9.2) |
| 10.1 / 10.2 / 10.3 / 10.4 | Wave 10 — risk-reduction primitive | NETCONF `:confirmed-commit:1.0` capability advertisement and `ConfirmedCommitter` interface (10.1); `spec.confirmTimeoutSeconds` field + engine `CommitConfirmed → runningVerify → ConfirmCommit` state machine + backward-compat fallback (10.2); `spec.atomicReplace` field + engine atomic-replace path with cross-family `depends_on` ordering (10.3); operator-runnable live playbook for tests 08 + 09 + reconciler `ConfirmedCommitFallback` / `ConfirmedCommitUsed` events (10.4) |

### 3.3 Architectural lessons internalised

Six lessons live in commit messages and inline comments so they do not recur. They form the durable testing-discipline closure for this branch:

1. **`fake.Client` is not a substitute for envtest** when CRD schema validation (`MinItems`, required) is part of the contract under test.
2. **Side-effect-driven registries are fragile** when the side-effect lives in a code path the production binary does not execute (Wave 5A-fu — gNMI registry was populated via `schema.LoadFamilies`, which only the docs generator called).
3. **Async status subresources** mean `Status.X` and `Spec.Y` can disagree during a reconcile cycle. Gates that read both must explicitly verify they refer to the same generation (Wave 7A.2 configPrereqs teardown freshness).
4. **A lease that protects in-process duplicate writers does NOT protect cross-process overlap** during pod/worker rollouts unless the holder identity is process-unique (Wave 7A.3 lease identity).
5. **`fake.Client` does not enforce Kubernetes object-name or field-enum validation.** When a name is composed from arbitrary strings or a status field is constrained by enum, the test must explicitly validate against `k8s.io/apimachinery/pkg/util/validation` or against the generated CRD schema (W7R-1 name validation, W8FU-1 enum validation).
6. **Status writes via DeepCopy do not mutate the caller's CR.** When a function writes status through `cr.DeepCopy()` and the caller needs the post-write phase (controller-runtime requeue, span attribution, follow-on conditional writes), the function must return that phase explicitly (Wave 9.2).

---

## 4. Tests — by phase and wave

The testing strategy is layered: unit tests for hot-path correctness, race detector for concurrency safety, helm lint + manifest sync for chart integrity, schema-aware envtest for apiserver-admission semantics, and an operator-runnable live-device playbook for behaviours that only manifest against real hardware.

### 4.1 Unit tests (≈ 449 functions across 22 packages)

| Phase / Wave | Anchor test file(s) | What it pins |
|---|---|---|
| 0 — scaffold | [`internal/drivers/registry_test.go`](../../internal/drivers/registry_test.go), [`internal/drivers/placeholders_test.go`](../../internal/drivers/placeholders_test.go) | Driver registry contract; placeholder packages MUST NOT register |
| 1A — transactional + writeStartup | [`engine/transactional_test.go`](../../internal/drivers/iosxe/configdriver/engine/transactional_test.go) | Commit on success, Discard on failure, SaveStartup gating on capability + flag |
| 1A-fu — candidate-aware Fetch | same file, `TestTransactionalVerifyReadsCandidate` | Verify-Fetch routes to candidate via `TxFetcher` |
| 1B — drift-detect interval | [`internal/provider/drift_detect_test.go`](../../internal/provider/drift_detect_test.go) | Hash short-circuit honors `driftDetectInterval`; subscribe trigger bypasses |
| 1C — topology exclusivity | [`internal/controller/aggregator_exclusivity_test.go`](../../internal/controller/aggregator_exclusivity_test.go) | Aggregator-mode never spawns per-device deployment |
| 2C — multi-family conflict | [`internal/provider/conflict_message_test.go`](../../internal/provider/conflict_message_test.go) | Overlap on second family no longer reports `NoOverlap` |
| 4A-fu — configPrereqs teardown | [`internal/controller/credential_rotation_test.go`](../../internal/controller/credential_rotation_test.go), [`writers/dhcp_test.go`](../../internal/drivers/iosxe/configdriver/writers/dhcp_test.go) | Empty source + `pruneOnRelinquish=true` reverts device-side state |
| 5A-fu / 7B — gNMI keyed paths | [`transport/gnmi_keys_test.go`](../../internal/drivers/iosxe/configdriver/transport/gnmi_keys_test.go) | Schema-registered keys win over heuristic; structured `PathSpec` survives wire encoding |
| 6A — Subscribe fast-path | [`internal/provider/subscribe_perpod_test.go`](../../internal/provider/subscribe_perpod_test.go), [`internal/provider/subscribe_watcher_test.go`](../../internal/provider/subscribe_watcher_test.go) | `SubscribeEvents` channel registered in `SetupWithManager`; subscribeNotifyTime distinguishes triggers |
| 6B — credential rotation | [`internal/controller/credential_rotation_test.go`](../../internal/controller/credential_rotation_test.go), [`internal/aggregator/credential_rotation_test.go`](../../internal/aggregator/credential_rotation_test.go) | Secret rotation rolls per-device Deployment / aggregator worker |
| 7A.1 — transactional + CLI fail-fast | `engine/transactional_test.go`, `TestTransactionalCLIRejected` | Engine refuses before any transport call |
| 7A.3 — runtime-suffixed lease identity | [`internal/provider/lease_identity_test.go`](../../internal/provider/lease_identity_test.go) | Two reconcilers with different `RuntimeID` cannot both renew |
| 8.1 — DNS-1123 lease names | [`engine/lease_name_test.go`](../../internal/drivers/iosxe/configdriver/engine/lease_name_test.go) | Every shipped family validates against `IsDNS1123Subdomain` |
| 8.2 — `LeaseBlocked` arbitration | [`internal/provider/lease_blocked_test.go`](../../internal/provider/lease_blocked_test.go) | All-blocked short-circuits before engine; partial-block downgrades clean InSync; `LastDeviceCheck` gated on `DeviceTouched`; sub-TTL requeue |
| 9.1 — schema-aware enum guard | [`internal/provider/iosxeconfig_phase_enum_test.go`](../../internal/provider/iosxeconfig_phase_enum_test.go) | Parses generated CRD; asserts every status-bound engine phase is enumerated |
| 9.2 — post-reconcile requeue | `internal/provider/lease_blocked_test.go`, headline `TestReconcile_AllBlockedReturnsSubTTLRequeueAndLeaseBlockedPhase` | End-to-end: foreign lease blocks the only family; asserts sub-TTL requeue + LeaseBlocked phase + LastDeviceCheck unchanged + engine never called |
| 10.1 — NETCONF confirmed-commit transport | [`transport/netconf_test.go`](../../internal/drivers/iosxe/configdriver/transport/netconf_test.go) | Capability advertisement; `CommitConfirmed → ConfirmCommit` RPC sequence; timeout clamping; capability rejection |
| 10.2 — engine confirmed-commit state machine | `engine/transactional_test.go` | Five tests: happy path, auto-revert on verify failure, fallback when transport lacks interface, fallback when capability missing, non-transactional surfaces reason |
| 10.3 — atomic replace + family ordering | [`engine/engine_test.go`](../../internal/drivers/iosxe/configdriver/engine/engine_test.go) | `AtomicReplace=true` implies `PruneOnRelinquish`; `FamilyOrder` hook applied; nil hook preserves input order |

Module-wide gate: `GOCACHE=/tmp/cvk-gocache go test -race -count=5 ./...` clean.

### 4.2 Real-apiserver envtest (9 cases)

Closes the recurring "fake.Client doesn't validate" lesson at the apiserver-admission level. Build-tagged `envtest`; runnable via `make test-envtest` (uses `setup-envtest @ v0.19.7` to download apiserver + etcd binaries).

| Test | Asserts |
|---|---|
| `TestEnvtest_StatusPhaseLeaseBlockedAccepted` | Real apiserver accepts `status.phase=LeaseBlocked` (Wave 9.1) |
| `TestEnvtest_StatusPhaseEnumRejectsBogusValue` | **Negative control** — bogus phase rejected; proves enum is *enforced*, not just present |
| `TestEnvtest_LeaseCreationForUnderscoreFamily` | `FamilyLeaser.Acquire(edge-01, "interface_ethernet", ...)` succeeds; stored name has no underscore (Wave 8.1) |
| `TestEnvtest_ConfirmTimeoutSecondsAdmittedByApiserver` | `confirmTimeoutSeconds=30` round-trips |
| `TestEnvtest_ConfirmTimeoutSecondsMaximumEnforced` | `=301` rejected (kubebuilder Maximum=300) |
| `TestEnvtest_ConfirmTimeoutSecondsBoundaryValues` | `=0` and `=300` accepted; `=-1` rejected (Min=0) |
| `TestEnvtest_AtomicReplaceFieldAdmitted` | `atomicReplace=true` round-trips |
| `TestEnvtest_AtomicReplaceWithConfirmedCommitCombined` | Both Wave 10 fields together are admissible |
| `TestEnvtest_NonTransactionalCRWithConfirmTimeoutAdmissible` | Non-transactional CR with `confirmTimeoutSeconds` is admissible (the engine surfaces it via `ConfirmedCommitFallback`, not via admission) |

Source: [`internal/provider/envtest_apiserver_smoke_test.go`](../../internal/provider/envtest_apiserver_smoke_test.go).

### 4.3 Operator-runnable live-device playbook (12 packages)

Under [`./release-blocker-tests/`](./release-blocker-tests/). Ordered least-disruptive to most-disruptive in [`./release-blocker-tests/RUNBOOK.md`](./release-blocker-tests/RUNBOOK.md) §4. Each package contains `README.md`, `00-apply.yaml` (or two phases for the multi-step tests), `expected.md`, `pre-state.sh`, `verify.sh`, `rollback.sh`. Every `verify.sh` sources [`./release-blocker-tests/lib/baseline.sh`](./release-blocker-tests/lib/baseline.sh) for shared assertions (observedGeneration synced, phase + Ready condition, no ApplyError, no stale LeaseBlocked, transport-aware metric proofs).

| # | Test | Closing waves | Risk profile |
|---|---|---|---|
| 02 | NETCONF transactional + CLI rejection | 7A.1 | Lowest — engine refuses before any device write |
| 04 | gNMI keyed-path (`GigabitEthernet0/0/0`) | 5A-fu + 7B | Cosmetic — interface description only |
| 05 | Credential Secret rotation overlap | 6B + 7A.3 + 8.2 + 9.2 | Pod-side; no device write; transient `LeaseBlocked` |
| 01 | NETCONF transactional structured (Loopback9999) | 1A-fu | Clean rollback |
| 07 | `writeStartup` save-config (Loopback9997) | 1A | Modifies startup-config — explicit operator approval |
| 06 | `driftPolicy: revert` live write (banner motd) | drift-detect machinery | Visible but reversible |
| 11 | `confirmTimeoutSeconds` + non-transactional → fallback warning | 10.2 + 10.4 | Lowest Wave-10 risk — no auto-revert path engaged |
| 10 | Confirmed-commit happy path (Loopback9995, NETCONF) | 10.1 + 10.2 | Catches silent-fallback regression |
| 09 | Atomic-replace cross-family (VLAN/VRF/Loopback) | 10.3 | Two-phase atomic removal |
| 13 | Combined atomic-replace + confirmed-commit | 10.1 + 10.2 + 10.3 | Recommended-default proof; both safety nets |
| 08 | Confirmed-commit auto-revert (deliberate mgmt-session break) | 10.1 + 10.2 | **Most invasive** — out-of-band console required |
| 03 | configPrereqs deletion-driven cleanup | 4A-fu + 7A.2 + 7A.4 | Most invasive — exercises deletion finalizer end-to-end |

Top-level operator entry-point: [`./release-blocker-tests/RUNBOOK.md`](./release-blocker-tests/RUNBOOK.md). Mandatory preflight: [`./release-blocker-tests/preflight.sh`](./release-blocker-tests/preflight.sh) (kubectl context, device readiness, transport reachability, operator interface approval). Pre-window snapshot helper: [`./release-blocker-tests/fetch-running-config.sh`](./release-blocker-tests/fetch-running-config.sh).

A 12th slot is reserved for a future "older IOS-XE without `:confirmed-commit:1.0`" test that requires a second device with an older image; intentionally unauthored on this branch.

### 4.4 Verification gate snapshot

The most recent gate run is at [`./evidence/2026-04-26-wave10-variation-matrix/`](./evidence/2026-04-26-wave10-variation-matrix/). Every gate exit-zero:

| Gate | Outcome |
|---|---|
| `go test ./...` | ✅ PASS, 22 packages |
| `go test -race -count=5 ./...` | ✅ PASS, all packages × 5 iterations |
| `go vet ./...` | ✅ clean |
| `helm lint charts/cisco-virtual-kubelet` | ✅ clean (1 info note about icon) |
| CRD / Helm-chart sync | ✅ 8 / 8 in sync |
| `make test-envtest` (real apiserver) | ✅ 9 / 9 PASS |

CI parity: [`.github/workflows/smoke.yml`](../../.github/workflows/smoke.yml) runs `go vet`, generated-artefact drift check, CRD/chart sync, `setup-envtest @ v0.19.7` (PINNED, never `@latest`), `make test-envtest`, `helm lint`, then the kind-cluster smoke (build image → kind load → Helm install → CR apply → cisco-vk pod reaches device-call boundary).

---

## 5. Architectural alignment with the original container-deployment model

This is the question every reviewer asks first: does the new subsystem disrupt the original apphosting deployment model?

**Answer: no.** The pre-existing apphosting subsystem is unchanged in semantics. The two subsystems coexist by:

1. **Sharing exactly two things at runtime** — read-only `CiscoDevice` CR; optional OTel exporter.
2. **Targeting distinct device-side surfaces** — apphosting writes to `/restconf/data/Cisco-IOS-XE-app-hosting:app-hosting-cfg-data`; configuration writes to `/restconf/data/Cisco-IOS-XE-native:native/...`. No overlap.
3. **Running in distinct goroutine pools** with distinct metric namespaces.
4. **Using independent transport stacks** — apphosting RESTCONF only; configuration multi-protocol. The two stacks share no connection pool or auth state. Operationally cheap (two TLS sessions per pod when both subsystems active, well below any device session limit).
5. **Documenting the one junction point** — `CiscoDevice.spec.configPrereqs` materialises a synthetic `IOSXEConfig` for day-0 prerequisites that land before apphosting Pods schedule. Teardown reverses the bootstrap state on `CiscoDevice` deletion.

The new failure modes (lease conflicts, drift detection, confirmed-commit auto-revert) are scoped to the configuration subsystem; an apphosting Pod scheduling decision is unaffected by configuration-side state.

The two architectural tensions worth naming explicitly — both deferred to dedicated future PRs:

- **Platform-agnostic code under `internal/drivers/iosxe/configdriver/...`.** The `engine/`, `intent/`, `schema/`, and most of `transport/` are not IOS-XE-specific. They live under the IOS-XE driver path because that's where the original platform-specific work began. Phase-10 cosmetic relocation moves them to `internal/configdriver/`. See §7.2.
- **CRD surface area at v1alpha1 is large.** Seven new CRDs plus three nested type families and a five-layer merge precedence chain. CRD v1 promotion plan addresses this with a conversion webhook. See §7.2.

---

## 6. Operator runtime defaults

| Setting | Default | When to change |
|---|---|---|
| Topology | per-pod (`aggregator.enabled=false` in [`charts/cisco-virtual-kubelet/values.yaml`](../../charts/cisco-virtual-kubelet/values.yaml)) | Aggregator opt-in only, after sufficient soak time |
| `spec.driftPolicy` | `revert` (CRD default) | `report` for cutover-safe parallel-run with another tool |
| `spec.transactional` | `false` | `true` when running NETCONF and you want candidate-commit semantics |
| `spec.writeStartup` | `false` | `true` when device should survive reboot without a config-save orchestrator |
| `spec.driftDetectInterval` | `5m` (clamped ≥ 30s) | Shorter for high-confidence environments; longer for low-impact monitoring |
| `spec.pruneOnRelinquish` | `false` | `true` for CRs whose intent is authoritative for the families |
| **`spec.confirmTimeoutSeconds`** (Wave 10) | `0` (off) | **Recommended `30` for ACL / management-plane changes; `60–120` for BGP / routing-protocol changes** |
| **`spec.atomicReplace`** (Wave 10) | `false` | `true` for CRs whose intent is the complete state for those families; combines naturally with `confirmTimeoutSeconds > 0` |

The Wave-10 fields are the recommended-default for any operator running risk-sensitive families (BGP, ACL, management-plane, VRF) under config-as-code. Either alone is incomplete; together they enable "all-or-nothing with auto-revert on connectivity loss."

---

## 7. Roadmap — follow-up work

Three categories of remaining work, none of which gate merge.

### 7.1 Live-device validation (release-tag prerequisite)

Eight 🔒-classified live-device retests are operator-scheduled as prerequisites for any release tag. Each modifies running device state on a real Cat9K and is documented as an operator-runnable test package under [`./release-blocker-tests/`](./release-blocker-tests/) — see §4.3 for the table.

The eight blockers cover:

- NETCONF transactional structured (Wave 1A-fu)
- NETCONF transactional + CLI rejection (Wave 7A.1)
- `configPrereqs` deletion cleanup (Waves 4A-fu + 7A.2 + 7A.4)
- gNMI keyed-path (Waves 5A-fu + 7B)
- Credential Secret rotation overlap (Waves 6B + 7A.3 + 8.2 + 9.2)
- `driftPolicy: revert` live write
- **Wave 10 confirmed-commit auto-revert** (deliberate management-session break — most operationally meaningful test of the entire branch)
- **Wave 10 atomic-replace cross-family** (partial-drift prevention)

When all eight pass in a maintenance window, the dispositions in §3.2 above flip from `🔒 release blocker` to `✅ verified live` and the branch is release-certified. The Wave-9-status review framing applies: *"I would not let 'mergeable' quietly become 'release-certified' until the real-apiserver and live-device checks are captured."*

### 7.2 Future PRs (deferred by explicit plan)

Three architectural watch-items deferred to dedicated PRs:

- **Watch-item #4 — Cosmetic relocation** of `internal/drivers/iosxe/configdriver/...` → `internal/configdriver/...`. Mechanical; touches every import path. Deferred because it conflicts noisily with the v1 CRD cut and the netascode example corpus PRs in flight. Plan: [`../driver-extension-guide.md`](../driver-extension-guide.md) §7. Effort: mechanical.
- **Watch-item #9 — Log unification** (logrus + zap → `slog` shims). Plan: [`../log-unification-plan.md`](../log-unification-plan.md). Effort: ~3 engineer-days.
- **Watch-item #10 — CRD v1alpha1 → v1 promotion** with conversion webhook and three-release phasing. Plan: [`../crd-v1-promotion-plan.md`](../crd-v1-promotion-plan.md). Effort: ~2 engineer-weeks; should land on a release-cut branch with wider-team review. The Wave-10 fields (`confirmTimeoutSeconds`, `atomicReplace`) propagate naturally into v1.

A fourth deferred item, **broader envtest infrastructure**, lands with the conversion-webhook PR (item #10) since it depends on the same etcd + apiserver binary infrastructure. The narrow envtest already on this branch (9 cases) closes the most-pressing fake-client blind spots; broader controller-runtime coverage waits.

### 7.3 External infrastructure (Phase-8 residuals)

Two items external to the source tree, tracked in [`../phase-8-residuals.md`](../phase-8-residuals.md):

- **Terraform Registry publish** for `cisco-open/iosxeconfig`. Provider implementation is feature-complete. Missing: Cisco/HashiCorp publisher account (paperwork), GPG signing key in corporate KMS, `.github/workflows/terraform-release.yml` for multi-arch build + sign + publish, Hashicorp-layout provider docs.
- **netascode portal-compat example corpus**. Per-family pages with operator-validated example fragments. Structure shipped; content is ~80 engineer-hours of focused YAML authoring + lab validation.

Neither blocks merge; both should land before a release announcement.

### 7.4 Pre-existing branch deferrals

- **`internal/provider` package coverage** — currently 25.7 %. The uncovered code is controller-runtime wiring (predicates, handler.Funcs, watch establishment) that needs envtest to exercise honestly. Lands with item #10's conversion-webhook PR.
- **Apphosting-side ygot regen drift** — `make generate` produces a 754-line drift in `internal/drivers/iosxe/models.go` that is unrelated to this branch's configuration work. Reverted locally during gate runs; tracked as a separate apphosting cleanup item.

---

## 8. Cross-references

| Topic | Reference |
|---|---|
| Phase taxonomy + design intent (Phases 0–9) | [`../iosxe-config-driver-review.md`](../iosxe-config-driver-review.md) |
| Quality / composition snapshot | [`../iosxe-config-driver-appraisal.md`](../iosxe-config-driver-appraisal.md) |
| Architectural watch-items 1–12 | [`../architectural-review.md`](../architectural-review.md) |
| Driver-extension contract (NX-OS, IOS-XR, OpenConfig placeholders) | [`../driver-extension-guide.md`](../driver-extension-guide.md) |
| Transport architecture — RESTCONF / NETCONF / gNMI internals | [`../transport-architecture.md`](../transport-architecture.md) |
| Deployment modes — operator setup guide per transport | [`../deployment-modes.md`](../deployment-modes.md) |
| Operator CLI guide — kubectl interaction, status fields, events, metrics, troubleshooting, roadmap | [`../operator-cli-guide.md`](../operator-cli-guide.md) |
| Diagnostics RFC — show-command surface design (Phases A–D delivered) | [`../diagnostics-rfc.md`](../diagnostics-rfc.md) |
| Diagnostics guide — operator usage of `IOSXEDiagnostic` CRD + `kubectl ciscovk exec` plugin | [`../diagnostics-guide.md`](../diagnostics-guide.md) |
| Device-operations RFC — destructive-ops surface (clears / reload / write-erase) with RBAC tiers + two-person rule | [`../device-operations-rfc.md`](../device-operations-rfc.md) |
| CRD v1 promotion plan | [`../crd-v1-promotion-plan.md`](../crd-v1-promotion-plan.md) |
| Slog-backend log unification plan | [`../log-unification-plan.md`](../log-unification-plan.md) |
| External infrastructure residuals (Phase 8) | [`../phase-8-residuals.md`](../phase-8-residuals.md) |
| Operator-runnable live-device test playbook | [`./release-blocker-tests/RUNBOOK.md`](./release-blocker-tests/RUNBOOK.md) |
| Latest gate evidence bundle | [`./evidence/2026-04-26-wave10-variation-matrix/SUMMARY.md`](./evidence/2026-04-26-wave10-variation-matrix/SUMMARY.md) |
| Live-device retest (Cat9300 / IOS-XE 17.18.2) | [`./evidence/2026-04-27-live-c9300-cat9k-smoke/SUMMARY.md`](./evidence/2026-04-27-live-c9300-cat9k-smoke/SUMMARY.md) |
| Live-device retest #2 — fixes validated | [`./evidence/2026-04-27-live-c9300-v8-fixes-validated/SUMMARY.md`](./evidence/2026-04-27-live-c9300-v8-fixes-validated/SUMMARY.md) |
| Live-device retest #3 — production-readiness pass | [`./evidence/2026-04-27-live-c9300-v12-production-ready/SUMMARY.md`](./evidence/2026-04-27-live-c9300-v12-production-ready/SUMMARY.md) |
| Live-device tier-1 — #6(a) NETCONF dial root-cause narrowing | [`./evidence/2026-04-27-live-c9300-netconf-probe-tier1/SUMMARY.md`](./evidence/2026-04-27-live-c9300-netconf-probe-tier1/SUMMARY.md) |
| Live-device — NETCONF candidate-only mode closure (tests 06 + 07) | [`./evidence/2026-04-27-live-c9300-netconf-candidate-only/SUMMARY.md`](./evidence/2026-04-27-live-c9300-netconf-candidate-only/SUMMARY.md) |
| Live-device retest #4 — release-blocker dashboard 2026-04-28 (tests 01/03/05/08/09/10/11/13 ✅; 04 ⏸ shared-port) | [`./evidence/2026-04-28-live-c9300-release-blockers/SUMMARY.md`](./evidence/2026-04-28-live-c9300-release-blockers/SUMMARY.md) |
