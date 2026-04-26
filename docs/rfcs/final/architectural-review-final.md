# Final architectural review — `pr/johalley/ciscoconfig_xe`

**Branch:** `pr/johalley/ciscoconfig_xe`
**Base:** `main`
**Author:** Josh Halley
**Date:** 2026-04-26
**Status:** post-Wave-9 closure; reviewer-accepted with no new blocking findings (Codex, [`../external-review-wave9-status.md`](../external-review-wave9-status.md))
**Audience:** maintainers, release reviewers, downstream operators evaluating whether further architectural changes are needed before merge.

This document is the single architectural overview for the configuration-management subsystem this branch adds to `cisco-virtual-kubelet`. It complements the wave-by-wave RFCs (which are authoritative for individual findings and closures) by collecting everything into one place: what existed, what was added, how the additions align with the pre-existing container-deployment model, what is still pending, and whether any architectural adjustments are warranted before merging.

---

## 1. Executive summary

**Numerical baseline.** 115 commits diverged from `main`. 321 files changed, +51,811 lines added, −4,299 deleted. The bulk of the addition is the configuration-management subsystem (`internal/drivers/iosxe/configdriver/...`, 8 new CRDs, 4 new controllers, ~52 per-family writers, multi-protocol transport stack), the documentation and RFC chain that grew alongside it (~7,168 doc lines under `docs/rfcs/` alone), and four new in-tree tools (`cisco-vk-config-lint`, `cisco-vk-config-docs`, `cisco-vk-yang-sync`, `terraform-provider-iosxeconfig`).

**Review chain.** Five external review rounds (Codex), 26 of 26 findings closed in code with focused tests. The latest round ([`../external-review-wave9-status.md`](../external-review-wave9-status.md)) accepted both Wave 9 closures and recorded no new blocking findings.

**Architectural verdict.** **No structural change is required before merge.** The branch is shippable for day-0 and day-2 under the per-pod topology, with the aggregator topology exclusive-and-correct. The pre-existing container-deployment architecture (apphosting on Cat9k via virtual-kubelet) and the new configuration-management subsystem coexist cleanly inside the same `cisco-vk` pod, sharing only the `CiscoDevice` CR and the OTel exporter — neither side mutates state that the other depends on.

The follow-up work that remains is a mix of (a) external infrastructure (Terraform Registry publish, netascode example corpus), (b) deliberate deferrals to dedicated PRs (CRD v1 promotion, log unification, cosmetic relocation), (c) test-discipline closure (envtest), and (d) operator-scheduled live retests against the lab Cat9K. None block merge. The §6.E code-level TODOs and the §6.F.3 documentation cleanup were the only items that were both reasonable and in-scope for this branch; both have closed (commits `2e73766` and `5487dc0`).

§5 of this document elaborates the two architectural tensions that *are* worth naming — both are watch-items with plans on file, not pre-merge blockers.

---

## 2. The pre-branch baseline — container-deployment architecture

### 2.1 Problem the original repository solved

`cisco-virtual-kubelet` makes a Cisco IOS-XE device appear as a Kubernetes Node, so containerised workloads deployed through the device's **app-hosting** capability (Cat9k IOx, etc.) can be scheduled, monitored, and lifecycle-managed by `kubectl` and standard Kubernetes tooling. The pre-branch shape was a focused proof-of-concept: one CRD (`CiscoDevice`), one transport (RESTCONF), one provider (apphosting).

### 2.2 Per-device pod lifecycle

The pre-branch deployment model is two-tier:

1. **Cluster controller manager** (`cisco-vk manager`) — watches `CiscoDevice` CRs cluster-wide. For each device, the [`CiscoDeviceReconciler`](../../../internal/controller/ciscodevice_controller.go) provisions a per-device `Deployment`, `ConfigMap`, and the RBAC bindings the per-device pod needs. The reconciler also runs the deletion-finalizer path that tears down the synthetic node and the Deployment when a `CiscoDevice` is removed.

2. **Per-device pod** (`cisco-vk run`) — one pod per `CiscoDevice`. Inside the pod, the virtual-kubelet HTTP server (`:10250`) is wired to the `AppHostingProvider` (`nodeutil.Provider`) and the `AppHostingNode` (`node.NodeProvider`), both implemented in [`internal/provider/provider.go`](../../../internal/provider/provider.go). They delegate to a shared per-device driver implementation in [`internal/drivers/iosxe/`](../../../internal/drivers/iosxe/) — `pod_lifecycle.go` translates Kubernetes Pod specs into IOx app-hosting configurations, `driver.go` is the RESTCONF client.

The per-pod runtime is intentionally a single process: one HTTP listener, one apphosting goroutine pool, one transport. The pod is the failure unit; if it dies, the controller restarts it, and apphosting drift on the device is reconciled on next pod tick.

### 2.3 Pre-branch CRD surface

