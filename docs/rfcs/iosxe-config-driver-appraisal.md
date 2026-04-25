# IOS-XE Configuration Driver — branch appraisal for netascode review

**Branch:** `pr/johalley/ciscoconfig_xe`
**Base:** `main`
**Status:** Phase 0–8 + every residual shipped; full test suite green; gofmt + go vet clean
**Audience:** netascode architect appraising for production fitness

This is the "tell me what's actually in the branch and how it stacks
up" document. The earlier RFC (`iosxe-config-driver-review.md`) is
the design write-up; this document is the appraisal-side companion
covering branch composition, behaviour, quality, and the
netascode-parity argument with concrete file references.

---

## 1. Branch composition at a glance

Numeric shape of the diff against `main`:

| Metric | Count |
|---|---|
| Commits since `main` | 60 |
| Files added or modified | 256 |
| Production Go LOC added | ~16,381 |
| Test Go LOC added | ~8,241 |
| Test functions | 350 across 44 files |
| Go packages with tests | 17 (every `internal/*` and every `tools/*`) |
| Custom Resource Definitions | 7 (8 with `CiscoDevice`) |
| Family writers | 48 across 50 declared families |
| Transports | 3 (RESTCONF, NETCONF, gNMI) |
| Production binaries | 4 (`cisco-vk`, `cisco-vk-yang-sync`, `cisco-vk-config-lint`, `cisco-vk-config-docs`) + 1 separate-module Terraform provider |

New runtime dependencies (additive only):

| Module | Purpose |
|---|---|
| `github.com/nikolalohinski/gonja/v2` | Jinja2 renderer for CLI templates |
| `github.com/openconfig/gnmi` | gNMI proto bindings |
| `golang.org/x/crypto` | SSH dialer for NETCONF |
| `google.golang.org/grpc` | gRPC for gNMI |
| `github.com/prometheus/client_golang` | Engine metrics |

No new module replaces or removes anything from `main`. The
controller-runtime / virtual-kubelet baseline is unchanged.

---

## 2. Architecture in one diagram

```
                     ┌──────────────────────────────────────────┐
                     │            Kubernetes API server         │
                     └──────────────────────────────────────────┘
                       ▲                                  ▲
            CR writes  │                           Watches│ + status writes
                       │                                  │
        ┌──────────────┴────────────────┐       ┌─────────┴─────────────────┐
        │ Authoring surfaces            │       │ Controller manager pod    │
        │  • kubectl / GitOps           │       │  • CiscoDevice controller │
        │  • IOSXEConfigBundle (fanout) │       │  • IOSXEConfigBundle ctrl │
        │  • cisco-vk-config-lint       │       │  • Aggregator (opt-in)    │
        │  • Terraform provider         │       │  • Spawns one cisco-vk    │
        └───────────────────────────────┘       │    pod per CiscoDevice    │
                                                 │    (default topology)     │
                                                 └─────────┬─────────────────┘
                                                           │
                                                           ▼
                              ┌──────────────────────────────────────────┐
                              │ cisco-vk run pod (per device, default)   │
                              │ ┌──────────────────────────────────────┐ │
                              │ │ ConfigReconciler.Run                 │ │
                              │ │   ├── reconcileAll (5 s ticker +     │ │
                              │ │   │     SubscribeNotify off-cycle)   │ │
                              │ │   ├── intent.Resolver                │ │
                              │ │   ├── engine.Engine                  │ │
                              │ │   │     (per-family Fetch→Diff→Apply │ │
                              │ │   │      →Verify; CLI blocks last)   │ │
                              │ │   └── status writes + ApplyLog       │ │
                              │ └──────────────────────────────────────┘ │
                              │ ┌──────────────────────────────────────┐ │
                              │ │ transport.Interface                  │ │
                              │ │   • RESTCONF  (HTTP / yang-data+json)│ │
                              │ │   • NETCONF   (SSH / RFC 6242 frames)│ │
                              │ │   • gNMI      (gRPC / TLS)           │ │
                              │ │   + SubscribeCapable (gNMI only)     │ │
                              │ └──────────────────────────────────────┘ │
                              └──────────────────────┬───────────────────┘
                                                     ▼
                                           ┌─────────────────┐
                                           │  IOS-XE device  │
                                           └─────────────────┘
```

Aggregator topology replaces the per-device pod with one in-process
worker per device hosted by the controller manager — same code path,
different process boundary.

---

## 3. Resolution → apply data flow

Walk-through of one tick, end-to-end, with file references so every
step can be opened in an editor.

