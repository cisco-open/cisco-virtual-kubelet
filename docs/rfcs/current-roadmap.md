# Current Roadmap (post PR #111)

> **Status doc, not a design RFC.** This file is the authoritative
> punch list of next-quarter work. It is meant to be re-read and
> re-cut every release. Item state ("shipped" / "in flight" /
> "not started") is verifiable against the tree at the time of the
> last update — *do not trust this document if the heading
> revision is older than the current branch*.
>
> **Last updated:** post-merge `pr/johalley/nacxe @ fffc4cf`
> (PR cisco-open/cisco-virtual-kubelet#111).
>
> Inputs: survey of `docs/rfcs/`, code state at the listed commit,
> and two `/codex:adversarial-review` passes (one against the
> initial proposal, one against this document — see §10).

---

## 0. Status snapshot

Items frequently miscalled "future work" that have already shipped
on `pr/johalley/nacxe`. **Do not put these on a roadmap.**

| Item | Evidence | Notes |
|---|---|---|
| Subscribe-overflow metric | `internal/drivers/iosxe/configdriver/transport/metrics.go:46`, `gnmi.go:213` | `cisco_vk_config_subscribe_events_dropped_total` |
| OTel reconciler spans | `internal/provider/config_reconciler_controller.go:279` | per-tick span with device + CR-namespace + CR-name attribution. **Family-level attributes from `result.FamilyStatuses` are NOT attached** — that is open work, see §2.5 |
| Parser fuzz targets | `internal/drivers/iosxe/configdriver/transport/parsers_fuzz_test.go` | parseGNMIPath, splitYAMLDocs, parseHello, parseRPCReply, splitReplayAnnotation |
| Sequence-keyed diffing | `iosxe-config-driver-review.md` ledger | Phase-4 follow-up, marked done |
| `kubectl ciscovk exec` | `tools/kubectl-ciscovk/main.go:53` | Plugin source landed, **but not yet packaged**: `Dockerfile` builds only `./cmd/cisco-vk`, and `Makefile` has no `kubectl-ciscovk` target (cf. `cisco-vk-config-lint`, `-docs`, `-yang-sync`). See §2.4 — "package and ship the plugin" is a real work item, not just "expand subcommands". |
| Aggregator topology | `--enable-config-aggregator`, `internal/aggregator/` | Off by default in chart (`values.yaml:47`); opt-in |

The previous roadmap (rev. 1) listed several of these as Q1 work
items because the underlying RFCs hadn't been updated to reflect
shipped status. **Future updates of this file must verify the
"shipped" column against `git ls-tree HEAD` before re-listing
deferred items.**

---

## 1. Tier-1 — Release-readiness on `v1alpha1`

Codex's adversarial pass on the prior roadmap identified the real
external-release blockers. These all live on `v1alpha1`; finish
them before the v1 promotion gets a calendar slot.

### 1.1 CRD inventory completeness in the v1 plan
- **What:** [`crd-v1-promotion-plan.md`](crd-v1-promotion-plan.md)
  scope table omits `IOSXEDiagnostic`, but the chart ships it
  ([`config.cisco.vk_iosxediagnostics.yaml`](../../charts/cisco-virtual-kubelet/crds/config.cisco.vk_iosxediagnostics.yaml)).
- **Action:** add `IOSXEDiagnostic` to the v1 plan's conversion-webhook
  scope; reconcile the field-rename table for it.
- **Severity:** BLOCKER for v1 promotion. **Effort:** half day.

### 1.2 `CiscoDevice` schema split for apphosting/config transport
- **What:** [`api/v1alpha1/types.go:166`](../../api/v1alpha1/types.go)
  declares `spec.transport` as configdriver-only; apphosting
  hardwires RESTCONF in
  [`internal/drivers/iosxe/driver.go:115`](../../internal/drivers/iosxe/driver.go).
  Promoting `CiscoDevice` to v1 before resolving this freezes the
  wrong abstraction.
- **Action:** decide the v1 schema for transport selection
  *before* §3 (transport consolidation) lands. Three candidates,
  pick one:
  1. Single `spec.transport` honoured by both drivers — apphosting
     gains NETCONF/gNMI bindings (depends on §3 Phase A).
  2. `spec.transport.config` and `spec.transport.apphosting` as
     siblings — preserves current divergence; explicit but ugly.
  3. Single `spec.transport` with a per-driver
     `unsupported`-style fail-loud when apphosting is asked to
     operate over an unsupported transport.
- **Severity:** BLOCKER for v1 promotion. **Effort:** decision
  meeting + 1 week implementation depending on choice.

### 1.3 Terraform provider full `IOSXEConfigSpec` coverage
- **What:** the provider emits a subset of the spec
  ([`tools/terraform-provider-iosxeconfig/internal/provider/resource_iosxeconfig.go:308`](../../tools/terraform-provider-iosxeconfig/internal/provider/resource_iosxeconfig.go)).
  Missing: `transactional`, `driftDetectInterval`,
  `confirmTimeoutSeconds`, `atomicReplace`, `targetYangVersion`,
  per-source secret refs.
- **Action:** expand `resource_iosxeconfig.go` to the full schema;
  add per-field unit tests; drop the README "release-scaffold
  quality" disclaimer
  ([`tools/terraform-provider-iosxeconfig/README.md:9`](../../tools/terraform-provider-iosxeconfig/README.md)).
- **Severity:** HIGH for external Terraform-first adopters.
  **Effort:** 1 week.

### 1.4 Production chart profile
- **What:** PDB
  ([`values.yaml:93`](../../charts/cisco-virtual-kubelet/values.yaml)),
  NetworkPolicy
  ([`values.yaml:107`](../../charts/cisco-virtual-kubelet/values.yaml)),
  and ServiceMonitor
  ([`values.yaml:122`](../../charts/cisco-virtual-kubelet/values.yaml))
  all default `enabled: false`. **Cross-namespace lease arbitration**
  is also off by default: `config.leaseNamespace` is empty
  ([`values.yaml:59`](../../charts/cisco-virtual-kubelet/values.yaml)),
  the controller only sets `CONFIG_LEASE_NAMESPACE` when that value
  is non-empty
  ([`templates/deployment.yaml:47`](../../charts/cisco-virtual-kubelet/templates/deployment.yaml)),
  and as a result two `IOSXEConfig` CRs in different namespaces
  pointing at the same device do **not** arbitrate against each
  other — last write wins. External operators get no
  shipped-as-correct posture without adding their own overlays.
- **Action:** ship `values-production.yaml` with PDB / NetworkPolicy
  / ServiceMonitor on, a non-empty `config.leaseNamespace`,
  HPA examples, recommended limits, and a "production checklist"
  in `docs/getting-started.md`. Keep the bare `values.yaml` as
  development-default for local kind. Document the multi-tenant
  contract: production deployments **must** use a shared lease
  namespace, or the operator must accept last-write-wins on
  cross-namespace device contention.
- **Severity:** MEDIUM. **Effort:** 2 days.

### 1.5 Operator-persona RBAC (see §5)
Cross-cuts release-readiness and the operator-surfaces theme.
Treated as Tier-1 because device-ops and diagnostics both depend
on it.

### 1.6 Inline-password → Secret migration
- **What:** [`api/v1alpha1/types.go:103`](../../api/v1alpha1/types.go)
  still accepts `spec.password` inline. The v1 plan
  ([`crd-v1-promotion-plan.md`](crd-v1-promotion-plan.md))
  proposes synthesising a Secret during conversion — a security
  concern, not just a schema change.
- **Action:** deprecate `spec.password` *now* (warning event when
  set on `v1alpha1`); convert to a synthesised Secret on v1
  conversion. Document the migration recipe.
- **Severity:** HIGH (CVE-class if not handled at v1 cut).
  **Effort:** 1 week.

### 1.7 Backup / restore documentation
- **What:** ApplyLog body retention is opt-in
  ([`api/config/v1alpha1/iosxeconfigapplylog_types.go:136`](../../api/config/v1alpha1/iosxeconfigapplylog_types.go),
  `spec.retainBody=false` by default), and the operator-CLI guide
  falls back to "the GitOps source-of-truth" when no log body is
  available
  ([`operator-cli-guide.md:545`](operator-cli-guide.md)). There is
  no documented recovery procedure for losing the cluster (CRs +
  status + ApplyLog state + generated ConfigMaps).
- **Action:** add `docs/operations/backup-restore.md` covering:
  CRD spec/status export (Velero-friendly shape), ApplyLog rotation
  policy + retention guidance, generated-ConfigMap regeneration,
  device-config divergence detection during restore (the device
  may have changed since the last reconcile), and downgrade
  recovery (if a restore lands you on an older controller image).
  Wire a `cisco-vk` subcommand or a documented `kubectl get -o yaml`
  recipe.
- **Severity:** MEDIUM (HIGH for any compliance environment).
  **Effort:** 1 week including a restore drill on a real lab
  cluster.

### 1.8 Multi-release YANG strategy
- **What:** schema catalog declares only `1791`
  ([`internal/drivers/iosxe/configdriver/schema/yang-versions.yaml`](../../internal/drivers/iosxe/configdriver/schema/yang-versions.yaml)).
  Adopters running 17.12 / 17.15 / 17.18 see "experimental" warnings
  or unsupported-version failures.
- **Action:** add 17.12 / 17.15 / 17.18 release directories to
  `schema/yang/`; CI matrix for each; document the
  "supported / experimental / deprecated" lifecycle.
- **Severity:** HIGH for any adopter not on 17.9. **Effort:**
  1–2 weeks (depends on writer drift between releases).

---

## 2. Tier-2 — Operator UX (verified-not-shipped portion)

These looked like quick wins in the prior roadmap; in practice they
need preliminary status-field or CRD-marker work first.

### 2.1 Tier-1 printer columns
Adding `TRANSPORT`, `LAST-APPLY`, `DRIFT-COUNT` columns
([`operator-cli-guide.md` §13.1](operator-cli-guide.md))
needs **new status fields** on `IOSXEConfigStatus`
([`api/config/v1alpha1/iosxeconfig_types.go:268`](../../api/config/v1alpha1/iosxeconfig_types.go))
— `activeTransport`, `driftCount`, `lastApplyTime`. Land status
fields first, then printer columns.

**Effort:** 3 days.

### 2.2 Field selectors via `selectableFields`
The operator-CLI guide already notes
([`operator-cli-guide.md:673`](operator-cli-guide.md))
that `kubectl get ... --field-selector status.phase=...` does not
work today. Kubernetes 1.30 supports declared `selectableFields` on
CRDs; add markers to the affected types and regen.

**Effort:** 2 days.

### 2.3 Condition normalisation
Standard `Ready` condition across all eight config CRDs; lease-holder
identity in conflict conditions; drift-cause classification on events.

**Effort:** 1 week.

### 2.4 `kubectl ciscovk` plugin: package + expand
**Two work items, in this order:**
1. **Package and ship the existing plugin** — add a Makefile
   target (sibling of `config-lint`, `config-docs`, `yang-sync`),
   a Dockerfile build stage, a `krew` manifest, and a release
   asset in `.github/workflows/release.yml`. Without this, only
   developers who `go run ./tools/kubectl-ciscovk` can use the
   `exec` subcommand that #111 ships.
2. **Add `diff`, `explain`, `replay`, `health` subcommands.**
   Each is a 1–2 day implementation; do the cheapest first
   (`diff` → `explain` → `health` → `replay`).

**Effort:** 3 days packaging + 2 weeks for the four subcommands.

### 2.5 OTel reconciler span: family-level attribution
The shipped span at
[`config_reconciler_controller.go:279`](../../internal/provider/config_reconciler_controller.go)
attributes only at the CR level (device + namespace + name). The
reconciler already produces `result.FamilyStatuses`
([`config_reconciler_controller.go:452`](../../internal/provider/config_reconciler_controller.go))
with per-family success / fail / drift information that is *not*
attached to the span. Two options:
- Attach per-family attributes to the parent span (one
  `cisco.vk.family.<name>.phase` attribute per managed family).
- Emit a child span per family. Cleaner for trace UIs, more
  spans / writes.

**Effort:** 1–2 days. Do this *before* the family-level printer
columns in §2.1 so dashboards have something to filter on.

---

## 3. Theme: Transport protocol consolidation

> **The strategic risk:** apphosting and configdriver have diverged
> structurally. Configdriver has a uniform `transport.Interface`
> with three bindings (RESTCONF / NETCONF / gNMI) plus `ssh_cli`;
> apphosting hardwires RESTCONF. Each release widens the gap.
> Until they share a transport surface, the v1 schema, the RBAC
> story, and the operator UX are all forced into compromise
> shapes.

### 3.1 Current state

- **Configdriver `transport.Interface`**
  ([`internal/drivers/iosxe/configdriver/transport/transport.go`](../../internal/drivers/iosxe/configdriver/transport/transport.go))
  — `Capabilities`, `Mutate`, `Get`/`Set`, `Lock`/`Unlock`,
  `Commit`/`Discard`, optional `SubscribeCapable`,
  `DiagnosticExecer`, `ConfirmedCommitter`.
- **Apphosting**
  ([`internal/drivers/iosxe/driver.go:115`](../../internal/drivers/iosxe/driver.go),
  [`device.go:114`](../../internal/drivers/iosxe/device.go),
  `client_test.go`) talks to `/restconf/data/Cisco-IOS-XE-app-hosting-oper`
  and `/restconf/operations/Cisco-IOS-XE-rpc:app-hosting` only.
- **Capability gap:** the configdriver interface has no
  `LifecycleVerb` family — no
  `Install` / `Activate` / `Start` / `Stop` / `Uninstall`
  semantics. Apphosting's RPC vocabulary doesn't fit cleanly into
  `Mutate` / `Get` / `Set`.

### 3.2 Phased plan

**Phase A — `LifecycleCapable` optional interface, typed (4 weeks).**
- Add a sibling-of-`SubscribeCapable` interface to
  `transport.go`. Variadic `args ...any` is rejected — apphosting
  RPCs have operation-specific payloads
  ([`internal/drivers/iosxe/client.go:250`](../../internal/drivers/iosxe/client.go)
  for `install`, with `package`, source URL, fallback retry/copy
  paths). Use **typed request/response structs per operation:**
  ```go
  type LifecycleCapable interface {
      InstallApp(ctx, InstallAppRequest) (InstallAppResponse, error)
      ActivateApp(ctx, AppRef) error
      StartApp(ctx, AppRef) error
      StopApp(ctx, AppRef) error
      DeactivateApp(ctx, AppRef) error
      UninstallApp(ctx, AppRef) error
      // optional: Status query, separate from Pod-IP discovery.
      AppStatus(ctx, AppRef) (AppStatus, error)
  }
  type InstallAppRequest struct {
      AppID, PackageURL, ChecksumSHA256 string
      CopyTimeout, InstallTimeout       time.Duration
      FallbackPolicy                    FallbackPolicy // delete-then-reinstall, etc.
  }
  ```
- **Whole-lifecycle device lock.** `CreateAppHostingApp`
  ([`internal/drivers/iosxe/client.go:64`](../../internal/drivers/iosxe/client.go))
  performs POST → install → wait → conditional delete/repost/copy
  /reinstall as one logical sequence; the existing RESTCONF
  `SessionLock`
  ([`internal/drivers/iosxe/configdriver/transport/restconf.go:409`](../../internal/drivers/iosxe/configdriver/transport/restconf.go))
  acquires per HTTP request, not over the multi-RPC arc. Phase A
  must add a `transport.SessionLease` (or similar) that brackets
  the whole `InstallApp` flow against concurrent config writers
  on the same device. Without this, an `IOSXEConfig` apply
  reordered against an apphosting install can corrupt either
  side.
- Implement `LifecycleCapable` on RESTCONF (port the existing RPC
  paths from `internal/drivers/iosxe/client.go`).
- Refactor apphosting Pod paths to call through
  `transport.Interface` + `LifecycleCapable` instead of holding
  their own RESTCONF client. Apphosting still works because RESTCONF
  still supports the verbs.
- **Deliverable:** apphosting compiles against the configdriver's
  transport package; behaviour unchanged; whole-lifecycle locking
  in place.

**Phase B — NETCONF and gNMI bindings (6 weeks, gated per platform).**
- NETCONF: implement `LifecycleCapable` for the `<cli-config>` and
  `<cli-exec>` RPCs (Cisco-IA already exposes them).
- gNMI: implement `LifecycleCapable` for whatever gNMI subset
  Cisco actually supports — likely none in 17.18; gate by
  `Capabilities.SupportsLifecycle`.
- CI matrix: lab-CI runs Phase B against Cat9k 17.18.x and
  Cat8kv; report unsupported clearly when the device returns
  `unknown-element`.
- **Deliverable:** apphosting can run over NETCONF on Cat9k; gNMI
  remains opt-in / experimental.

**Phase C — Per-family transport overrides (4 weeks).**
- The hybrid mode from
  [`transport-architecture.md` §12](transport-architecture.md):
  apphosting on RESTCONF, banner on RESTCONF, interfaces on gNMI,
  ACLs on NETCONF — declarable per `IOSXEConfig`.
- Schema: `spec.transportOverrides` (map family → transport name).
- Validation: rejects override that exceeds the chosen transport's
  `Capabilities`.

**Phase D — Convergence boundaries (NOT a writer migration).**
The earlier rev. 1 of this plan called for folding apphosting
into `configdriver/writers/`. **Reject that.** The
`SectionWriter` interface is config-state-shaped
(`Fetch` / `Diff` / `Apply` over declared families,
[`internal/drivers/iosxe/configdriver/writers/writer.go:35`](../../internal/drivers/iosxe/configdriver/writers/writer.go)).
Apphosting reconciles **Pod orchestration** — it carries Pod
metadata, image-pull secrets, package-pull timeouts, two-phase
start, and Pod-IP discovery
([`internal/drivers/iosxe/types.go:90`](../../internal/drivers/iosxe/types.go)).
Forcing apphosting through `SectionWriter` would erase those
semantics or pollute the writer interface to accommodate them.

What converges in Phase D:
- **Transport** (Phase A) — apphosting calls through
  `transport.Interface` + `LifecycleCapable`; one connection pool,
  one capability surface, one lock model.
- **Observability** — apphosting metrics/spans use the same
  helper packages (`internal/drivers/iosxe/configdriver/observability/`).
- **Capability negotiation** — both surfaces advertise + probe
  via the same `Capabilities` struct.

What stays separate:
- **Reconciler shape.** Apphosting stays as a Pod-lifecycle
  reconciler in `internal/drivers/iosxe/`; configdriver stays
  as a config-state reconciler. Two reconcilers, one transport
  package.
- **Writer interface.** No apphosting `SectionWriter`. The
  `LifecycleCapable` verbs *are* the apphosting writer abstraction.

**Deliverable:** transport divergence gone, reconciler
divergence intentionally retained and documented as the
"two reconciler shapes, one transport surface" pattern.
**Effort:** 2 weeks (mostly cleanup + docs after Phase A–C).

### 3.3 Design decisions to settle before Phase A

- **Where does the lifecycle capability surface — `transport.Interface`
  or a sibling driver capability?** `transport.Interface` is the
  right home if we want the same RBAC + audit story; a sibling
  if we want apphosting to retain a separate path.
- **Does `LifecycleOp` carry typed arguments (struct per op) or
  a `map[string]any` payload?** Strongly typed wins for safety;
  payload wins for speed-of-iteration.
- **Pod-IP discovery (`internal/drivers/iosxe/ip_discovery.go`)
  currently reads `app-hosting-oper`. Does it move into
  `LifecycleCapable` as a `Status` query, or stay separate?**
  Recommendation: separate — it's a read against a single oper
  path, not a mutation.

### 3.4 RBAC implications

- The configdriver controller's ClusterRole (`charts/.../templates/role.yaml`)
  already includes `IOSXEConfig`-related verbs. Lifecycle ops
  introduce a new `pods/lifecycle` semantic — recommend a
  separate ClusterRole `cisco-virtual-kubelet-apphosting` with
  pod-create/update/delete; keep configdriver and apphosting
  RBAC tracked separately even after transport-package merge.

### 3.5 Risks

- **Cisco gNMI lifecycle support is essentially absent** on shipped
  IOS-XE (Phase B gNMI piece may be permanently gated as
  experimental). Don't block Phase B on gNMI.
- **Pod-IP discovery on NETCONF** requires `<cli-exec>` against
  `show app-hosting list` and parsing the textual output. Brittle.
  Consider keeping `app-hosting-oper`-via-RESTCONF as the discovery
  path even after Phase A.
- **Cross-cut with §1.2**: the `CiscoDevice` v1 schema decision
  *must* settle the transport selection model before Phase A
  ships, otherwise the API freezes the wrong shape.

### 3.6 Cross-theme dependencies

- §1.2 (`CiscoDevice` schema split) — must precede.
- §4 (gNOI) — Phase B and gNOI both touch gRPC binding work, but
  ports differ. gNMI defaults to **50052** on IOS-XE 17.18 gnxi
  builds
  ([`internal/drivers/iosxe/configdriver/transport/factory.go:90`](../../internal/drivers/iosxe/configdriver/transport/factory.go);
  older 6030 / 57400 builds need explicit port override per
  [`transport-architecture.md:84`](transport-architecture.md)).
  gNOI does *not* automatically share the same port — design
  must capability-probe and configure each endpoint
  independently. Share the underlying gRPC client library /
  TLS material; do not assume shared dial endpoint.
- §5 (operator personas) — apphosting RBAC scope decides whether
  the apphosting persona is separate from the config persona.

---

## 4. Theme: gNOI integration

> Goal: device + cert lifecycle via gNOI. Today the project has no
> way to rotate device-side certs, install OS images, or trigger
> a controlled reboot through Kubernetes. gNOI is the de-facto
> answer in the OpenConfig ecosystem.

### 4.1 Scope

| gNOI service | Operations | Priority |
|---|---|---|
| `Cert` | `Install`, `Rotate`, `RevokeCertificates`, `GenerateCSR`, `GetCertificates` | **P0** — device cert rotation is the most-asked operator capability |
| `System` | `Reboot`, `Ping`, `Traceroute`, `Time` | **P1** — `Reboot` overlaps device-ops RFC; consolidate (see §4.4) |
| `File` | `Get`, `Put`, `Stat`, `Remove` | P1 — needed for OS image staging |
| `OS` | `Install`, `Activate`, `Verify` | P2 — image management; depends on `File` |
| `FactoryReset` | `Start` | P2 — destructive; needs approval subresource |

### 4.2 Cisco IOS-XE gNOI coverage (verify before scheduling)

- 17.12+: `Cert`, `System.Reboot`, `System.Ping`, `System.Time`,
  `File`, `OS`.
- 17.15+: adds `FactoryReset`.
- 17.9: `Cert` only.
  *Verify these against actual lab device responses before locking
  the schedule — Cisco's release notes do not always match
  shipped binaries.*

### 4.3 Phased plan

**Phase A.0 — Lab capability probe (1 week, must precede A.1).**
The CRD work below is gated on knowing which gNOI services the
target IOS-XE actually exposes. Cisco's release notes do not
match shipped binaries reliably (see §3.6 about gNMI port
defaults). Run a probe against the live lab Cat9k 17.18 +
Cat8kv 17.18: dial gNOI on the configured port, call
`Capabilities`, record which services / RPCs respond, and
emit a JSON capability matrix to `docs/operations/gnoi-coverage.md`.
Schedule §4 Phase A.1 only after that matrix lands.

**Phase A.1 — `Cert` services (3 weeks).**
Imperative ops belong in their own API group (the existing
`config.cisco.vk` is documented as declarative,
[`api/config/v1alpha1/groupversion_info.go:22`](../../api/config/v1alpha1/groupversion_info.go)).
**New API group: `ops.cisco.vk/v1alpha1`** (covers all gNOI
CRDs + the device-operations RFC's `IOSXEMaintenance` /
`IOSXEDeviceOp` when those land).

Split desired-state from one-shot operations. Device-ops models
operations as one-shot CRs only
([`device-operations-rfc.md:101`](device-operations-rfc.md));
preserve that:

- **`GNOICertPolicy` (`ops.cisco.vk/v1alpha1`)** — *desired
  state*. Says "this device should always have a cert in this
  Secret, rotated quarterly":
  ```yaml
  spec:
    deviceRef: { name: cat9k-1 }
    certRef: { secretName: cat9k-1-tls }
    rotation:
      cronExpression: "0 0 1 */3 *"
      gracePeriodSeconds: 3600
    issuer:
      certManagerRef: { name: cisco-vk-issuer }   # see §4.5
  status:
    nextRotationDue: ...
    lastRotation: ...
    conditions: [...]
  ```
  The controller emits one-shot `GNOICertOp` CRs at rotation time.

- **`GNOICertOp` (`ops.cisco.vk/v1alpha1`)** — *one-shot request*:
  ```yaml
  spec:
    deviceRef: { name: cat9k-1 }
    operation: Rotate    # | Install | Revoke | GenerateCSR
    certRef: { secretName: cat9k-1-tls }
  status:
    phase: Pending | InProgress | Completed | Failed
    completedAt: ...
    csrPEM: ...          # for GenerateCSR only
    conditions: [...]
  ```
  Owner ref to `GNOICertPolicy` (when emitted by policy) or
  `CiscoDevice` (when manually created). Finalizer-driven cleanup.

Add `gnoiTransport` sibling to `gnmiTransport` in
`internal/drivers/iosxe/configdriver/transport/`. Capability-probe
the gNOI port independently of gNMI (see §3.6).
**Deliverable:** end-to-end cert rotation against a real Cat9k,
validated by certificate fingerprint check post-rotation.

**Phase B — `System` services + device-ops convergence (4 weeks).**
- See §4.4. Resolve duplication with device-ops RFC.
- Implement `System.Reboot`, `System.Ping`, `System.Time` on the
  unified surface.
- `IOSXEDeviceOp` / `IOSXEMaintenance` from
  [`device-operations-rfc.md`](device-operations-rfc.md) absorb gNOI
  System under the hood; CRD names stay vendor-aware (`IOSXE…`)
  for clarity.

**Phase C — `File` + `OS` services (5 weeks).**
- New CRD `GNOIImageOp`: install / activate / verify.
- Owner ref + finalizer; pre-flight free-space check via `File.Stat`.
- Long-running operation pattern (similar to confirmed-commit) —
  status updates as the device installs.
- **Deliverable:** rolling image upgrade across a fleet, kicked off
  by a `GNOIImageOpBundle` (label-selector fanout, similar to
  `IOSXEConfigBundle`).

**Phase D — `FactoryReset.Start` (2 weeks, gated).**
- Approval subresource pattern from
  [`device-operations-rfc.md`](device-operations-rfc.md).
- Two-eyes RBAC: requires `cisco-vk-operator-destructive` ClusterRole
  (a separate role from normal operator).

### 4.4 Convergence with device-operations RFC (narrow scope)

The device-ops RFC introduces `IOSXEMaintenance` (clear-class,
counter resets) and `IOSXEDeviceOp` (reload, erase). gNOI
`System.Reboot` and `FactoryReset.Start` overlap directly with
those — but **only those**. The convergence story is narrower
than rev. 1 of this plan claimed:

| gNOI | Device-ops RFC equivalent | Convergence? |
|---|---|---|
| `System.Reboot` | `IOSXEDeviceOp.spec.operation=Reload` | **Yes** — `IOSXEDeviceOp` becomes the operator-facing CRD; gNOI is the transport underneath when supported, NETCONF `<reload>` RPC fallback otherwise |
| `FactoryReset.Start` | `IOSXEDeviceOp.spec.operation=Erase` | **Yes** — same pattern, with the approval-subresource gate |
| `System.Ping` / `System.Traceroute` / `System.Time` | `IOSXEMaintenance` (read/clear class) | **Partial** — Ping/Traceroute fit `IOSXEMaintenance.spec.operation=Probe`; `Time` belongs separately (it's a config concern, not maintenance) |
| `Cert.*` | (no device-ops equivalent) | **No** — Cert lives entirely under `GNOICertPolicy` / `GNOICertOp` |
| `File.*` | (no device-ops equivalent) | **No** — File is image-staging primitives; supports `OS.*`, not exposed directly to operators |
| `OS.Install` / `OS.Activate` / `OS.Verify` | (no device-ops equivalent — image lifecycle is new) | **No** — lives under `GNOIImageOp` |

**Recommendation:** keep `IOSXEMaintenance` and `IOSXEDeviceOp`
as the operator-facing CRDs *for the operations they cover*
(reload, erase, ping, traceroute, clear). gNOI is the transport
underneath. **Do not** force `Cert`, `File`, `OS` through that
naming: those genuinely warrant separate CRDs in `ops.cisco.vk`
because the semantics, RBAC tier, and lifecycle are different.

### 4.5 Design decisions and the cert-manager flow

**A. gNOI as a `transport.Interface` sibling, or its own package?**
Own package (`internal/drivers/iosxe/configdriver/gnoi/`) under
a thin `GNOICapable` interface on `transport.Interface`. gNOI's
verb set (Cert, System, File, OS) doesn't map to the
config-write-shaped `transport.Interface`.

**B. CRD location.** New API group `ops.cisco.vk/v1alpha1` —
not `config.cisco.vk` — because the existing config group is
documented as declarative
([`api/config/v1alpha1/groupversion_info.go:22`](../../api/config/v1alpha1/groupversion_info.go))
and gNOI is imperative.

**C. Cert storage shape.** `kubernetes.io/tls` Secret (standard
keys: `tls.crt`, `tls.key`, `ca.crt`) for cert-manager interop.

**D. Cert-manager integration flow** (concrete, not just
"interop"):

```
   ┌─────────────────────────────────────────────────────────┐
   │ 1. Operator creates Issuer / ClusterIssuer (cert-manager)│
   │    e.g. internal CA, Vault, ACME                          │
   └─────────────────────────────────────────────────────────┘
                         │
                         ▼
   ┌─────────────────────────────────────────────────────────┐
   │ 2. Operator creates GNOICertPolicy referencing issuer    │
   │    spec.issuer.certManagerRef = { name: cisco-vk-issuer }│
   └─────────────────────────────────────────────────────────┘
                         │
        controller emits Certificate                           │
                         ▼
   ┌─────────────────────────────────────────────────────────┐
   │ 3. cert-manager generates Secret (kubernetes.io/tls)     │
   │    owner = Certificate; renewal triggers Secret update   │
   └─────────────────────────────────────────────────────────┘
                         │
       Secret update event                                     │
                         ▼
   ┌─────────────────────────────────────────────────────────┐
   │ 4. Cisco-VK controller emits GNOICertOp(Rotate)          │
   │    pulls bytes from Secret, calls gNOI Cert.Rotate       │
   │    on the device, validates fingerprint post-rotation    │
   └─────────────────────────────────────────────────────────┘
```

Required deliverables for the flow:
- Schema field `spec.issuer.certManagerRef` on `GNOICertPolicy`.
- A controller watch on Secrets where
  `metadata.ownerReferences` includes a cert-manager `Certificate`
  *and* the Secret name appears in any `GNOICertPolicy.spec.certRef`.
- Document the chart values for the issuer
  (`charts/cisco-virtual-kubelet/values.yaml.cert-manager.issuerName`),
  the renewal cadence interaction with `spec.rotation.cronExpression`
  (cert-manager's renewal wins; the policy's cron is a maximum
  age guarantee), and the recovery path when the Secret is
  rotated but the device push fails.

The existing cert-manager mention in the v1 plan
([`crd-v1-promotion-plan.md:101`](crd-v1-promotion-plan.md))
covers conversion-webhook TLS only, not device certs. This is
new work.

### 4.6 RBAC implications

Three new ClusterRoles:
- `cisco-vk-cert-operator` — `gnoicertops` get/list/watch/create/update/patch.
- `cisco-vk-image-operator` — `gnoiimageops` + `gnoiimageopbundles`.
- `cisco-vk-operator-destructive` — `factoryreset` verbs; bound only
  to a specific named SA via `RoleBinding`, never `ClusterRoleBinding`.

### 4.7 Risks

- **Cisco gNOI service mapping changes between 17.x minors.** Build
  a capability-probe step (`Capabilities` RPC) into the dial path;
  don't trust release notes.
- **OS image transfer** is multi-GB and slow. The reconciler must
  not block other reconciles during a Phase-C `OS.Install` — needs
  goroutine + state-machine treatment.
- **Approval subresource** is non-trivial; copy the device-ops RFC
  design rather than inventing a new one.

### 4.8 Cross-theme dependencies

- §3 (transport consolidation) — share gRPC dial pool with gNMI.
- §5 (operator personas) — the destructive-ops persona is required
  for Phase D.
- §6 (device-ops RFC) — converges in Phase B.

---

## 5. Theme: Operator surfaces (priv config / normal config / exec) + RBAC

> The project has three implicit operator surfaces today, none of
> which has a corresponding RBAC persona shipped in the chart.
> External adopters cannot meaningfully differentiate "person who
> can change ACLs" from "person who can run `show tech-support`"
> from "person who can change BGP". This blocks production rollout
> for any sufficiently regulated environment.

### 5.1 Surfaces

| Surface | What it does | CRD(s) | Today's gate |
|---|---|---|---|
| **Privileged config** | All `IOSXEConfig` family writes (BGP, OSPF, interfaces, ACLs, DHCP, …) | `IOSXEConfig`, `IOSXEConfigBundle`, `IOSXETemplate`, defaults / group CRDs | Standard CRD verbs on the namespace |
| **Normal (scoped) config** | Persona-scoped subset: e.g. interface-only, ACL-only, banner-only | `IOSXEConfig` + admission-time family allow-list (does not exist) | None — `spec.managedFamilies` is per-CR but not enforced against requester identity |
| **Exec / show troubleshooting** | Run show commands and capture output | `IOSXEDiagnostic` | `pods/portforward` to localhost admin server ([`diagnostics-guide.md:564`](diagnostics-guide.md)) — too coarse for persona separation |

### 5.2 Phased plan

**Phase A.0 — `managedFamilies` enum enforcement at the schema (1 week, prerequisite).**
The current `IOSXEConfig.spec.managedFamilies` validation only
requires non-empty strings
([`api/config/v1alpha1/iosxeconfig_types.go:73`](../../api/config/v1alpha1/iosxeconfig_types.go));
the closed family set lives only in advisory Rego policy
([`tools/cisco-vk-config-lint/policy/families.rego:10`](../../tools/cisco-vk-config-lint/policy/families.rego)).
That means *anything* the requester wants to claim as a family
sails through the API server today, regardless of what the
controller can actually write.

Generate the family enum into the CRD spec — either as
`+kubebuilder:validation:Enum=…` markers (regenerate from
`families.yaml`) or as a `+kubebuilder:validation:XValidation`
CEL rule consulting a static list. Without this, the Phase B
allow-list webhook below is layering scoped enforcement on top
of unscoped string acceptance.
**Effort:** 1 week including a generator step.

**Phase A.1 — Persona ClusterRoles + chart wiring (1 week).**
- Three new ClusterRoles in `charts/cisco-virtual-kubelet/templates/persona-rbac.yaml`:
  - `cisco-vk-operator-priv` — full verbs on all 8 config CRDs.
  - `cisco-vk-operator-normal` — `IOSXEConfig` get/list/watch/create/update/patch
    (NOT `IOSXEConfigDefaults`, `IOSXETemplate`); no
    `IOSXEConfigBundle` (which is fanout, requires priv).
  - `cisco-vk-operator-troubleshoot` — `IOSXEDiagnostic`
    get/list/watch/create/update + `pods/portforward` only on the
    cisco-vk Pod label.
- ClusterRole names plumbed via Helm values:
  `persona.priv.create=true`, etc.
- **Deliverable:** an operator gets a coherent role to bind to
  their SA without composing primitives.

**Phase B — Family allow-list admission via webhook (2 weeks).**

This **must** be a validating webhook, not a `ValidatingAdmissionPolicy`
(CEL). The lookup is "requester's SA → which `RoleBinding`s
reference it → which one carries the
`iosxepersonapolicy/<name>` annotation → load that
`IOSXEPersonaPolicy`". CEL rules in `ValidatingAdmissionPolicy`
have no API access; they can only see the admitted object,
namespace metadata, and the requester's user identity. They
cannot list `RoleBinding`s. The existing config-lint policy
pack
([`tools/cisco-vk-config-lint/policy/README.md:13`](../../tools/cisco-vk-config-lint/policy/README.md))
is positioned for future Gatekeeper/CEL on the *object* shape,
not for this kind of cross-resource lookup.

- Validating webhook (`internal/webhook/iosxeconfig_admission.go`)
  that consults a `IOSXEPersonaPolicy` CRD:
  ```yaml
  apiVersion: ops.cisco.vk/v1alpha1
  kind: IOSXEPersonaPolicy
  metadata: { name: net-eng-team }
  spec:
    allowedFamilies: [interface_*, acl_extended, vlan]
    allowedDevices:
      labelSelector: { matchLabels: { tier: edge } }
    deniedFamilies: []
  ```
- Lookup path: requester's SA → bound `IOSXEPersonaPolicy` (via
  annotation `cisco.vk/iosxepersonapolicy=<name>` on `RoleBinding`)
  → reject CR if `spec.managedFamilies` includes a family outside
  `allowedFamilies`.
- A future migration to `ValidatingAdmissionPolicy` is possible
  *if* the binding model changes to put the policy reference on
  the object (e.g. `metadata.labels[cisco.vk/persona-policy]`)
  rather than on the `RoleBinding`. That's a Phase E discussion,
  not Phase B.
- **Deliverable:** "interface team can only edit interface_* and
  acl_*" enforced at admission, not just RBAC.

**Phase C — Diagnostic command allow-list, two enforcement points (2 weeks).**

The CR-level allow-list is necessary but **not sufficient**: ad-hoc
exec via the plugin's port-forward path bypasses the CRD entirely.
The admin server endpoint
([`internal/provider/diagnostic/adminserver/server.go:15`](../../internal/provider/diagnostic/adminserver/server.go))
treats `pods/portforward` as the auth gate; any caller with that
permission can reach `/v1/exec` and ask for unredacted output via
`--allow-secrets`
([`server.go:52`](../../internal/provider/diagnostic/adminserver/server.go)).
The troubleshoot persona is meaningless if `pods/portforward` →
unrestricted exec.

Phase C does both:

1. **CR-level enforcement.** Extend `IOSXEPersonaPolicy.spec` with
   `allowedShowCommands` (regex list, e.g. `^show interface\b`,
   `^show ip route\b`). `IOSXEDiagnostic` validating webhook
   rejects CRs whose `spec.commands` contains a command not
   matched by the requester's allow-list.
2. **Admin-server SAR enforcement.** Modify `/v1/exec` to perform
   a SubjectAccessReview against the calling identity (resolved
   from the impersonation headers the kubelet plugs in for
   port-forward) before executing. Resolve to the bound
   `IOSXEPersonaPolicy`; reject commands outside
   `allowedShowCommands`; reject `--allow-secrets` unless the
   bound policy explicitly grants `allowSecrets: true`.

- **Deliverable:** troubleshoot persona cannot capture
  `show running-config` whether they go through the
  `IOSXEDiagnostic` CR path or the plugin's `/v1/exec` path.

**Phase D — `kubectl ciscovk` persona-aware UX (1 week).**
- Plugin reads bound `IOSXEPersonaPolicy`, filters subcommand
  options accordingly, prints "you don't have permission for
  family X" when a CR would be rejected — fail fast, before
  the API call.
- Composable with §2.4 plugin expansion; ship after `diff` and
  `explain` land.

### 5.3 Design decisions to settle before Phase A

- **Bind `IOSXEPersonaPolicy` to SA, RoleBinding, or ClusterRoleBinding?**
  Recommendation: annotation on `RoleBinding` →
  `iosxepersonapolicy/<name>`. Simple, scoped per-namespace.
- **What happens when no policy is bound?** Recommendation: deny
  by default for `normal` and `troubleshoot` ClusterRoles,
  permissive default for `priv` (priv role is the override).
- **Diagnostic exec via `pods/portforward` — keep, or move to
  an APIService?** Phase C above forces the answer: portforward
  is too coarse to act as the persona gate by itself, but moving
  to an APIService (diagnostics-rfc Phase E) is parked. Bridge
  the gap with a server-side SAR check inside `/v1/exec` (the
  Phase C item) — that gives portforward + persona enforcement
  without standing up the APIService scaffolding.

### 5.4 Cross-theme dependencies

- §1.2 (`CiscoDevice` schema split) — `IOSXEPersonaPolicy.spec.allowedDevices`
  uses the same selector shape; align on label conventions.
- §3 (transport consolidation) — apphosting persona is a 4th
  surface (Pod operator); align with this scheme rather than
  inventing a parallel one.
- §4 (gNOI) — `cisco-vk-cert-operator` and
  `cisco-vk-operator-destructive` are siblings of the three
  surfaces above; share the `IOSXEPersonaPolicy` binding mechanism.

### 5.5 Risks

- **Webhook latency** — admission webhooks add ~50–200ms per
  apply. Acceptable for `IOSXEConfig`; *not* acceptable inside the
  reconcile loop. Webhook scope: admission only.
- **`IOSXEPersonaPolicy` divergence from RBAC** — operators can
  end up with RBAC saying yes and policy saying no, or vice
  versa. Document the precedence order: RBAC enforced first
  (kube-apiserver), policy second (admission webhook).
- **CEL alternative** — Kubernetes 1.30 ValidatingAdmissionPolicy
  with CEL is lighter than a webhook. Worth prototyping; webhook
  remains the fallback.

---

## 5b. Theme: Platform telemetry pipeline (MDT / OTel)

> Operators want device-side throughput, environment, BGP, OSPF, PoE,
> and TCAM telemetry surfaced as Prometheus / OTel time series next
> to cvk's controller metrics. cvk does not produce that data today
> — it consumes a narrow gNMI Subscribe slice for **drift detection
> only** ([`internal/drivers/iosxe/configdriver/transport/gnmi.go:129`](../../internal/drivers/iosxe/configdriver/transport/gnmi.go))
> and exposes controller-side metrics
> ([`internal/drivers/iosxe/configdriver/engine/metrics.go:103`](../../internal/drivers/iosxe/configdriver/engine/metrics.go)).
> The device-side platform telemetry plane is missing.

### 5b.1 Current state

- gNMI dial-IN: drift detection only
  ([`gnmi.go:129`](../../internal/drivers/iosxe/configdriver/transport/gnmi.go)).
- Controller-side Prometheus: drift, transactions, mutate-ops,
  apply-errors, subscribe-events-dropped (engine + transport
  packages).
- OTel: topology *spans* only — nodes, links, hosted apps
  ([`docs/observability.md:63`](../observability.md)). No metrics
  pipeline.
- MDT (Cisco's Model-Driven Telemetry) gRPC dial-out from the
  device: not handled. No collector. No CRD field for "configure
  MDT subscriptions on this device".

### 5b.2 Reference receiver: `jeremycohoe/otel-grpc-cisco-receiver`

A custom OTel Collector receiver that accepts IOS-XE MDT gRPC
dial-out (kvGPB encoding), translates Cisco YANG leaf values to
OTel metrics with attributes, and fans the output into any
OTel-compatible backend (Splunk HEC examples shipped, Prometheus
/ Datadog / Loki all valid). Covers interfaces / CPU / memory /
environment / BGP / OSPF / PoE / TCAM / TrustSec out of the box;
arbitrary YANG paths via mounted model files. Production-ready
performance characteristics (>1k msg/s, <10ms p99); single
contributor; preparing for upstream contribution to
`opentelemetry-collector-contrib`.

cvk does **not** take a hard dependency on this project. Instead,
treat it as the reference implementation of the *pattern* —
device-as-MDT-source, OTel-collector-as-translator,
backend-of-choice-as-storage. Telegraf's MDT input plugin and
the eventual contrib-repo receiver are alternatives operators
may pick; cvk's job is to make the device-side configuration
seamless and the integration path documented, not to ship the
collector itself.

### 5b.3 Phased plan

**Phase A — Documentation pattern (1 week).**
- Add `docs/operations/platform-telemetry.md`: how to deploy an
  OTel collector with the receiver alongside cvk, point Cat9k
  / Cat8kv at it via existing config tooling, and consume the
  metrics. Include a Helm-values overlay for the OTel collector.
- A reference Grafana dashboard (`docs/operations/grafana/`).
- Zero code in cvk. **Deliverable:** an operator can wire the
  pipeline up by following the guide.

**Phase B — Closed-loop CRD field (3 weeks).**
- Add `CiscoDevice.spec.telemetry`:
  ```yaml
  spec:
    telemetry:
      enabled: true
      receiver:
        endpoint: otel-collector.observability.svc:57500
        protocol: mdt-grpc-dialout    # | gnmi-dialin | telegraf-mdt
      subscriptions:
        - name: interfaces
          paths: [/interfaces/interface/state/counters]
          sampleIntervalMs: 30000
        - name: environment
          paths: [/environment-sensors/environment-sensor/state]
          sampleIntervalMs: 60000
  ```
- The configdriver renders this into the device's
  `telemetry` config family (NETCONF / RESTCONF / gNMI Set,
  whichever the chosen transport supports — see §3).
- The controller does **not** ship or run the collector itself;
  `spec.telemetry.receiver.endpoint` points at whatever the
  operator is running.
- **Vendor-neutrality requirement:** the schema must accept
  multiple `protocol` values so future drivers (NX-OS, IOS-XR,
  third-party) can plug in without breaking the field shape.
  cvk-on-IOS-XE today emits MDT; cvk-on-IOS-XR tomorrow emits
  the IOS-XR variant; the CRD shape is the same.
- **Deliverable:** one CR drives device subscriptions; operator
  adds a destination collector; metrics arrive in their backend
  of choice.

**Phase C — Optional bundled subchart (2 weeks).**
- `charts/cisco-virtual-kubelet-telemetry`: deploys an OTel
  collector + the upstream MDT receiver + a default
  ServiceMonitor / Splunk HEC config. Off by default; opt-in via
  `--set telemetry.enabled=true` on the parent chart, or
  `helm install` the subchart standalone.
- This is a packaging convenience, **not a hard dependency**.
  The subchart pins to a specific receiver image tag (or to
  whatever lands in `opentelemetry-collector-contrib`); operators
  who want a different receiver replace the values overlay.
- **Deliverable:** "out of the box" telemetry experience for
  greenfield deployments.

### 5b.4 Design decisions to settle before Phase B

- **Where does the YANG-path catalogue live?** Phase B needs a
  validation list (which paths cvk knows are sane on Cat9k 17.18).
  Recommendation: extend the existing
  `internal/drivers/iosxe/configdriver/schema/` catalogue with a
  `telemetry-paths.yaml` rather than inventing a new validation
  pipeline.
- **Push vs pull semantics.** MDT dial-out is push; gNMI dial-in
  is pull. The CRD's `protocol` field has to be honest about
  which: `mdt-grpc-dialout` requires the device to know the
  receiver's address (operator deploys collector first, then
  references it); `gnmi-dialin` requires the receiver to know
  the device's address (the existing cvk transport already does
  this for drift). Don't conflate.