A single API group: `cisco.vk/v1alpha1`. Two visible kinds: [`CiscoDevice`](../../../api/v1alpha1/types.go) (device connection spec — address, credentials reference, driver enum, capacity hints) and its boilerplate `CiscoDeviceList`.

### 2.4 Pre-branch Helm chart

[`charts/cisco-virtual-kubelet/`](../../../charts/cisco-virtual-kubelet/) deploys the manager, RBAC for `CiscoDevice` watch + per-device Deployment provisioning, and the CRD itself. Per-device pods are not chart resources; they are children of a `CiscoDevice` reconcile.

### 2.5 What stays untouched on this branch

Apphosting remains the original `internal/drivers/iosxe/{driver.go,pod_lifecycle.go,reconciler.go}` plus its supporting `internal/provider/{provider.go,node.go}` files. The branch makes minor additions (e.g. `cisco.vk/credential-resource-version` annotation for credential-Secret rotation rolling, recovery loop for stuck Pods), but the apphosting state machine, transport, and node-provider interface contracts are unchanged. **No apphosting-side semantics are altered by this branch.** The configuration-management subsystem is purely additive on top of the apphosting baseline.

---

## 3. The new configuration-management subsystem

### 3.1 Why it exists

Operators of Cat9k fleets need declarative, GitOps-friendly device-configuration management alongside the apphosted workloads — VLANs, interface IPs, ACLs, BGP, AAA, NTP, and 50+ other "families" of running configuration. The branch adds this as a separate subsystem so operators can apply both apphosting Pods *and* IOS-XE configuration from the same Kubernetes control plane, with the same RBAC, GitOps, and observability discipline.

The design follows the **netascode** project's family/scope shape (IOS-XE-as-data), so existing netascode YAML payloads work as the per-device source of truth without translation.

### 3.2 New CRD surface — `config.cisco.vk/v1alpha1`

Eight new CRDs ([`api/config/v1alpha1/`](../../../api/config/v1alpha1/)):

| CRD | Purpose | Key fields |
|---|---|---|
| `IOSXEConfig` | Per-device standalone configuration declaration | `deviceRef`, `managedFamilies`, `source` (inline or ConfigMapRef), `driftPolicy`, `driftDetectInterval`, `transactional`, `writeStartup`, `pruneOnRelinquish`, `targetYangVersion`, `secretRefs[]` |
| `IOSXEConfigBundle` | Selector-based fan-out → N child `IOSXEConfig` CRs | `deviceRefs[]` or `deviceSelector`, `template` (embedded `IOSXEConfigTemplateSpec`), `status.generatedCRs[]` |
| `IOSXEConfigDefaults` | Cluster-wide base configuration (lowest-precedence merge layer) | `configuration` (schemaless YAML), targets all devices |
| `IOSXEDeviceGroupConfig` | Device-group-scoped defaults | `deviceRefs[]` or `deviceSelector`, `configuration` |
| `IOSXEInterfaceGroupConfig` | Interface-group-scoped configuration shared across devices | per-interface YAML body |
| `IOSXETemplate` | Reusable parameterised configuration fragment | `spec.parameters[]` (typed: string/int/bool/ipv4/ipv6/cidr), `spec.configuration` (Go text/template) |
| `IOSXEConfigApplyLog` | Per-device circular audit log of apply outcomes | `spec.maxEntries`, `spec.retainBody`; entries carry `phase`, `hash`, source CR, family outcomes; powers Phase-7 time-travel replay |
| `IOSXEConfigStatus` (subresource of `IOSXEConfig`) | Phase, observed generation, last-applied-hash, drift entries, family statuses, conditions | `phase` enum: `Pending,Validating,Planning,Applying,Verifying,InSync,Drifted,Failed,Paused,LeaseBlocked` |

**Resolution chain.** When a per-device tick runs, the [`Resolver`](../../../internal/drivers/iosxe/configdriver/intent/resolver.go) deep-merges in this fixed order: defaults → device groups → interface groups → templates → per-device source → per-family secret refs. Rightmost wins. The result is a `ResolvedIntent` that carries the deep-merged `Configuration map[string]any`, the closed `ManagedFamilies` list, and the per-CR knobs (`Transactional`, `DriftPolicy`, etc.).

### 3.3 Subsystem package layout

[`internal/drivers/iosxe/configdriver/`](../../../internal/drivers/iosxe/configdriver/) is the platform-specific home for IOS-XE configuration driving. Subpackages:

- **[`engine/`](../../../internal/drivers/iosxe/configdriver/engine/)** — the per-tick reconcile state machine. [`Engine.Reconcile(ctx, ResolvedIntent) → Result`](../../../internal/drivers/iosxe/configdriver/engine/engine.go) walks the five phases (Validating → Planning → Applying → Verifying → terminal) family-by-family. The terminal phase set is `{InSync, Drifted, Failed, Paused, LeaseBlocked}` (the last added in Wave 8.2 as a first-class arbitration state). [`FamilyLeaser`](../../../internal/drivers/iosxe/configdriver/engine/lease.go) implements per-`(device, family)` `coordination.k8s.io/v1.Lease` arbitration with a 30s TTL and DNS-1123-safe lease names (Wave 8.1 sanitises + hashes underscore-bearing family names).

