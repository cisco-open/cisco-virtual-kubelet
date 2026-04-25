# CRD v1 promotion plan

**Branch:** `pr/johalley/ciscoconfig_xe`
**Status:** plan, not implementation. Closes architectural-review watch-item **#10** (no CRD v1 promotion plan / conversion webhook). The implementation lands when v1 cuts.
**Audience:** anyone planning the v1alpha1 → v1 migration of `cisco.vk` and `config.cisco.vk` CRDs.

---

## 1. Why a plan now

Every CRD in this branch is `v1alpha1`. Kubernetes API conventions are explicit that `alpha` carries no compatibility promises and is acceptable to break, but in practice operators have already wired GitOps repositories, ArgoCD ApplicationSets, Terraform modules, and `cisco-vk-config-lint` rule packs against the current shape. A v1 cut without a documented migration path will produce one of two operator outcomes, both bad:

1. **Big-bang break.** v1 lands, every CR fails admission, every drift report is missing, every controller goes `Unknown`. Operators react by pinning v1alpha1 forever.
2. **Silent drift.** v1 ships in parallel with v1alpha1, the storage version flips quietly, and every operator who hasn't read release notes is suddenly working on the wrong shape.

The plan exists so the decision is made once, deliberately, with the migration mechanism specified in advance.

---

## 2. Scope

In scope for v1:

| Group | Kind | v1alpha1 today |
|---|---|---|
| `cisco.vk` | `CiscoDevice` | served + storage |
| `config.cisco.vk` | `IOSXEConfig` | served + storage |
| `config.cisco.vk` | `IOSXEConfigDefaults` | served + storage |
| `config.cisco.vk` | `IOSXEDeviceGroupConfig` | served + storage |
| `config.cisco.vk` | `IOSXEInterfaceGroupConfig` | served + storage |
| `config.cisco.vk` | `IOSXETemplate` | served + storage |
| `config.cisco.vk` | `IOSXEConfigBundle` | served + storage |
| `config.cisco.vk` | `IOSXEConfigApplyLog` | served + storage |

Out of scope for v1: any cross-group reorganisation. The group split between `cisco.vk` (device identity) and `config.cisco.vk` (intent) is correct and stays.

---

## 3. Shape changes anticipated for v1

These are the breaking changes the v1 cut should land — none are speculative; each maps to a specific lesson from this branch.

### 3.1 `IOSXEConfig.spec.driftPolicy`

v1alpha1 accepts the strings `revert`, `report`, `pause` directly. v1 should make this explicit:

```go
// +kubebuilder:validation:Enum=Revert;Report;Pause
type DriftPolicy string

const (
    DriftPolicyRevert DriftPolicy = "Revert"
    DriftPolicyReport DriftPolicy = "Report"
    DriftPolicyPause  DriftPolicy = "Pause"
)
```

Rationale: every other K8s API uses upper-camel-case enum values. Lowercase verbs read fine as English but break the operator's pattern-matching when they `kubectl get -o yaml` half a dozen CR types.

### 3.2 `CiscoDevice.spec.password` removal

The inline `password` field has been redundant since `CredentialSecretRef` shipped. v1alpha1 keeps it for back-compat. v1 removes it. Conversion drops the inline value; if no Secret reference is present, the conversion webhook synthesises a Secret from the inline value before dropping it (operator-visible warning event).

### 3.3 `IOSXEConfig.spec.targetYangVersion` becomes `spec.yangRelease`

v1alpha1's `targetYangVersion` is a pseudo-version string (`1791`, etc.) that maps to a Cisco IOS-XE release through `schema/yang-versions.yaml`. The field name leaks the YANG nomenclature; operators think in releases. v1 renames to `yangRelease` and accepts release-train strings (`17.9.1`, `17.12.1`); the resolver maps to YANG release numbers internally.

### 3.4 `status.familyStatus[].state` enum tightening

v1alpha1 accepts free-form strings. v1 enforces:

```
+kubebuilder:validation:Enum=InSync;Drifted;Applied;Failed;Skipped;Paused
```

### 3.5 Drop deprecated fields

Anything marked `// Deprecated:` in `api/v1alpha1/*.go` at the time of the v1 cut is dropped (none today, but this anchors the discipline).

---

## 4. Migration mechanism — conversion webhook