- **Schema evolution.** `spec.telemetry.subscriptions` will grow
  per-vendor. Use a discriminated-union pattern (`type` +
  `mdt:` / `gnmi:` / `streaming-telemetry:` siblings) to leave
  room for IOS-XR's protobuf-over-TCP and NX-OS's NX-API.
- **Cross-cut with §3 transport consolidation.** Phase B's
  rendering of telemetry config onto the device goes through
  the same configdriver `transport.Interface`. If §3 hasn't
  finished, only the RESTCONF path will work. That's acceptable
  for Phase B; Phase C should ride on top of §3 Phase B (NETCONF
  + gNMI bindings) so any chosen device protocol can carry
  telemetry config.

### 5b.5 RBAC implications

- Phase A: no new RBAC.
- Phase B: the configdriver already has the necessary device-write
  RBAC for the `telemetry` family; nothing new on the cvk side.
  The collector deployment runs in the `observability` namespace
  with its own SA — out of cvk's scope.
- Phase C: new chart values for the subchart's collector SA;
  recommend a default Pod Security Standard of `restricted`,
  NetworkPolicy egress-only to the OTel receiver port, no
  pod-to-cvk ingress.
- Cross-link with §5 personas: the *config* persona can declare
  `spec.telemetry`, but cannot deploy or modify the collector
  itself; that's a platform-team concern.