- **[`writers/`](../../../internal/drivers/iosxe/configdriver/writers/)** — ~52 family-specific implementations of the `SectionWriter` interface (`Family() / YANGPaths() / Fetch / Diff / Apply`), plus an optional `PruneCapable.PruneDiff` for delete-only ops. Coverage spans system / AAA / SNMP / NTP / VLAN / VRF / interface_ethernet/loopback/port_channel/switchport/tunnel/vlan / static_route / OSPF/BGP/EIGRP / ACL (v4 + v6) / route-map / DHCP / etc. The registry is process-global with `init()`-time self-registration; production binaries import [`internal/drivers/iosxe/configdriver/iosxebuilder/`](../../../internal/drivers/iosxe/configdriver/iosxebuilder/) which calls `RegisterGNMIPathKeysForXE()` so the gNMI keyed-path table is populated regardless of which binary links the driver (the Wave 5A-fu fix for the side-effect-driven registry hazard).

- **[`transport/`](../../../internal/drivers/iosxe/configdriver/transport/)** — three transports (RESTCONF / NETCONF / gNMI) behind one [`Interface`](../../../internal/drivers/iosxe/configdriver/transport/transport.go). RESTCONF is non-transactional; NETCONF supports candidate datastore + commit; gNMI supports `Subscribe` (pushes on-change events through `SubscribeCapable`). Optional `TxFetcher` lets the engine read from a candidate during the Verifying phase. `Op` carries both string `Path` and structured `PathSpec []PathElement` (Wave 5A-fu + 7B) so keyed-list paths survive into the gNMI Set without ad-hoc string parsing.

- **[`intent/`](../../../internal/drivers/iosxe/configdriver/intent/)** — `Resolver`, `ResolvedIntent`, `KeyRules`, `CanonicalHash`, parameterised template expansion, and `secretRefs[]` merge.

- **[`schema/`](../../../internal/drivers/iosxe/configdriver/schema/)** — embedded `families.yaml` (per-family YANG/OpenConfig paths, key fields, dependencies, portal hints) and `yang-versions.yaml`.

### 3.4 New controllers

[`internal/controller/`](../../../internal/controller/) gains:

- **`IOSXEConfigBundleReconciler`** — fan-out loop. Selector-based device match → owner-referenced child `IOSXEConfig` CRs. Membership recomputation on device label changes (Wave 3A).
- **`configPrereqs` flow on `CiscoDeviceReconciler`** — bootstraps a synthetic `IOSXEConfig` from `CiscoDevice.spec.configPrereqs` so day-0 prerequisites (e.g. management VLAN, baseline AAA) apply before apphosting Pods land. Teardown step empties the source and lets `pruneOnRelinquish=true` revert the device-side state on `CiscoDevice` deletion (Wave 4A-fu + 7A.2 + 7A.4).

[`internal/aggregator/`](../../../internal/aggregator/) is new — an alternative topology (§4.4 below).

### 3.5 Per-pod reconciler

[`internal/provider/config_reconciler.go`](../../../internal/provider/config_reconciler.go) is the per-pod home for configuration reconcile. Contains both the legacy polling `Run()` loop and the controller-runtime [`Reconcile()`](../../../internal/provider/config_reconciler_controller.go) entry that the per-pod manager wires into a controller-runtime Manager via `SetupWithManager()`. Both paths share `reconcileOne()`, which (Wave 9.2) returns `(engine.Result, error)` so the controller-runtime caller's `RequeueAfter` and OTel span attribution come from the post-reconcile result rather than the pre-update CR.

### 3.6 Cross-cutting features

- **Family-leaser arbitration.** Two CRs claiming the same family on the same device do not race: the second goes to `Phase=LeaseBlocked` and requeues at the sub-TTL (15s) interval. Holder identity carries a runtime suffix (`#<podUID|workerUUID>`, Wave 7A.3) so cross-process duplicate-writer hazards during pod rollouts close cleanly.
- **Hash-based reconcile short-circuit.** [`intent.CanonicalHash`](../../../internal/drivers/iosxe/configdriver/intent/hash.go) is computed at resolve time and stored on `Status.LastAppliedHash`. Subsequent ticks short-circuit the engine if generation + hash + `Status.Phase=InSync` agree AND the drift-detect interval has not elapsed AND the trigger is not a `Subscribe` notification.
- **Subscribe fast-path.** `gNMI` `Subscribe` events arrive on `ConfigReconciler.SubscribeEvents` (a `<-chan event.GenericEvent`); [`SetupWithManager`](../../../internal/provider/config_reconciler_controller.go) registers a `source.Channel` so the per-pod controller-runtime path picks them up. The aggregator path consumes the same notify channel through its own bridge.
- **Replay annotation.** `config.cisco.vk/replay-from-log: <log-name>:<index|hash>` overrides the resolver's normal output with a historical body retained on the `ApplyLog` (when `spec.retainBody=true`). The annotation is cleared on a successful replay apply.
- **OTel reconcile span.** Per-tick root span on `ConfigReconciler.Reconcile`, attributes carry the device, the IOSXEConfig identity, the resolved phase, drift count, and apply outcomes.