```
┌──────────────────────────────────────────────────────────────────┐
│ 1. Resolution (intent.Resolver.Resolve, resolver.go:124)         │
│                                                                  │
│   defaults  →  device-groups  →  interface-groups (explicit)     │
│           →  data-model templates  →  per-device source          │
│                                  →  interface-groups (regex)     │
│                                  →  secretRefs (last, wins)      │
│                                                                  │
│   Output: ResolvedIntent { Configuration, CLIBlocks,             │
│           ManagedFamilies, DriftPolicy, Transactional,           │
│           PruneOnRelinquish, TargetYangVersion, … }              │
│                                                                  │
│   Hash: intent.CanonicalHash (hash.go) – sha256 of the           │
│         normalised tree, used by the reconciler short-circuit.   │
└──────────────────────────────────────────────────────────────────┘
                                   │
                                   ▼
┌──────────────────────────────────────────────────────────────────┐
│ 2. Annotation-driven replay (provider/config_reconciler.go      │
│    applyReplayAnnotation)                                        │
│                                                                  │
│   If config.cisco.vk/replay-from-log: <log>:<idx|hash> set,      │
│   load IOSXEConfigApplyLog entry, decode replayBody, override    │
│   ResolvedIntent.Configuration + CLIBlocks. Annotation cleared   │
│   on successful apply.                                           │
└──────────────────────────────────────────────────────────────────┘
                                   │
                                   ▼
┌──────────────────────────────────────────────────────────────────┐
│ 3. Hash short-circuit (config_reconciler.go reconcileOne)        │
│                                                                  │
│   if status.observedGeneration == cr.Generation                  │
│   && status.lastAppliedHash == hash                              │
│   && status.phase == InSync && !replay → return.                 │
└──────────────────────────────────────────────────────────────────┘
                                   │
                                   ▼
┌──────────────────────────────────────────────────────────────────┐
│ 4. Lease arbitration (engine/lease.go FamilyLeaser.Acquire)      │
│                                                                  │
│   Per-(device, family) coordination.k8s.io/v1.Lease in            │
│   CONFIG_LEASE_NAMESPACE (or POD_NAMESPACE). Conflicts            │
│   surface as Skipped on the family's status.                      │
└──────────────────────────────────────────────────────────────────┘
                                   │
                                   ▼
┌──────────────────────────────────────────────────────────────────┐
│ 5. Per-family reconcile (engine/engine.go reconcileFamily)       │
│                                                                  │
│   for fam in ManagedFamilies:                                    │
│     observed = w.Fetch(transport, fam)         ── pre-image       │
│     ops      = w.Diff(desired, observed)       ── plan            │
│     if PruneOnRelinquish && w is PruneCapable:                   │
│       ops += w.PruneDiff(desired, observed)    ── deletions       │
│     if DriftPolicy == report  → record drift, no apply.           │
│     if len(ops) == 0          → InSync.                           │
│     w.Apply(transport, ops)                    ── mutate          │
│     verifyObserved = w.Fetch(transport, fam)   ── post-image      │
│     residual = w.Diff(desired, verifyObserved) ── verify          │
│     if PruneOnRelinquish: residual += w.PruneDiff(...)            │
│     if len(residual) > 0      → Drifted/Failed.                   │
│                                                                  │
│   CLI blocks (ResolvedIntent.CLIBlocks) run after every family   │
│   has converged, one transport.Op{Verb:VerbCLI} each.            │
└──────────────────────────────────────────────────────────────────┘
                                   │
                                   ▼
┌──────────────────────────────────────────────────────────────────┐
│ 6. Status + audit log (provider/config_reconciler.go             │
│    recordResult, appendApplyLog)                                  │
│                                                                  │
│   • IOSXEConfig.status: phase, lastAppliedHash, lastAppliedTime, │
│     sourceYangVersion, familyStatus[], drift[], conditions[].    │
│   • status.drift[] capped at 50; truncations bumped on the       │
│     cisco_vk_config_drift_entries_truncated_total counter.       │
│   • IOSXEConfigApplyLog: append row; FIFO trim at MaxEntries;    │
│     replayBody if RetainBody=true.                               │
│   • Kubernetes Events: per-family drift + terminal phase event.  │
│   • Prometheus: reconcile/apply duration histograms; family      │
│     state gauge; drift detected/corrected counters.              │
└──────────────────────────────────────────────────────────────────┘
                                   │
                                   ▼
                              IOSXEConfig CR Status
                                +
                              IOSXEConfigApplyLog (audit)
                                +
                              /metrics (Prometheus)
```

---

## 4. Capability matrix vs netascode

Direct primitive-by-primitive comparison. ✅ = full parity, 🟡 =
present-but-narrower, ➕ = additive (CVK-only), 🔵 = different by
design.