### 5b.6 Risks

- **Adoption risk on the reference receiver.** 0 stars, single
  contributor, no contrib-repo home yet. Mitigation: Phase A is
  documentation-only and has no dependency on this specific
  project; the pattern works with telegraf-mdt or any future
  contrib receiver.
- **Vendor lock at the wrong layer.** MDT kvGPB is Cisco-specific;
  cvk's design point is that the configdriver insulates operators
  from vendor protocol details. Mitigation: `spec.telemetry`
  schema is vendor-neutral (paths + intervals + endpoint); the
  driver translates.
- **Cardinality explosion.** Per-interface, per-sensor metrics
  on a 96-port Cat9k * 100 devices = ~10K series per device, ~1M
  series fleet-wide. Mitigation: document the cardinality budget
  in Phase A; recommend recording rules for fleet-aggregate
  metrics; default the chart's ServiceMonitor scrape interval to
  60s, not 15s.
- **Collector drift.** OTel collector versions move quickly; the
  reference receiver pins to v0.138+. Phase C's subchart needs a
  version-bump cadence (quarterly?). Add to release-engineering
  checklist.
- **Privacy / data-egress.** Some operators forbid telemetry
  egress to cloud backends. Phase A guide must show a
  fully-on-prem path (collector → Prometheus / Splunk on-prem)
  alongside the cloud examples.