### 3.7 Tooling

[`tools/`](../../../tools/) gains four binaries:

- **[`cisco-vk-config-lint`](../../../tools/cisco-vk-config-lint/)** — offline + cluster-mode validator for `IOSXEConfig` overlap, selector collisions, missing key fields, circular template refs.
- **[`cisco-vk-config-docs`](../../../tools/cisco-vk-config-docs/)** — generates HTML reference docs from `families.yaml` + CRD OpenAPI schemas + `IOSXETemplate.spec.parameters[]`.
- **[`cisco-vk-yang-sync`](../../../tools/cisco-vk-yang-sync/)** — consumes upstream Cisco-IOS-XE YANG tarballs, emits Go types and writer skeletons returning `ErrNotImplemented`.
- **[`terraform-provider-iosxeconfig`](../../../tools/terraform-provider-iosxeconfig/)** — Terraform provider wrapping the `IOSXEConfig` CRD CRUD path; in-tree pending Terraform Registry publish (§6.A).

---

## 4. Architectural alignment — how the two subsystems coexist

This is the section the user's brief specifically asked for: how does the original container-deployment architecture line up with the new configuration additions?

### 4.1 Single-pod coexistence model

The per-device `cisco-vk run` pod hosts **both** subsystems simultaneously:

```
┌─────────────────────── cisco-vk pod (one per CiscoDevice) ──────────────────────┐
│                                                                                  │
│   ┌── Apphosting (pre-existing) ───┐    ┌── ConfigReconciler (new) ──────────┐  │
│   │  AppHostingProvider :10250     │    │  controller-runtime Manager        │  │
│   │  AppHostingNode (Capacity, …)  │    │   • Reconciler (per CR tick)       │  │
│   │  pod_lifecycle.go              │    │   • SubscribeEvents source.Channel │  │
│   │  reconciler.go (app state)     │    │   • polling Run() fallback         │  │
│   │                                │    │  Engine + Writers + Transport      │  │
│   │  Transport: RESTCONF only      │    │  Transport: RESTCONF/NETCONF/gNMI  │  │
│   └─────────────────┬──────────────┘    └────────────────┬───────────────────┘  │
│                     │                                    │                       │
│                     │  shared CiscoDevice CR (read-only) │                       │
│                     ▼                                    ▼                       │
│             ┌───────────────────────────────────────────────────┐               │
│             │  Cat9k device (RESTCONF + NETCONF/830 + gNMI/57400)│              │
│             └───────────────────────────────────────────────────┘               │
│                                                                                  │
│   Optional: OTel exporter (shared); Subscribe bridge (gNMI notify fan-out)      │
└──────────────────────────────────────────────────────────────────────────────────┘
```

The two subsystems share exactly two things at runtime: (1) the read-only `CiscoDevice` CR (for the device address, credentials reference, transport selection), and (2) the OTel exporter (when enabled). Neither writes state the other reads. They run in different goroutine pools, expose different metrics, and target different device-side surfaces (apphosting's IOx vs. configuration's running-config).

### 4.2 The `CiscoDevice` CR as the bridge — and the `configPrereqs` link

`CiscoDevice` is the *only* CRD both subsystems consume. The branch extends it (in [`api/v1alpha1/types.go`](../../../api/v1alpha1/types.go), the small +68 / −x diff) with `spec.configPrereqs` — an embedded `IOSXEConfigTemplateSpec` that the [`CiscoDeviceReconciler`](../../../internal/controller/ciscodevice_controller.go) materialises into a synthetic `IOSXEConfig` CR. This is the architectural junction point: a device declares "before apphosting workloads land here, run *this* config bootstrap" without leaking apphosting-vs-config wiring details into operator-authored manifests.

The teardown direction matters too: when `CiscoDevice` is deleted, the configPrereqs CR is rewritten with empty source and `pruneOnRelinquish=true`, the engine reverts the device-side state, and only then is the synthetic node torn down. This sequencing was tightened in Wave 4A-fu and 7A.2/7A.4 to use `Status.ObservedGeneration` and `Status.Phase=InSync` as the gate so a stale prior-generation status cannot prematurely free the pod.

### 4.3 Two transport stacks — by design, not by accident

Apphosting still uses the original RESTCONF-only stack ([`internal/drivers/iosxe/driver.go`](../../../internal/drivers/iosxe/driver.go)). Configuration uses the multi-protocol stack under `configdriver/transport/`. The two stacks are independent: the apphosting driver imports `internal/drivers/common.NetworkClient`; the configdriver transport package imports `golang.org/x/crypto/ssh` (for NETCONF) and `google.golang.org/grpc` (for gNMI). They share neither connection pool nor auth state.