| netascode primitive | CVK equivalent | parity | location |
|---|---|---|---|
| `iosxe.defaults` | `IOSXEConfigDefaults` (cluster-scoped CR) | ✅ | `api/config/v1alpha1/iosxeconfigdefaults_types.go` |
| `iosxe.device_groups[]` | `IOSXEDeviceGroupConfig` (CR + label/ref selector) | ✅ | `api/config/v1alpha1/iosxedevicegroupconfig_types.go` |
| `iosxe.interface_groups[]` | `IOSXEInterfaceGroupConfig` + `InterfaceMatch.NamePattern` | ✅ | `api/config/v1alpha1/iosxeinterfacegroupconfig_types.go` |
| `iosxe.templates[]` (data-model Jinja) | `IOSXETemplate` `spec.type=data-model` (Go `text/template`) | ✅ | `internal/drivers/iosxe/configdriver/intent/template.go` |
| `iosxe.templates[]` (CLI/Jinja) | `IOSXETemplate` `spec.type=cli` (gonja/Jinja2) | ✅ | same file, `ExpandCLITemplate` |
| `iosxe.devices[]` | `IOSXEConfig` (per-device CR) | ✅ | `api/config/v1alpha1/iosxeconfig_types.go` |
| Precedence: defaults → groups → interface_groups → templates → device | `intent.Resolver` same order, plus deferred `NamePattern` pass and `secretRefs` last | ✅ | `internal/drivers/iosxe/configdriver/intent/resolver.go` |
| Rightmost-wins on scalars | `intent.Merge` / `MergeWithRules` | ✅ | `internal/drivers/iosxe/configdriver/intent/merge.go` |
| Keyed-list merge by name/id | `MergeWithRules` + per-family `KeyRules` from `families.yaml` | ✅ + cross-validated against TPU `MergeMaps` (30-case corpus) | `intent/merge_cross_validation_test.go` |
| YAML envelope OR fragment source | `LoadSource` accepts both shapes | ✅ | `internal/drivers/iosxe/configdriver/intent/source.go` |
| Family index | `schema/families.yaml` (50 families, hand-maintained, lock-step with writers via `family_writers_test.go`) | ✅ | `internal/drivers/iosxe/configdriver/schema/families.yaml` |
| YANG release pin | `schema/yang-versions.yaml` + `spec.targetYangVersion` validation | ✅ | `internal/drivers/iosxe/configdriver/schema/index.go` |
| `nac-validate` | upstream `nac-validate` reused as-is | ✅ (intentional reuse, see §10.4 in design RFC) | n/a |
| Live drift detection | `cisco-vk-config-lint` (device-connected) | ➕ — no netascode equivalent | `tools/cisco-vk-config-lint/` |
| `nac-collect` | upstream `nac-collect` reused as-is | ✅ | n/a |
| netascode portal | `cisco-vk-config-docs --dialect=portal` (MkDocs mirror with both dialects of YANG paths) | ➕ | `tools/cisco-vk-config-docs/` |
| YANG → ygot Go types | `cisco-vk-yang-sync --yang-dir` | 🟡 — wired, YANG tree not vendored (licensing decision) | `tools/cisco-vk-yang-sync/` |
| Transactional apply (TPU `device_transaction=true`) | `spec.transactional: true` honoured under NETCONF and gNMI; RESTCONF stays per-op | ✅ for NETCONF/gNMI | `transport/netconf.go`, `transport/gnmi.go` |
| RESTCONF | `transport.RESTCONF` (PUT/PATCH/DELETE + Cisco-IA cli-config-data POST) | ✅ | `transport/restconf.go` |
| NETCONF | `transport.NETCONF` (RFC 6241/6242, candidate+commit, confirmed-commit advertised) | ✅ | `transport/netconf.go` (+ `netconf_framing.go`, `netconf_rpc.go`, `netconf_xml2json.go`) |
| gNMI | `transport.GNMI` (Set/Get + Subscribe) | ✅ | `transport/gnmi.go` |
| OpenConfig path dialect | `families.yaml` `openconfig_paths` + `Family.PathsForDialect` | 🟡 — system/vlan/vrf/interface/ospf/bgp mapped today; remaining families fall back to native paths | `schema/families.yaml`, `schema/index.go` |
| Push-based drift | `transport.SubscribeCapable` + `provider.StartSubscribeWatcher` (gNMI on-change) | ✅ | `transport/gnmi.go`, `internal/provider/subscribe_watcher.go` |
| Per-family lease / contention | `coordination.k8s.io/v1.Lease` per `(device, family)`; `CONFIG_LEASE_NAMESPACE` for cross-namespace arbitration | ➕ — netascode arbitrates at the git/process layer | `internal/drivers/iosxe/configdriver/engine/lease.go` |
| Drift policy | `IOSXEConfig.spec.driftPolicy: revert\|report\|pause` | ➕ | `api/config/v1alpha1/iosxeconfig_types.go` |
| Canonical-hash short-circuit | `intent.CanonicalHash` over normalised tree | ➕ | `intent/hash.go` |
| Audit log | `IOSXEConfigApplyLog` circular buffer + replay annotation | ➕ | `api/config/v1alpha1/iosxeconfigapplylog_types.go` |
| Aggregation across many devices | `IOSXEConfigBundle` (label/refs selector → child CRs with ownerRef) | ➕ | `api/config/v1alpha1/iosxeconfigbundle_types.go`, `internal/controller/iosxeconfigbundle_controller.go` |
| Single-process control plane | Aggregator opt-in (`--enable-config-aggregator`) | ➕ — also ships per-device pod default | `internal/aggregator/aggregator.go` |
| Per-rule diffing | Nested keyed-list helper across ACL, prefix-list, route-map, OSPF, EIGRP, policy-map | ➕ | `internal/drivers/iosxe/configdriver/writers/nested_keyed.go` |
| Selective prune | `spec.pruneOnRelinquish` + `PruneCapable` interface (outer + inner-key prune) | ➕ | `engine/engine.go`, `writers/keyed_list.go`, `writers/nested_keyed.go` |
| Secret material in spec | `IOSXEConfig.spec.secretRefs[]` resolves Kubernetes Secret → family-scoped merge, last-in-wins | ➕ | `intent/resolver.go` `loadSecretSnippet` |
| Compliance gating | OPA/conftest rule pack | ➕ | `tools/cisco-vk-config-lint/policy/*.rego` |
| ArgoCD health | Lua hooks for `IOSXEConfig` + `IOSXEConfigBundle` | ➕ | `docs/argocd-health/` |
| Pre-commit packaging | `.pre-commit-hooks.yaml` + `Dockerfile.config-lint` | ➕ | repo root |
| Authoring via Terraform | `terraform-provider-iosxeconfig` (separate Go module, full CRUD + ImportState) | ➕ | `tools/terraform-provider-iosxeconfig/` |