### 5b.7 Cross-theme dependencies

- §3 (transport consolidation) — Phase B device-side rendering
  benefits from §3 Phase B (NETCONF / gNMI bindings); without
  them, only RESTCONF can carry the telemetry config.
- §1.4 (production chart profile) — when the parent chart's
  ServiceMonitor is enabled, document the discovery path for
  the telemetry subchart's metrics endpoint as well.
- §7.3 (per-device throughput aggregate) — §7.3 is a
  controller-side metric (per-CR + per-family aggregates that
  the controller already has visibility into). §5b is a
  device-side metric (per-interface counters straight from the
  device). Different sources, complementary outputs; both should
  exist. Cross-reference rather than substitute.

### 5b.8 Effort summary

| Phase | Effort | Gate |
|---|---|---|
| A — pattern docs | 1 week | None |
| B — `spec.telemetry` CRD field | 3 weeks | §1.2 (schema decisions for v1) ideally precede; otherwise ship as `v1alpha1` and migrate |
| C — bundled subchart | 2 weeks | A + B; benefits from §3 Phase B for non-RESTCONF transport |

Tier classification: **Tier-2** (operator-facing value, not a
release-readiness blocker). Phase A can land in any quarter;
Phase B should follow §1.2's `CiscoDevice` schema decision so it
doesn't churn that field again at v1; Phase C is opportunistic.