This is the right call. Apphosting only needs RESTCONF semantics (PUT/PATCH/DELETE for IOx app config), and forcing it through the configdriver's transactional, candidate-aware `transport.Interface` would be over-engineering for that use case. The cost is two TLS sessions per pod when both subsystems are active — operationally cheap, well below any device session limit.

### 4.4 Two topologies, exclusive-and-correct

The configuration subsystem can be deployed in one of two mutually-exclusive ways:

- **Per-pod (default).** Each `cisco-vk run` pod's `ConfigReconciler` reconciles the `IOSXEConfig` CRs targeting its own device. RBAC is fleet-wide (informers list all `IOSXEConfig` CRs cluster-wide) but writes are filtered to `Spec.DeviceRef.Name == r.DeviceName`. Failure isolation is per-device.

- **Aggregator.** A single cluster-level manager process runs an [`AggregatedReconciler`](../../../internal/aggregator/aggregator.go) that spins one `deviceWorker` goroutine per device. Each per-device pod runs with `DISABLE_IN_POD_CONFIG_RECONCILER=true`, so the in-pod ConfigReconciler is skipped and the apphosting provider is the only thing the pod hosts. Failure isolation is cluster-wide; the aggregator is the single point of contention for all devices.

Wave 1C added the [topology-exclusivity test](../../../internal/controller/aggregator_exclusivity_test.go) that pins this contract: configdriver-registered drivers in aggregator mode never see a per-device `Deployment` created with the in-pod ConfigReconciler enabled. The runtime-suffixed lease identity (Wave 7A.3) closes the cross-process duplicate-writer hazard at the lease layer, so even an unintended overlap window during a topology switch cannot produce concurrent writes to the same `(device, family)`.

The choice between topologies is a deployment-time decision driven by `helm upgrade --set aggregator.enabled=...`. The per-pod default scales linearly with fleet size; the aggregator scales constant-pod but is a single point of failure. Most operators should stay on per-pod unless the per-pod resource overhead is measurably uncomfortable.

### 4.5 RBAC and lease scope

Apphosting requires the per-device ServiceAccount to read its own pod and Secret references. Configuration requires it to read all `IOSXEConfig`-family CRs cluster-wide (it filters by `DeviceRef.Name` after listing) and to read/write `coordination.k8s.io/v1.Leases` in the configured lease namespace (default: per-pod namespace; overridable to a fleet-wide namespace via `CONFIG_LEASE_NAMESPACE`). The Helm chart's [`role.yaml`](../../../charts/cisco-virtual-kubelet/templates/role.yaml) was extended to cover both (Wave 1D + 2D).

The lease namespace choice has architectural consequence: a fleet-wide lease namespace (e.g. `cisco-vk-leases`) lets two `IOSXEConfig` CRs in *different* tenant namespaces still arbitrate against each other when they both target the same device. Per-pod lease namespace gives stronger isolation but loses cross-tenant arbitration. The default (per-pod) is the conservative choice; operators with multi-tenant fleets should override.

---

## 5. Architectural assessment — adjustments needed?

### 5.1 What works well

1. **The two subsystems are loosely coupled at runtime.** They share read-only state (`CiscoDevice`) and one optional sink (OTel). The configPrereqs flow uses a documented junction point (a synthetic `IOSXEConfig` materialised by the device controller) rather than leaky direct calls.
2. **The lease-backed arbitration model is honest.** `LeaseBlocked` is a first-class phase (Wave 8.2 + 9.1), the requeue is sub-TTL, the holder identity carries a runtime suffix, and the lease names validate as DNS-1123 subdomains for every shipped family. This is the correctness foundation for multi-CR-per-device authoring.
3. **The test discipline learned from each round closes durably.** The six architectural lessons collected in [`../implementation-status.md`](../implementation-status.md) §1.2 are not advice — each is anchored in a specific finding, was tested for, and the test stays in the suite. The reviewer's repeated "fake-client doesn't validate" finding pattern is exactly why the envtest follow-up matters (§6.C).
4. **The deletion path is conservative.** `pruneOnRelinquish` is set only on teardown reconciles (Wave 7A.4); steady-state day-0 reconciles are additive. This prevents the "operator removes a family from `ManagedFamilies` and the device is silently scrubbed of unrelated entries" surprise.
5. **The two topologies are exclusive at the test level.** The aggregator-exclusivity test makes "did we accidentally enable both" a build-time question, not a production question.

### 5.2 Architectural tensions worth naming

Two real tensions, both already on the roadmap with plans:

**Tension A — `internal/drivers/iosxe/configdriver/` houses platform-agnostic code.** The `engine/`, `intent/`, `schema/`, and most of `transport/` (RESTCONF, NETCONF, the lease layer, the result types) are not IOS-XE-specific. They live under the IOS-XE driver path because that is where the original platform-specific work began. A new contributor reading the layout could reasonably expect "configdriver" to mean "the configuration driver for IOS-XE" — and 80 % of the package is in fact platform-agnostic infrastructure that the IOS-XE driver consumes via [`iosxebuilder/`](../../../internal/drivers/iosxe/configdriver/iosxebuilder/).