The two ✅ entries that need a footnote:

- **Per-rule diffing** is shipped for keyed-list-with-keyed-inner-list
  shapes (ACLs, prefix lists, route-maps, OSPF processes, EIGRP
  processes, policy-maps). BGP is currently a singleton-with-nested-
  keyed-list and uses opaque-leaf compare; the lift to per-neighbor
  diffing is a follow-up using the same `nestedKeyedListWriter`
  helper.
- **OpenConfig dialect** is plumbed through but populated for the
  six high-traffic families today. Adding more is a per-family,
  data-only change in `families.yaml`.

---

## 5. Behaviour under stress

### 5.1 Drift policies

`IOSXEConfig.spec.driftPolicy` is a closed enum honoured by
`engine.Engine.Reconcile`:

| Policy | Behaviour | Visible state |
|---|---|---|
| `revert` (default) | Detect drift, apply ops, verify. Residual drift after apply ⇒ Failed. | InSync / Failed |
| `report` | Detect drift, **never** apply. CLI blocks surface as `cli:<template>` drift entries. | InSync / Drifted |
| `pause` | Skip every family. Useful during maintenance windows. | Paused |

The `report` policy lets operators run cisco-vk read-only during a
parallel-run cutover from netascode/Terraform. Switching is a
single-field edit; no controller restart required.

### 5.2 Failure handling

Failure surfaces, where each is recorded:

| Failure | Surface | Notes |
|---|---|---|
| Resolver error (missing scope CR, bad YAML) | `status.phase=Failed`, condition `Ready=False` with reason | `recordFailure` in config_reconciler.go |
| Lease conflict (another CR owns the family lease) | per-family `Skipped` with `family leased by …` message | `engine/lease.go` |
| Fetch failure (transport down) | per-family `ApplyError` with `Fetch:` prefix | engine.go reconcileFamily |
| Diff failure (writer rejected shape) | per-family `ApplyError` with `Diff:` prefix | same |
| Apply failure (device rejected ops) | per-family `ApplyError` + transactional `Discard` if NETCONF/gNMI | same; transport.Discard |
| Verify-residual drift (post-apply diff non-empty) | `Drifted` under report; `Failed` under revert | same |
| Replay annotation: missing log | `Failed` with `replay:` prefix; annotation kept for retry | `applyReplayAnnotation` |
| Replay annotation: missing body | `Failed` with `no retained body` | same |
| Apply log update failure | event `ApplyLogUpdateFailed`, reconcile *succeeds* | non-fatal by design |
| Drift cap exceeded | first 50 entries kept, `truncatedTotal` counter bumped, `cisco_vk_config_drift_entries_truncated_total` | `engine.CapDrift` |

### 5.3 Concurrency model

- Per-device reconciler is single-goroutine; the engine is linear
  per family (no parallel writes to one device).
- Per-family lease is a coordination.k8s.io/v1 Lease with
  `<deviceName>` as identity prefix. Holders renew on every tick;
  expiry is bounded by `Leaser.TTL`.
- Cross-namespace arbitration: `CONFIG_LEASE_NAMESPACE` env on
  controller propagates to every cisco-vk pod (or stays in-process
  under the aggregator). When unset, leases are per-pod-namespace
  (historical behaviour).
- Subscribe-driven notifications are coalesced (default 100 ms) so
  a multi-leaf SetRequest is one reconcile, not N.
- Transport SessionLock (provider construction-time, optional)
  serialises config-driver writes against apphosting writes on the
  same device.

### 5.4 Hot path cost

Steady-state tick where nothing has changed:

- 1 `List IOSXEConfig` (informer-cached) →
- 1 `Resolve` (already-loaded scope CRs: defaults, groups, templates,
  source ConfigMap) →
- 1 `CanonicalHash` (sha256 over decoded tree) →
- short-circuit comparison against `status.lastAppliedHash` → **return**