---

## 6. Tier-3 — v1 CRD promotion

After §1 lands. The plan in
[`crd-v1-promotion-plan.md`](crd-v1-promotion-plan.md) is sound but
under-scoped (see §1.1).

- **Single compatibility release** for the `v1alpha1`-and-`v1`
  overlap. *Don't* commit to a 3-release soak window unless
  external users exist by then.
- **Gating:** all of §1 done; §3 Phase A done; §5 Phase A done;
  §4 Phase A NOT required (gNOI is additive).
- **Effort:** 3 weeks (webhook, conversion, dual-version chart,
  upgrade docs).

---

## 7. Tier-4 — Reliability / observability remaining

### 7.1 Log unification
- Today: split logrus + zap (
  [`cmd/cisco-vk/run.go:160`](../../cmd/cisco-vk/run.go),
  [`manager.go:81`](../../cmd/cisco-vk/manager.go),
  [`config_reconciler.go:140`](../../cmd/cisco-vk/config_reconciler.go)).
- The architectural-review RFC labels the divergence "cosmetic"
  ([`architectural-review.md:528`](architectural-review.md)).
- Rank: **low**, contradicting the prior roadmap which had it as
  reliability. Operationally annoying but not a release-readiness
  blocker.
- **Effort:** 1 week.

### 7.2 `internal/provider` test coverage
- 25.7% today. The package is wiring-heavy; lift coverage via
  envtest-driven aggregator-lifecycle tests (`make test-envtest`
  exists since H2 / commit `573e48f`).