The cosmetic relocation to `internal/configdriver/` (architectural watch-item #4) is the planned closure. It is deferred to Phase 10 ([`../implementation-status.md`](../implementation-status.md) §7.A.1) because the move conflicts noisily with two other in-flight workstreams (the v1 CRD cut and the netascode example corpus), and forcing three rebases on every collaborator is worse than the current mild-confusion cost. **No pre-merge change recommended;** track the relocation against the Phase-10 PR.

**Tension B — the CRD surface area at v1alpha1 is large.** Eight new CRDs, plus three nested type families (`IOSXEConfigTemplateSpec`, `ConfigurationSource`, `FamilyStatus[]`/`DriftEntry[]`/`Conditions[]`). The merge precedence chain has five layers (defaults → device groups → interface groups → templates → per-device). This is a feature: it's exactly the netascode shape operators want. But it is a lot of surface area to commit to without a v1 cut.

The CRD v1 promotion plan ([`../crd-v1-promotion-plan.md`](../crd-v1-promotion-plan.md)) addresses this with a conversion webhook, three-release phasing, and an explicit list of breaking changes (e.g. enum tightening, field renames). This is the right shape for the change. **No pre-merge change recommended;** keep the v1alpha1 surface as it is, land the v1 cut on a release-cut branch with the wider team's review.

### 5.3 Things that would warrant adjustment if found, but were checked and aren't there

I looked specifically for these because they are the failure modes I would expect on a 51K-line addition that grew alongside an active review chain:

- **Apphosting state mutated by the config subsystem.** *Not found.* The two reconcilers do not write to overlapping CRDs or device-side state. Apphosting talks to IOx (`/restconf/data/Cisco-IOS-XE-app-hosting:app-hosting-cfg-data`); config talks to running-config (`/restconf/data/Cisco-IOS-XE-native:native/...`). Distinct surfaces.
- **Implicit dependency on Subscribe being available.** *Not found.* The Subscribe fast-path is opt-in (transport must implement `SubscribeCapable`) and the polling fallback is the default. RESTCONF deployments degrade gracefully to interval-driven reconcile.
- **Status-write loops.** *Not found.* The hash short-circuit, the `ObservedGeneration` gate on conditions, and the deduplication of conflict-message owners mean a steady-state CR does not churn `Status.Conditions` every tick. Wave 2C's [`buildConflictMessage`](../../../internal/provider/config_reconciler.go) explicitly sorts owners + families for stable output.
- **Engine calling itself recursively or holding a lease across reconcile boundaries.** *Not found.* Leases are acquired per-tick, released implicitly via TTL renewal-or-expiry, and never held across the engine boundary in a way that would deadlock against a second reconciler.
- **Side-effect-driven registry that production binaries don't trigger.** *Found and fixed in Wave 5A-fu.* Pre-fix, the gNMI keyed-path registry was populated as a side-effect of `schema.LoadFamilies()` — only the docs generator called it. The fix routes registration through `iosxebuilder.RegisterGNMIPathKeysForXE()` from the IOS-XE driver's `init()`. This is now Lesson 2 in the architectural lessons list.

### 5.4 Recommendation

**Merge as-is.** The architectural shape is sound. The two tensions named in §5.2 already have written plans on file and are deliberately deferred to dedicated PRs where they will get the focused review they deserve. Forcing either into this branch would worsen the review surface for everyone.

The non-architectural follow-ups (envtest, live retests, Terraform Registry publish, netascode example corpus) all live in §6 below and are independently scheduled.

---

## 6. Pending roadmap

Eighteen open non-blocking items, none of which gate merge, after the §6.E code-level TODOs closed in commit `2e73766` and the §6.F.3 documentation cleanup closed in commit `5487dc0` (Wave 9D). Grouped by category, with a disposition column showing what's been actioned on this branch versus what is deferred to dedicated PRs or operator schedules:

**Disposition legend.** ✅ closed on this branch · ⏸ held per the §5 architectural recommendation (deferred to dedicated PR) · 🔒 requires lab device access (operator-scheduled).

### 6.A External infrastructure (2 items, both ⏸)

| Disposition | Item | Owner / source | Effort | Notes |
|---|---|---|---|---|
| ⏸ | Terraform Registry publish for `cisco-open/iosxeconfig` | [`../phase-8-residuals.md`](../phase-8-residuals.md) §2 | ~2 eng-days technical + paperwork | Provider account (Cisco/HashiCorp), GPG key in corporate KMS, signing workflow, Hashicorp-layout docs. Out of scope for this branch — depends on external paperwork. |
| ⏸ | netascode example corpus (~54 family pages) | [`../phase-8-residuals.md`](../phase-8-residuals.md) §3 | ~80 eng-hours | Author + lint each example; live-validate ~10 representative families on Cat9k under `driftPolicy=revert`. Includes lab access (see 6.D). |

### 6.B Architectural watch-items deferred with plans (3 items, all ⏸)

All three are explicitly deferred per §5.4 of this document. Pulling any into this branch would worsen the review surface and contradict the merge-readiness verdict.

| # | Disposition | Item | Plan RFC | Target landing | Effort |
|---|---|---|---|---|---|
| 4 | ⏸ | Cosmetic relocation `internal/drivers/iosxe/configdriver/...` → `internal/configdriver/` | [`../driver-extension-guide.md`](../driver-extension-guide.md) §7 | Phase 10 single PR | mechanical (touches every import path; conflicts with v1 CRD cut + netascode corpus if attempted now) |
| 9 | ⏸ | Log unification: logrus + zap → `slog` shims | [`../log-unification-plan.md`](../log-unification-plan.md) | Standalone PR | ~3 eng-days |
| 10 | ⏸ | CRD v1alpha1 → v1 promotion + conversion webhook | [`../crd-v1-promotion-plan.md`](../crd-v1-promotion-plan.md) | Release-cut branch (wider-team review window) | ~2 eng-weeks |

### 6.C Test / CI infrastructure (2 items, both ⏸)

| Disposition | Item | Source | Target landing | Notes |
|---|---|---|---|---|
| ⏸ | envtest infrastructure | [`../implementation-status.md`](../implementation-status.md) §1.2 (Lesson 1, 2, 5), §7.A.2 | Lands with the conversion-webhook PR (6.B item 10) | Durable closure for the recurring `fake.Client`-doesn't-validate gap (FU-2 / W7R-1 / W8FU-1). Right place to pay the etcd + apiserver binary dependency cost. |
| ⏸ | `internal/provider` package coverage (currently 25.7 %) | [`../architectural-review.md`](../architectural-review.md) §2.5; [`../implementation-status.md`](../implementation-status.md) §7.A.2 | Lands with envtest | The uncovered code is controller-runtime wiring (predicates, handler.Funcs, watch establishment) — needs envtest to exercise honestly. |

### 6.D Live retests against the lab Cat9K (8 paths, all 🔒)

Each modifies running device state and is the operator's call to schedule. Detailed in [`../latest-update.md`](../latest-update.md) §5; summary:

| Disposition | Path | Closing waves |
|---|---|---|
| 🔒 | NETCONF transactional commit, structured-only intent → `Phase=InSync` end-to-end | 1A-fu |
| 🔒 | NETCONF transactional + CLI block rejection → `Phase=Failed`, `ErrTransactionalCLIUnsupported`, no transport-side mutation | 7A.1 |
| 🔒 | `configPrereqs` deletion-driven cleanup → device clean of any prereq state created by the controller | 4A-fu + 7A.2 + 7A.4 |
| 🔒 | gNMI Set against `interface_ethernet[GigabitEthernet=0/0/0]` → keyed-path PathSpec preserved verbatim on the wire | 5A-fu + 7B |
| 🔒 | Credential Secret rotation with overlap window → new pod takes the lease cleanly, transient `LeaseBlocked` with sub-TTL requeue, no concurrent writes | 6B + 7A.3 + 8.2 + 9.2 |
| 🔒 | Real-apiserver Lease creation for an underscore family (e.g. `interface_ethernet`) → DNS-1123 sanitisation works end-to-end | 8.1 |
| 🔒 | Real-apiserver acceptance of `status.phase=LeaseBlocked` → CRD enum admits the value | 9.1 |
| 🔒 | `driftPolicy: revert` live write → flip a CR from `report` to `revert` for one family, watch the device-side change, flip back | per §5 |

### 6.E Code-level TODOs (4 items, all ✅ closed in commit `2e73766`)

| Disposition | Location | Topic | What changed |
|---|---|---|---|
| ✅ | [`internal/drivers/fake/driver.go`](../../../internal/drivers/fake/driver.go) — `UpdatePod` | Was a no-op log + TODO | Now finds the pod by namespace/name in `d.pods` and replaces it; returns an explicit "could not find pod to update" error when no match exists, mirroring `DeletePod`'s surface. |
| ✅ | [`internal/drivers/fake/driver.go`](../../../internal/drivers/fake/driver.go) — `GetPodStatus` | Stale TODO comment from the pre-storage scaffold state | Comment removed; the actual implementation already used `common.FindPod` against `d.pods`. |
| ✅ | [`internal/drivers/fake/driver.go`](../../../internal/drivers/fake/driver.go) — `ListPods` | Was returning `nil, nil` | Now returns a `[]*v1.Pod` over `d.pods` so consumers iterating the list see the seeded pods. |
| ✅ | [`internal/provider/defaults.go`](../../../internal/provider/defaults.go) — `InitNodeSystemInfo` | Returned every field as `"unknown"`, briefly visible in `kubectl get nodes -o wide` between pod start and the first heartbeat | Replaced with values consistent with what `AppHostingNode.syncNodeStatus` writes once the driver responds: `OperatingSystem="Cisco"`, `OSImage="IOS-XE"`, `ContainerRuntimeVersion="ios-xe-iox"`. `Architecture` stays empty (post-sync value is the device ProductID, not knowable until the driver's `GetDeviceInfo` is called). |

### 6.F Reviewer recommendations from the most recent round (3 items)

Per [`../external-review-wave9-status.md`](../external-review-wave9-status.md) §6 — no blocking findings, three "before release tag" recommendations:

| Disposition | Item | Notes |
|---|---|---|
| 🔒 | Live apiserver validation of `status.phase=LeaseBlocked` | Same as §6.D row 7. Operator-scheduled. |
| 🔒 | Live apiserver Lease creation for an underscore family | Same as §6.D row 6. Operator-scheduled. |
| ✅ | Documentation cleanup of `implementation-status.md` §1 | Closed by Wave 9D in commit `5487dc0`. |

### 6.G Aggregate

| Category | Count | ✅ closed on this branch | ⏸ deferred (with plans) | 🔒 needs lab device | Approximate effort for the remainder |
|---|---:|---:|---:|---:|---|
| External infrastructure | 2 | 0 | 2 | 0 | ~2 eng-days + paperwork + ~80 eng-hours content |
| Watch-items | 3 | 0 | 3 | 0 | ~3 days + ~3 weeks + mechanical |
| Test/CI | 2 | 0 | 2 | 0 | ~2 weeks (lands with conversion-webhook PR) |
| Live retests | 8 | 0 | 0 | 8 | operator-scheduled |
| Code TODOs | 4 | **4** | 0 | 0 | — (closed) |
| Reviewer recommendations | 3 | **1** | 0 | 2 | operator-scheduled |
| **Total** | **22** | **5** | **7** | **10** | — |

Five of the twenty-two items closed on this branch (4 × §6.E TODOs in `2e73766`, plus §6.F.3 docs cleanup in `5487dc0`). Seven remain ⏸-deferred to dedicated PRs by explicit plan; ten are 🔒-blocked on operator-scheduled lab access. **No item that is reasonable to action on this branch is still open.**

---

## 7. Verdict

The branch is architecturally ready to merge. The pre-existing apphosting container-deployment architecture and the new configuration-management subsystem coexist cleanly inside the same `cisco-vk` pod, share only the `CiscoDevice` CR (the documented junction point) and an optional OTel sink, and have independent failure boundaries. The five external review rounds closed all 26 findings with focused tests; the most recent round explicitly accepted the day-0/day-2 claim with no new blocking work.

The two architectural tensions worth naming — the platform-agnostic code living under `internal/drivers/iosxe/configdriver/...`, and the v1alpha1 CRD surface area — both have written plans on file and are deliberately deferred to dedicated PRs (Phase 10 cosmetic relocation; v1 promotion on a release-cut branch). Pulling either into this branch would worsen the review surface; deferring is the correct architectural call.

Twenty-two follow-up items were enumerated; five closed on this branch (the four §6.E code-level TODOs in commit `2e73766` and the §6.F.3 documentation cleanup in commit `5487dc0`). The remaining seventeen are either ⏸-deferred to dedicated PRs by explicit plan (Phase-10 cosmetic relocation, log unification, v1 CRD cut + envtest, Terraform Registry publishing, netascode example corpus) or 🔒-blocked on operator-scheduled lab access (eight live retests, two reviewer-recommended live validations). No item that was reasonable to action on this branch is still open. The envtest follow-up is the durable closure for the recurring `fake.Client`-doesn't-validate lesson and is correctly scoped to land with the conversion-webhook PR rather than as a stand-alone effort.

**Recommendation: merge.** The architectural adjustments worth making are already scheduled as separate PRs; making them on this branch would defeat the purpose of the planned phasing.

---

## Appendix A — cross-reference index

This document is a synthesis. The authoritative RFCs for each topic remain:

| Topic | RFC |
|---|---|
| Phase-by-phase status sweep, watch-items, pending register | [`../implementation-status.md`](../implementation-status.md) |
| Most-recent round narrative ("what just changed") | [`../latest-update.md`](../latest-update.md) |
| Original phase taxonomy + design intent | [`../iosxe-config-driver-review.md`](../iosxe-config-driver-review.md) |
| Architectural watch-items 1–12 | [`../architectural-review.md`](../architectural-review.md) |
| Five external review rounds + responses | `../external-review*.md` (10 files) |
| v1 CRD promotion plan | [`../crd-v1-promotion-plan.md`](../crd-v1-promotion-plan.md) |
| slog backend plan | [`../log-unification-plan.md`](../log-unification-plan.md) |
| External Phase-8 residuals | [`../phase-8-residuals.md`](../phase-8-residuals.md) |
| Driver extension contract (for new platform additions) | [`../driver-extension-guide.md`](../driver-extension-guide.md) |