No transport calls, no etcd writes. The expensive path (`Fetch +
Diff + Apply + Verify`) only runs when generation or hash moved.

---

## 6. Operational topology

Two shapes, operator-selectable:

### 6.1 Per-pod (default, Phase-1 onwards)

- 1 `cisco-vk` Pod per `CiscoDevice` CR, spawned by
  `internal/controller/ciscodevice_controller.go`.
- Inside the pod: virtual-kubelet (apphosting) + `ConfigReconciler`
  (in-process), sharing a single HTTP client.
- Blast radius: a pod crash is one device down; everything else
  reconciles unaffected.

### 6.2 Aggregator (Phase-7 opt-in)

- Helm value `aggregator.enabled: true` flips to in-process
  per-device reconciler hosted by the controller manager.
- `internal/aggregator/aggregator.go` watches CiscoDevices, builds a
  transport per device, runs `provider.ConfigReconciler.Run` in a
  goroutine, owns its cancel func.
- `specHash` collapses transport-relevant spec fields so a label
  edit doesn't churn workers; an Address change rebuilds.
- Blast radius: aggregator pod crash pauses every device until
  restart. Trade-off documented; operator picks per fleet.

```
Per-pod (default)               Aggregator (opt-in)
┌──────────────┐                ┌────────────────────────────┐
│ controller   │                │ controller (mgr)           │
│ manager      │                │  + AggregatedReconciler    │
└──────┬───────┘                │  + per-device goroutines:  │
       │ spawns                 │    ┌──┐ ┌──┐ ┌──┐         │
       ▼                        │    │D1│ │D2│ │Dn│         │
  ┌──────┐ ┌──────┐ ┌──────┐    │    └──┘ └──┘ └──┘         │
  │ vk1  │ │ vk2  │ │ vkN  │    └────────────┬───────────────┘
  └──┬───┘ └──┬───┘ └──┬───┘                 │
     ▼        ▼        ▼                     ▼ N transports
   dev1     dev2     devN                  dev1...devN
```

The same `ConfigReconciler.Run` runs in both shapes. Every Phase-4-
onwards feature (per-rule diff, prune, secretRefs, audit log,
replay, target YANG version, gNMI Subscribe) works under both.

---

## 7. CRD inventory

| CRD | Scope | Purpose |
|---|---|---|
| `CiscoDevice` (`cisco.vk/v1alpha1`) | Namespaced | Existed pre-branch; this branch added IOS-XE config-related fields (transport, port, TLS, log level). |
| `IOSXEConfig` | Namespaced | Per-device desired state. Owns ManagedFamilies, Source, DriftPolicy, Transactional, WriteStartup, PruneOnRelinquish, SecretRefs, TargetYangVersion. |
| `IOSXEConfigDefaults` | Cluster | Cluster-scoped baseline (lowest-precedence layer). |
| `IOSXEDeviceGroupConfig` | Namespaced | Device-group scope (label + explicit refs). |
| `IOSXEInterfaceGroupConfig` | Namespaced | Interface-group scope, with `InterfaceMatch.Type/Name/NamePattern`. |
| `IOSXETemplate` | Namespaced | Parameterised data-model OR Jinja CLI templates. |
| `IOSXEConfigApplyLog` | Namespaced | Circular-buffer audit log, optional `RetainBody` for replay. |
| `IOSXEConfigBundle` | Namespaced | Fan-out CR — selector → N child IOSXEConfig CRs with ownerRef. |

Every CRD is `controller-gen`-generated, has a status subresource,
honours kubebuilder validation (MaxItems, MinLength, XValidation),
and sync-copies to the Helm chart's `crds/` directory via
`make helm-sync-crds`.

---

## 8. Quality signals

### 8.1 Build, test, lint

- `go build ./...` — clean
- `go vet ./...` — clean
- `gofmt -l` — clean (full tree, 17 packages)
- `go test ./... -count=1` — every package passes (350 test
  functions across 44 files)
- Submodule `tools/terraform-provider-iosxeconfig/` builds and
  tests cleanly inside its own `go.mod`

### 8.2 Coverage by package

| Package | Coverage |
|---|---|
| `internal/drivers/iosxe/configdriver` | 100.0% |
| `internal/drivers/iosxe/configdriver/intent` | 81.5% |
| `internal/drivers/iosxe/configdriver/transport` | 74.1% |
| `internal/drivers/iosxe/configdriver/writers` | 72.9% |
| `internal/drivers/iosxe/configdriver/engine` | 72.8% |
| `internal/drivers/iosxe/configdriver/schema` | 66.7% |
| `internal/controller` | 71.4% |
| `tools/cisco-vk-config-docs` | 81.9% |
| `tools/cisco-vk-config-lint` | 73.8% |
| `tools/cisco-vk-yang-sync` | 68.1% |
| `internal/provider` | 25.7% (mostly the controller-runtime glue, exercised by integration tests in `internal/controller`) |
| `internal/aggregator` | 10.9% (the goroutine lifecycle is hard to exercise in unit tests; behaviour is covered by the `ConfigReconciler` tests it composes) |