- **Effort:** 2 weeks for >70% coverage.

### 7.3 Per-device throughput telemetry (controller-side)
- Today: config metrics carry `device`, `family`, `transport`,
  `verb` labels
  ([`operator-cli-guide.md:438`](operator-cli-guide.md));
  apphosting throughput exposes only per-interface counters with
  `interface` labels
  ([`operator-cli-guide.md:476`](operator-cli-guide.md)). An
  operator wanting "throughput per device, per IOSXEConfig CR,
  per family" has to glue label sets across two metric families.
- **Action:** add a `cisco_vk_apphosting_pod_throughput_*`
  metric family with `device` + `pod` labels (joined to
  `interface` via a recording rule), and a
  `cisco_vk_config_writes_per_device_total` aggregate. Document
  the canonical Grafana dashboard alongside.
- **Effort:** 1 week including dashboard JSON.
- **Cross-reference §5b.** §7.3 is the *controller-side* metric
  (what the controller saw, with CR + family attribution). §5b
  is the *device-side* pipeline (what the device pushed via MDT,
  raw counters and environment data). They are complementary —
  one shows "the controller successfully wrote interface X 47
  times this hour"; the other shows "interface X carried 4.2 Gbps
  during that hour". Both should exist; neither subsumes the
  other.

### 7.4 Chaos / disaster recovery drill
- Today: `internal/provider/diagnostic/sink.go` and
  finalizer-driven cleanup are validated by unit tests, but
  there is no "controller crash mid-confirm-commit" smoke test,
  no "device unreachable for 30 min" drill, no
  "etcd snapshot + restore" drill.