The official Kubernetes pattern for breaking CRD changes is a [conversion webhook](https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/#webhook-conversion). Storage version is set to v1; v1alpha1 is `served=true, storage=false`. Every read on v1alpha1 goes through the webhook to convert from the v1 storage shape; every write on v1alpha1 is converted forward to v1 before persistence.

Implementation outline:

```
internal/webhook/conversion/
├── ciscodevice/
│   ├── conversion.go          // v1alpha1 ↔ v1 spec mapping
│   └── conversion_test.go     // round-trip property tests
├── iosxeconfig/
│   ├── conversion.go
│   └── conversion_test.go
└── webhook.go                 // single Service-and-ValidatingWebhookConfiguration
```

Round-trip test contract: every conversion has a property test asserting `v1 → v1alpha1 → v1` is the identity for any field combination producible by the kubebuilder schema. This is the only way to catch lossy conversions before they ship.

The webhook is served by the controller pod (cisco-virtual-kubelet-controller), not a separate deployment; controller-runtime supports this directly via `mgr.GetWebhookServer()`. Cert-manager (or chart-managed self-signed certs) provides the TLS pair the API server requires.

### 4.1 Helm wiring

Chart additions:

```yaml
# values.yaml
webhook:
  enabled: true
  certificate:
    # one of "cert-manager", "self-signed", or "external"
    source: cert-manager
```

Two new chart templates:

- `templates/webhook-service.yaml` — ClusterIP Service in front of the controller pod's :9443.
- `templates/webhook-conversion.yaml` — `CustomResourceDefinition.spec.conversion.strategy = Webhook` with `clientConfig.service` pointing at the above.

If `source: cert-manager`, generate a `Certificate`. If `source: self-signed`, the controller generates a CA at boot and writes its cert into the CRDs via dynamic update (the controller already has CRD update perms via the install-CRD bootstrap).

---

## 5. Phasing — three releases, not one

A clean cutover takes three releases. The motivation is that operators upgrade their CVK install on a different cadence to upgrading their CRs.

### Release N — last v1alpha1-only release (today's HEAD).

No webhook. No v1. Storage is v1alpha1. Operators have time to read the changelog and pin a CVK version.

### Release N+1 — v1 introduced as `served=true, storage=true`; v1alpha1 stays served and is converted.

- Webhook ships, conversion implemented for every kind.
- v1 is the storage version. Etcd holds v1; v1alpha1 reads/writes are converted on the wire.
- `kubectl get crd iosxeconfigs.config.cisco.vk -o yaml` now shows both versions, with v1 marked `storage: true`.
- `controller-gen rbac` regenerates with v1 paths everywhere; v1alpha1 controller code becomes a thin shim that calls the v1 reconciler with a converted spec.
- `cisco-vk-config-lint`, `cisco-vk-config-docs`, and the Terraform provider are dual-version: each accepts both shapes on read, emits v1 on write.
- Release notes ship a migration table: every renamed field, every dropped field, every enum case change.
- An optional `kubectl-cvk migrate` plugin re-applies v1alpha1 CRs as v1 (idempotent, safe to re-run).

This is the longest-lived release. Field experience drives any conversion-bug fixes. Suggested support window: two minor versions or six months, whichever is longer.

### Release N+2 — v1alpha1 is removed.

- v1alpha1 dropped from `served` list.
- Conversion webhook stays for one more release as a safety net (returning a useful error when invoked) before being removed in N+3.
- All in-tree code refers to v1 only.

---

## 6. Compatibility table — operator's view

This is the table that lands in the release notes for N+1.

| Operator action | Pre-N+1 (v1alpha1 only) | N+1 (both served, v1 storage) | N+2 (v1 only) |
|---|---|---|---|
| `kubectl apply -f my-iosxeconfig.yaml` (v1alpha1) | ✅ stored as v1alpha1 | ✅ converted to v1 in etcd, returned as v1alpha1 if requested | ❌ rejected; see migration plugin |
| `kubectl get iosxeconfig` (no version) | returns v1alpha1 | returns v1 (the storage version) | returns v1 |
| `kubectl get iosxeconfigs.v1alpha1.config.cisco.vk` | returns v1alpha1 | returns v1alpha1 (converted) | ❌ NotFound |
| ArgoCD sync of v1alpha1 manifest | ✅ | ✅ | ❌ |
| Terraform provider `iosxeconfig_config` resource | reads/writes v1alpha1 | dual-version | v1 only |
| `cisco-vk-config-lint` against v1alpha1 CRs in cluster | ✅ | ✅ | ❌ |

---

## 7. Acceptance criteria for the implementation PR

Defined now so the implementation PR can be evaluated against a frozen target.

1. **Round-trip property test passes** for every (kind, field-permutation) pair generated by `controller-gen` schema. No silent field drops.
2. **`kubectl convert`** produces clean output between versions for every CR shape in `examples/`.
3. **Helm chart upgrade from N → N+1 is non-destructive** in a kind smoke. Tested via the existing `.github/workflows/smoke.yml`, extended to install N first, apply v1alpha1 CRs, upgrade to N+1, assert all CRs become v1 in etcd, assert `kubectl get -o yaml` of the original v1alpha1 path still works.
4. **Conversion webhook latency** under 50ms p99 in a one-CR-per-second load test. Above that, the API-server-side timeout becomes a real risk.
5. **`cisco-vk-config-lint`** has been re-run against the v1alpha1 reference corpus AND a v1-translated copy; same drift verdict for both.

---

## 8. Risks called out

- **Storage version flip is a one-way door.** Once etcd is v1, downgrading to a controller that doesn't know v1 corrupts the cluster's view of those CRs. The Helm chart must refuse to install a pre-N+1 controller against a cluster where v1 is the storage version.
- **Conversion webhook is on the API-server hot path** for every CR read. A flaky webhook or expired cert turns into "kubectl get appears hung". Cert-manager + `failurePolicy=Fail` is the production posture; `Ignore` for dev.
- **The webhook deployment must not have a circular dependency on its own CRDs.** It's installed separately and watches Pods/Services, not its own CRs, by design.

---

## 9. What this plan does NOT cover

- v1 → v2 strategy (no plans). Once v1 is stable, breaking changes pause until justified by user demand.
- Server-side apply field-manager renames. Operator-visible but mechanical; addressed in the implementation PR notes.
- API priority/fairness flow-control rules. Default queue is fine for the expected RPS.

---

## 10. Schedule pointer

This RFC is the green-light artefact for the v1 cut. Implementation is scoped at ~2 engineer-weeks (webhook scaffold + conversions + chart wiring + dual-version tooling shims + release-note material). Parallel work — `cisco-vk-config-lint` and the Terraform provider's dual-version support — adds about ~1 engineer-week each.

The order of operations:

1. Land this RFC (this PR).
2. Cut release N (last v1alpha1-only).
3. Implement conversion-webhook PR against `main`.
4. Cut release N+1.
5. Soak six months minimum.
6. Cut release N+2 (v1alpha1 removed).