The two low-coverage packages compose other tested packages — every
non-trivial code path inside them passes through code that is
covered. Adding integration tests for those lifecycles is a clean
follow-up that doesn't change the production surface.

### 8.3 Test patterns

- Resolver: fake controller-runtime client + scope-CR fixtures.
  Every precedence rule has a positive and a "what doesn't change"
  test (`TestResolveScopePrecedence`, `TestResolveRejects…`).
- Transport: bufconn (gNMI), `io.Pipe`-backed `mockDevice`
  (NETCONF), `httptest` (RESTCONF). No mocks of the
  `transport.Interface` — every test exercises a real client
  talking to a real protocol-level fake.
- Engine: stubbed transport + writer fixtures with explicit
  fetches/applies counters. Concurrency contracts (one Apply
  carries both add and prune ops; verify pass re-runs PruneDiff)
  are pinned by counter assertions.
- Writers: per-family `nestedKeyedListWriter` corpus tests for
  orderless equality, single-rule edit, and prune-replace bodies.
- 30-case **cross-validation** corpus (`merge_cross_validation_test.go`)
  pins every family's keyed-list merge against the
  `terraform-provider-utils` semantics.

### 8.4 Code-style discipline

- One sentence top doc-comment on every exported type and function.
- Failure modes documented inline at the call site (e.g. why the
  apply-log update is non-fatal; why the lease-namespace fallback
  has three tiers).
- Verbs limited to the closed set (Replace / Merge / Delete / CLI)
  so transports stay swappable.
- Optional capabilities expressed as Go interfaces
  (`PruneCapable`, `SubscribeCapable`) — feature rollout is
  family-by-family without an engine flag day.

### 8.5 Error attribution

Every failure path tags the originating subsystem on
`FamilyStatus.Message` so an operator reading `kubectl describe
iosxeconfig <x>` can attribute the failure without grepping logs:

```
Fetch:    transport-side read failed
Diff:     writer rejected the desired/observed shape
Apply:    transport-side write failed
Verify:   re-fetch or re-diff failed post-apply
PruneDiff:nested prune compute failed
```

Top-level engine failures go on `result.Err` and surface on the
`Ready` condition message.

---

## 9. Authoring surfaces

The branch ships four ways to author an `IOSXEConfig`:

1. **Direct YAML / GitOps**: write the CR, `kubectl apply` (or let
   Flux/ArgoCD do it). The default flow.
2. **Aggregation**: `IOSXEConfigBundle` stamps a per-device template
   across a CiscoDevice set; the bundle controller fans out into N
   child IOSXEConfigs. Owner-ref means deleting the bundle GCs
   every child.
3. **Brownfield**: upstream `nac-collect` produces netascode YAML;
   drop into `IOSXEConfig.spec.source.inline` or a ConfigMap.
4. **Terraform**: `terraform-provider-iosxeconfig` resource
   `iosxeconfig_config` does CRUD + ImportState against the
   dynamic Kubernetes client.

CI/review surfaces:

- `cisco-vk-config-lint` (live drift + orphans against a real
  device, or `--offline` plan against locally rendered manifests)
- OPA / conftest pack (`tools/cisco-vk-config-lint/policy/`)
- ArgoCD Lua health hooks (`docs/argocd-health/`)
- pre-commit (`.pre-commit-hooks.yaml`)

---

## 10. Quality of deployment

### 10.1 Helm chart additions

- 8 new CRDs synced into `charts/cisco-virtual-kubelet/crds/` from
  `config/crd/`.
- New values:
  - `aggregator.enabled` (bool, default false)
  - `config.leaseNamespace` (string, default empty → POD_NAMESPACE)
- ClusterRole grants `get/list/watch/create/update/patch/delete` on
  `coordination.k8s.io/v1.leases` (already existed for
  node-heartbeat; now also covers config-family arbitration).
- Controller deployment carries `CONFIG_LEASE_NAMESPACE` env when
  the value is set; the `CiscoDevice` controller propagates it to
  every cisco-vk pod.

### 10.2 Operator-facing knobs

| Setting | Where | Default |
|---|---|---|
| Reconcile cadence | `IOSXEConfig.spec.driftDetectInterval` | 5m |
| Drift policy | `IOSXEConfig.spec.driftPolicy` | `revert` |
| Transactional apply | `IOSXEConfig.spec.transactional` | false |
| Save startup-config | `IOSXEConfig.spec.writeStartup` | false |
| Prune on relinquish | `IOSXEConfig.spec.pruneOnRelinquish` | false |
| YANG release pin | `IOSXEConfig.spec.targetYangVersion` | (driver default) |
| Audit retention | `IOSXEConfigApplyLog.spec.maxEntries` | 50 (max 500) |
| Audit body retention | `IOSXEConfigApplyLog.spec.retainBody` | false |
| Lease namespace | `Helm: config.leaseNamespace` | POD_NAMESPACE |
| Aggregator | `Helm: aggregator.enabled` | false |

### 10.3 Observability