- **Action:** add a `tests/chaos/` directory with kind-based
  scenarios that kill the controller mid-reconcile, partition
  the device, and assert recovery. Wire into `lab-ci/cat8kv`
  on a weekly schedule (NOT on every PR — too expensive).
- **Effort:** 2 weeks initial + ongoing maintenance.

---

## 8. Tier-5 — Config engine extensions

### 8.1 Template loops
- Conditionals in
  [`iosxe-config-driver-review.md` §7.5](iosxe-config-driver-review.md)
  shipped; loops are the next chunk.
- Phase-4 follow-up; not blocking.

---

## 9. Explicitly parked / dropped

These were on the prior roadmap; they should not be on the new one.

- **Diagnostics RFC Phase E (APIService aggregation)** — parked
  per the RFC's own conditional clause. Only revisit if the
  port-forward + plugin UX proves insufficient at scale; no
  evidence of that yet.
- **`internal/configdriver/` cosmetic relocation** — defer until
  *after* v1 cut; the divergence cost is real but the rename
  cost during v1 schema work is worse.
- **Sequence-keyed diffing** — already shipped (see §0).
- **Subscribe-overflow metric / OTel reconciler spans / fuzz
  targets** — already shipped (see §0).

---

## 10. Sequencing

> The shape: **release-readiness first, schema freeze second,
> capability extensions third.** Concretely:

```
Q1                    Q2                    Q3                    Q4
────────────────────  ────────────────────  ────────────────────  ────────────────────
§1 Tier-1 readiness   §3 Phase A            §6 v1 CRD promotion   §3 Phase D
  • CRD inventory       (LifecycleCapable     (after §3 Phase A     (convergence
  • CiscoDevice schema  + typed args +        AND §1 AND §5         boundaries
  • TF spec coverage    whole-lifecycle       Phase A.0+A.1         doc + cleanup)
  • prod chart           lock + apphosting    landed)             §3 Phase C (per-
    (incl. lease NS)     refactor)         §3 Phase B (NETCONF      family transport
  • inline-pwd dep    §4 Phase A.1            apphosting on real    overrides)
  • backup/restore      (Cert services,       hardware)           §4 Phase D (factory
  • multi-YANG          ops.cisco.vk         §4 Phase B (System +    reset, gated)
§4 Phase A.0            group, split          device-ops Reload   §5 Phase D (plugin
  (gNOI capability       Policy/Op CRDs,      + Erase + Probe)      persona-aware UX)
   probe milestone)      cert-manager flow) §4 Phase C (File +    §7.4 Chaos drills
§5 Phase A.0          §5 Phase A.1            OS image install)     (weekly schedule)
  (managedFamilies      (persona            §5 Phase B (family    §8 Template loops
   enum)                 ClusterRoles +       allow-list webhook) §5b Phase C (bundled
§2 status fields +       chart wiring)      §5 Phase C (diag       telemetry subchart,
  printer columns     §2.4 packaging        command allow-list +    rides on §3 Phase B)
§2.2 selectableFields    + plugin diff       admin-server SAR)
§2.5 OTel family attrs §7.3 throughput       §2.4 plugin explain
§5b Phase A (telemetry   metrics + dash       / health / replay
  pattern docs)       §5b Phase B
                         (CiscoDevice.spec.
                          telemetry CRD,
                          rides on §1.2)
                       §7.1 Log unification
                         (cosmetic; deferred
                          if Q2 is full)
```

### Sequencing notes

- **§1.2 must precede §3 Phase A.** The `CiscoDevice` schema
  decision determines whether `LifecycleCapable` is on
  `transport.Interface` or sits beside it.
- **§4 Phase A.0 (capability probe) must precede §4 Phase A.1.**
  The Cert CRD shape and the gNOI port assumption both depend
  on the lab probe results; do not schedule A.1 in Q1.
- **§5 Phase A.0 (managedFamilies enum) must precede §5 Phase B.**
  Layering scoped enforcement on top of unscoped string acceptance
  is incoherent.
- **§3 Phase A must complete before §6 v1 promotion.** v1 freezes
  `CiscoDevice.spec.transport`; that schema interlocks with
  `LifecycleCapable` placement. Q3 is the earliest §6 should
  start, not Q2 — rev. 1 of this doc had them concurrent, which
  contradicted §6's own gating clause.
- **§5 Phase A.1 (persona ClusterRoles) must complete before §6.**
  v1 promotion is the right time to ship persona roles in the
  chart so the v1 release is the first time external operators
  see "production-shaped RBAC out of the box".
- **§3 and §4 share gRPC client library / TLS material**, but
  **NOT the dial endpoint** — gNMI defaults to 50052, gNOI
  must be capability-probed and configured separately. Don't
  collapse the two configs.
- **§6 v1 promotion** is gated on §1 (all of), §3 Phase A, and
  §5 Phase A (both A.0 and A.1) being live.
- **§5b Phase B (`CiscoDevice.spec.telemetry`)** rides on §1.2's
  `CiscoDevice` schema decision so the field shape is settled
  before v1 freezes it. If §1.2 slips, ship §5b Phase B as
  `v1alpha1` with explicit migration semantics rather than
  blocking §5b on §1.2.
- **§5b Phase C (bundled telemetry subchart)** rides on §3
  Phase B (NETCONF + gNMI bindings) so any device transport can
  carry the telemetry config; otherwise restricted to RESTCONF.

---

## 11. Process notes for future updates

When updating this file:

1. **Re-verify the §0 status snapshot.** Run
   `git diff <previous-tag>..HEAD -- internal/ api/ charts/`.
   Items moved from "not started" → "shipped" in code must be
   moved out of Tier-N and into §0.
2. **Tier-1 (release-readiness) only retires when the v1 cut
   ships.** Don't promote partially-done items out of Tier-1.
3. **Codex `/codex:adversarial-review`** on this file before
   each release; a roadmap is a design artifact and adversarial
   review catches the same staleness Codex caught in revision 1
   of this doc.
4. **External-user signals** — every adopter contact should
   produce at least one inline annotation on this file
   (`<!-- adopter: $name asked for $thing $date -->`); decisions
   to defer should reference those signals, not RFC text.