- Prometheus metrics (registered idempotently on the default
  registry):
  - `cisco_vk_config_reconcile_duration_seconds{device,phase}` (histogram)
  - `cisco_vk_config_apply_duration_seconds{device,family}` (histogram)
  - `cisco_vk_config_drift_detected_total{device,family}` (counter)
  - `cisco_vk_config_drift_corrected_total{device,family}` (counter)
  - `cisco_vk_config_drift_entries_truncated_total{device}` (counter)
  - `cisco_vk_config_apply_errors_total{device,family,stage}` (counter)
  - `cisco_vk_config_family_state{device,family}` (gauge: 0=InSync,
    1=Drifted, 2=ApplyError, 3=Skipped, 4=Unsupported, -1=other)

- Kubernetes Events on the IOSXEConfig: per-family
  AppliedSuccess / FamilySkipped / Drift / ReconcileFailed / Paused;
  ApplyLogUpdateFailed (warning) when status update for the
  audit-log CR fails.

- ArgoCD: drop the Lua hooks under `argocd-cm`'s
  `resource.customizations.health.config.cisco.vk_<Kind>`. Bundles
  roll up child phase to bundle-level health.

### 10.4 Security posture

- No secrets in CRs by design. Credentials live in Kubernetes
  Secrets, referenced by `CiscoDevice.spec.credentialSecretRef`.
- `IOSXEConfig.spec.secretRefs[]` resolves Kubernetes Secrets at
  resolve time, merging the snippet last so a placeholder in
  ConfigMap-borne source can never leak past resolution.
- Authorization: per-pod ServiceAccount with namespace-scoped
  reads on ConfigMaps/Secrets and (for leases) cluster-scoped lease
  RBAC. Aggregator inherits the controller's SA.
- Insecure shapes flagged by the OPA pack: privilege-15 user
  without secret (deny), enable_password (warn), SNMP rw without
  ACL (deny), declared TACACS without aaa.new_model (warn).

### 10.5 Rollback story

Three independent recovery paths:

1. **Hash short-circuit** keeps a healthy device idle until intent
   actually changes — no continuous-write drift correction loop.
2. **`pruneOnRelinquish: false` (default)** means a CR that loses a
   family from `managedFamilies` *does not* delete the device-side
   state. Operators decide when to clean up.
3. **Annotation-driven replay** — set
   `config.cisco.vk/replay-from-log: <log>:<index|hash>` on the
   IOSXEConfig and the next reconcile re-applies a known-good body
   from the apply log. Annotation auto-clears on success.

Combined: roll a bad CR change forward by un-setting the offending
fields, or roll back to a prior shape by replay annotation. Either
way, the device state is the source of truth and the controller
converges to it within one tick.

---

## 11. Architectural appraisal — strengths and watch-items

### 11.1 Strengths (in order of operational consequence)

1. **One transport interface, three protocols.** RESTCONF / NETCONF
   / gNMI all satisfy `transport.Interface`; the engine and every
   writer dispatch unchanged. Capabilities-driven feature gating
   (transactions, save-startup, subscribe) means features roll out
   per-protocol without engine flag days.

2. **Closed verb set + optional capabilities.**
   `Replace/Merge/Delete/CLI` is a small, stable wire vocabulary;
   `PruneCapable` and `SubscribeCapable` let writers and transports
   opt into richer behaviour without breaking the contract or
   forcing a flag day. The `nestedKeyedListWriter` change rolled
   per-rule diffing across six families with one helper.

3. **Audit + replay separated from writes.** The apply log is a
   side-effect; the replay path consumes it. A device-state crisis
   doesn't lose the replay path because the audit CR is independent
   of the IOSXEConfig CR's own status.

4. **Cross-validation corpus.** The 30-case
   `merge_cross_validation_test.go` pins our merge semantics to
   `terraform-provider-utils`'s, family by family. A divergence
   surfaces in CI, not in production.

5. **Resolver layered, with a clean output.** `intent.Resolver`
   composes the entire scope graph into one `ResolvedIntent` —
   pure data, suitable for hashing, logging, and replay. Engine
   never re-reads the cluster.

6. **Per-family lease arbitration is cluster-native.** No external
   coordinator, no per-CR locking discipline; just a Lease with a
   well-defined TTL. Cross-namespace arbitration is one Helm value.

### 11.2 Watch-items (actionable, ranked)

1. **`internal/aggregator` test coverage (10.9 %).** The
   `ConfigReconciler` it composes is well-covered; the goroutine
   lifecycle wiring is not. Adding an envtest-driven integration
   test for the start/stop/spec-hash-change cycle is a one-day
   follow-up. **Does not affect correctness** under the current
   per-pod default.

2. **`internal/provider` test coverage (25.7 %).** Same
   composition story — the engine and intent packages are heavily
   tested, the wiring is leaner. The
   `internal/controller` integration tests exercise enough of the
   wiring to catch regressions; pure-unit coverage of
   `ConfigReconciler.Run` would be additive.

3. **OpenConfig dialect populated for 6 families.** The remaining
   44 fall back to native paths under gNMI, which works but
   doesn't deliver the multi-vendor benefit. Per-family extension
   is data-only — no engine, transport, or writer changes.

4. **YANG tree not vendored.** `cisco-vk-yang-sync --yang-dir`
   exists; the YANG tree itself is a separate licensing decision
   the design RFC defers to Phase 5. Until vendored, the
   managed-leaf set is hand-maintained — tracked by the
   schema-vs-writers consistency tests in `family_writers_test.go`,
   so a divergence is caught at CI.

5. **BGP per-neighbor diffing.** BGP is currently the singleton
   writer pattern with opaque-leaf compare on `neighbor`. Same
   per-rule diffing helper would fit; lift is "wrap the singleton
   with an inner-keyed-list pass". One follow-up.

6. **Subscribe-driven drift drops events under overflow.** The
   16-buffer + drop-on-full policy keeps the device-side stream
   responsive but loses fine-grained signal under sustained burst.
   The next periodic Fetch-diff tick recovers; an overflow counter
   would be a clean operational addition.

7. **Apply-log retention has no time-based eviction.** FIFO trim
   at MaxEntries is the only retention policy. A "drop entries
   older than X" extension is a one-field addition; not yet shipped.

8. **Terraform provider Registry release.** Provider is
   functionally complete; signed-binary release infrastructure
   (GPG, registry metadata) is the remaining handoff.

None of these are blockers for production rollout under the per-pod
topology. Each has a scoped follow-up that does not change the
contract.

---

## 12. Recommended review path for the netascode architect

Read in this order; the parenthetical references are the artefacts
to open alongside.

1. **§4 Capability matrix vs netascode** of this document. Confirm
   primitive coverage. (`api/config/v1alpha1/`)

2. **The merge-equivalence corpus.**
   `internal/drivers/iosxe/configdriver/intent/merge_cross_validation_test.go`
   is the empirical claim that CVK's `MergeWithRules` ≡
   netascode's `terraform-provider-utils.MergeMaps` across a
   30-case fixture. If you accept this, every other authoring-side
   behaviour is downstream of one merge function. If you reject
   any case, that's the place to file the divergence.

3. **The resolver.** `internal/drivers/iosxe/configdriver/intent/resolver.go`
   `Resolver.Resolve`. Walk the precedence chain. The two
   subtleties: deferred `NamePattern` expansion runs after the
   per-device source so patterns see every declared interface, and
   `secretRefs` merge last so secret material always wins.

4. **The engine.** `internal/drivers/iosxe/configdriver/engine/engine.go`
   `Engine.Reconcile` + `reconcileFamily`. Confirm the ordering
   contract: family writes, then CLI blocks, with PruneOnRelinquish
   appending DELETEs in the same Apply call. Verify pass re-runs
   PruneDiff so a stale device-side rule that survived an apply
   surfaces as residual drift.

5. **The transports.** `transport/transport.go` (`Interface`,
   `Verb`, `Capabilities`, `SubscribeCapable`). Then any one of the
   three implementations (`restconf.go`, `netconf.go`, `gnmi.go`).
   They are concrete enough to read in one sitting; the contracts
   pinned at the wire layer are tight.

6. **One writer.** `writers/access_list_extended.go` is the
   thinnest binding to the `nestedKeyedListWriter` helper. Per-rule
   diffing across the keyed-nested-list families is the same
   binding repeated.

7. **The CRD shapes.** `api/config/v1alpha1/`. The `IOSXEConfigSpec`
   in particular — every operator-facing knob lives there.

8. **The apply log + replay.** `provider/config_reconciler.go`
   `appendApplyLog`, `applyReplayAnnotation`, plus
   `provider/replay_test.go`. The replay path is small and the
   tests pin the failure modes.

9. **The Helm chart.** `charts/cisco-virtual-kubelet/`. Read
   `values.yaml` + `deployment.yaml` to see how
   `aggregator.enabled` and `config.leaseNamespace` flip
   topology.

10. **The operator-facing tools.** `tools/cisco-vk-config-lint/`
    (drift + orphans + offline plan), `tools/cisco-vk-config-docs/`
    (per-family reference, both flat and netascode-portal layouts),
    `tools/terraform-provider-iosxeconfig/` (separate-module
    Terraform provider). These are what an operator runs at the
    review surface; the controller is what runs at the reconcile
    surface.

---

## 13. The one-line summary

A Kubernetes-native IOS-XE configuration driver that consumes
netascode-shaped YAML, reconciles it against a device over
RESTCONF / NETCONF / gNMI, and exposes a closed set of operator-
and architect-grade knobs (drift policy, prune semantics, audit log,
replay, fan-out aggregation, per-rule diffing, CLI/Jinja templates).
Every netascode primitive has a CVK equivalent; CVK adds operability
primitives (leases, audit, replay, bundles, drift policy, OPA gate)
that netascode does not natively provide. The branch is in shape for
review and rollout under the per-pod topology; the ranked watch-items
in §11.2 are the natural follow-up agenda.
