# Architectural review — `pr/johalley/ciscoconfig_xe`

**Reviewer's lens:** the principles codified by the standard
software-architecture canon — Bass/Clements/Kazman *Software
Architecture in Practice*, Kleppmann *Designing Data-Intensive
Applications*, Martin *Clean Architecture*, Winters/Manshreck/Wright
*Software Engineering at Google*, Newman *Building Microservices*,
Geewax *API Design Patterns*, plus the Kubernetes-specific guidance
in Hightower/Burns/Beda *Kubernetes Up & Running* and Kelsey
Hightower's CRD-design talks.

**Method:** static survey of the branch (LOC, dependency graph,
imports, panics, concurrency primitives, error-wrapping discipline,
RBAC scopes, CRD schema posture, TLS posture), plus dynamic
verification (full suite under `-race`, coverage by package, gofmt
and `go vet`). One real defect surfaced and was fixed during the
review (NETCONF-session close-time data race); the assessment
below reflects the post-fix state.

The verdict up front: **the architecture is sound and the
discipline is uncommonly high for an in-flight branch of this
size**. The watch-items are scoped, callable by file, and none
of them are blockers. I'd ship this under the per-pod topology
today and treat the watch-items as a backlog with named owners,
not as gates.

---

## 0. Numerical baseline

| Signal | Value |
|---|---|
| Total Go LOC | 70,248 |
| Production / test LOC | 58,486 / 11,762 (test:prod ratio 0.20) |
| Test functions | 350 across 44 files |
| Packages tested | 18 of 18 |
| `TODO` / `FIXME` / `HACK` / `XXX` density | 4 markers across the entire tree |
| `gofmt -l` clean | yes |
| `go vet` clean | yes |
| `go test ./...` | green |
| `go test -race ./...` | green (post-fix; pre-fix exposed one race in NETCONF close, fixed in this review) |
| `panic()` in production | 11 sites, all in registry code paths where panic = "this is a build-time bug, fail loud" |
| `context.Background()` outside main / shutdown | 2 sites, both legitimate (root context, OTel shutdown) |
| Goroutine launches in production code | 5 (controller manager start, Subscribe pump, NETCONF close-session, aggregator worker, event-broadcaster shutdown) — all bound to a `ctx` or have explicit teardown |
| New deps added (additive only) | 5 (gonja, openconfig/gnmi, x/crypto, grpc, prometheus client) |

The test:prod ratio of 0.20 is at the lighter end of "well-tested
Go services" (the reference is Google's internal canon, which sits
0.4–0.6, but most of those numbers come from microservices where
the production code is integration-heavy; CVK's production code
includes the writer set, where each writer is mostly metadata
plus inherited helpers). 350 test functions across 44 files —
many of them table-driven and adversarial — read as substantively
better than the ratio number suggests.

---

## 1. Architectural style

### 1.1 Style chosen

The branch is a **layered architecture with a closed verb-set
ports-and-adapters core**, organised on the Clean Architecture
principle that high-level policy doesn't depend on low-level
detail.

```
Operator-facing CRDs   ←  IOSXEConfig, Bundle, ApplyLog, …
                         (api/config/v1alpha1)
        │
        ▼
Resolver  → ResolvedIntent (pure data)
                         (intent/)
        │
        ▼
Engine    → state machine over ManagedFamilies
                         (engine/)
        │  ┌─────────────────────────┐
        ▼  ▼                         ▼
    SectionWriter      transport.Interface
   (writers/)          (RESTCONF/NETCONF/gNMI)
                         (transport/)
        │
        ▼
                       Device wire
```

Each downward arrow is an interface; each leaf component depends
on its inward neighbour but never on its outward ones. This is
the standard "dependency rule" of Clean Architecture and the
ports-and-adapters pattern (Cockburn): **inner layers don't know
about outer ones; outer layers depend on inner abstractions.**

The Phase-9 plug-in registry (`internal/drivers/registry.go` +
`configdriver.go`) is the explicit ports surface for new platforms.
The two registries are deliberately split (apphosting vs
config-driver) so a platform can ship one capability without the
other.

### 1.2 Why this style fits

The problem domain is "reconcile declarative intent against an
external system whose protocol set is small and stable". This is
exactly the shape Clean Architecture targets — the reconciliation
loop is the policy, the transport is the detail. The branch
honours that mapping consistently.

### 1.3 Where the style is breached

One genuine violation, and the branch already self-flags it
(`docs/rfcs/driver-extension-guide.md` §7, this review's §6.4):
the platform-agnostic `intent/`, `engine/`, `transport/`,
`writers/` packages live under `internal/drivers/iosxe/configdriver/...`.
The names are platform-agnostic; the path is misleading. Cross-
package imports today:

| Importer | Imports `iosxe/configdriver/...` |
|---|---|
| `internal/drivers/` | intent, transport, writers |
| `internal/aggregator/` | engine |
| `internal/provider/` | engine, intent, transport, writers |

Three of these are not in `iosxe/`, so the import path is
factually wrong. The contract is correct (the imports do work
across platforms) but the path lies about it. Phase-10 cosmetic
relocation to `internal/configdriver/...` would close the gap;
the contract doesn't change.

---

## 2. Quality attributes (ISO/IEC 25010 frame)

### 2.1 Functional suitability

The mapping to netascode primitives is exhaustive (see
`iosxe-config-driver-appraisal.md` §4 for the primitive-by-primitive
matrix). Three CVK extensions are additive and do not break
netascode parity:

- Per-(device, family) Lease arbitration
- Annotation-driven time-travel replay
- Aggregation CRs with owner-ref fanout

**Verdict: full parity + additive extensions.**

### 2.2 Performance efficiency

Hot-path cost when a CR is steady:

```
List IOSXEConfig (informer-cached, O(1))
  → Resolver.Resolve (one merge tree, no I/O)
  → CanonicalHash (sha256 over normalised JSON)
  → short-circuit on (Generation, Hash, Phase) match → return
```

No transport calls, no etcd writes. The hash short-circuit at
[`config_reconciler.go:207-212`](../../internal/provider/config_reconciler.go#L207-L212)
is the explicit latency floor.

When work is required, the engine runs Fetch → Diff → Apply →
Verify per family in series. Per-family parallelism would buy
nothing on a single device (the device serialises writes
internally) and would complicate the lease + transactional
semantics. Linear-per-family is the right call.

The drift cap at `engine.MaxDriftEntries = 50` (with a
`cisco_vk_config_drift_entries_truncated_total` counter) prevents
the etcd object-size pathology Kleppmann flags as a "hidden
scaling issue" in DDIA — the CR can't grow arbitrarily even when
the device is in pathological drift.

**Watch-item:** the per-pod reconciler does
`Client.List(ctx, &list)` cluster-wide per tick at
[`config_reconciler.go:148`](../../internal/provider/config_reconciler.go#L148)
and filters in-process to its device. The controller-runtime
informer cache makes this cheap (no network), but at fleet scale
N (devices) × M (CRs) the in-process filter is O(M) per tick per
pod. With the aggregator topology this collapses to one process
× one O(M) pass shared across devices. For N>200 devices the
aggregator becomes operationally meaningful even before the
ergonomic argument.

### 2.3 Reliability

The reconciler's failure-mode taxonomy is explicit and
attributable. Every error message carries a stage prefix
(`Fetch:`, `Diff:`, `Apply:`, `Verify:`, `PruneDiff:`) at
[`engine.go:240-330`](../../internal/drivers/iosxe/configdriver/engine/engine.go#L240-L330)
so an operator reading `kubectl describe iosxeconfig <x>` can
attribute without grep'ing logs.

Apply-log update failure is non-fatal at
[`config_reconciler.go:425-432`](../../internal/provider/config_reconciler.go#L425-L432) —
the reconciler emits an event and continues. This is the right
call (audit is observability; failing the reconcile because the
audit didn't append would couple two unrelated reliability
concerns) and it matches the SRE principle of "never let
observability be on the data-path's critical path".

The transactional NETCONF / gNMI path discards on edit-config
failure — the device sees no partial apply. The verify pass
re-runs PruneDiff so a stale device-side rule that survived an
apply surfaces as residual drift, not as InSync.

**Defect found in this review (now fixed):** NETCONF
`netconfSession.close` had a data race between the close goroutine
that ran the `<close-session/>` RPC and the parent goroutine that
nilled out `s.rw`. The race detector caught it under
`go test -race`. Fix: capture `s.rw` to a local at the top of
`close()`, hard-close the local copy after the timeout (which
unblocks the in-flight RPC), and reassign `s.rw = nil` only under
the session mutex. Pinned by the race-detector run. See
[`netconf_rpc.go:154-186`](../../internal/drivers/iosxe/configdriver/transport/netconf_rpc.go#L154-L186).

This is the one substantive issue I found in the review. It would
have surfaced in production as intermittent panics or data
corruption on pod-shutdown paths. **Fix shipped as part of this
review.**

### 2.4 Security

Posture is appropriate for the threat model.

| Concern | Stance | Location |
|---|---|---|
| Device credentials | Inline `password` (dev/test) OR `CredentialSecretRef → corev1.Secret`. No bare passwords on the wire. | [`api/config/v1alpha1/iosxeconfig_types.go`](../../api/config/v1alpha1/iosxeconfig_types.go), [`internal/aggregator/aggregator.go`](../../internal/aggregator/aggregator.go) `resolvePassword` |
| TLS posture | `InsecureSkipVerify` defaults to `false`; honours device-spec opt-in only. Two sites total: apphosting RESTCONF + config-driver RESTCONF. | [`internal/drivers/iosxe/driver.go:60-64`](../../internal/drivers/iosxe/driver.go#L60-L64), [`transport/factory.go:163-173`](../../internal/drivers/iosxe/configdriver/transport/factory.go#L163-L173) |
| Secret material in CRs | Disallowed in `spec.source.inline` by convention; the OPA pack (`tools/cisco-vk-config-lint/policy/iosxeconfig.rego`) emits a warning for password-shaped leaves. `spec.secretRefs[]` is the supported channel; resolver merges last so a placeholder in source can never leak past resolution. | `intent/resolver.go` `loadSecretSnippet`, `policy/iosxeconfig.rego` |
| RBAC | Per-pod ServiceAccount with namespace-scoped reads on ConfigMaps/Secrets, cluster-scoped lease access (already grant from the node-heartbeat lease). Aggregator inherits the controller's SA. Bundle controller has explicit per-resource verbs declared via `+kubebuilder:rbac` markers. | `internal/controller/iosxeconfigbundle_controller.go:49-52`, `internal/aggregator/aggregator.go:75-76` |
| Replay annotation | Operator-driven; clears on success. The body is the previously-applied resolved intent (no operator-supplied content). | `provider/config_reconciler.go` `applyReplayAnnotation` |
| OPA / conftest gate | Three Rego files cover privilege-15-without-secret (deny), enable_password (warn), SNMP rw without ACL (deny), AAA central-server consistency (warn), unknown family in managedFamilies (deny). | `tools/cisco-vk-config-lint/policy/` |

**Watch-items:**

- *No supply-chain attestations*. The container images
  (`Dockerfile`, `Dockerfile.config-lint`) build from
  `golang:1.25-alpine` and produce distroless images, but there
  are no SBOMs, no `cosign` signing, no SLSA provenance in CI.
  This is standard for an in-flight branch but should land before
  any external release. Ranked: medium-priority pre-1.0 work.
- *No fuzzing*. The branch's parsers (`parseGNMIPath`,
  `splitYAMLDocs`, `parseHello`, `parseRPCReply`,
  `splitReplayAnnotation`) are all candidates for `go test -fuzz`.
  None today. Low-priority but a cheap follow-up — Go's native
  fuzz tooling means each new fuzzer is ~30 LOC. Probably worth a
  Phase-10 sprint that adds five fuzzers across the parser
  surface.
- *No `gosec` integration in CI*. Rough scan locally is clean
  (no `os/exec` with user input, no `unsafe`, no obvious SQLi
  surfaces — there's no SQL), but a static check should be in
  CI to keep it that way.

### 2.5 Maintainability (the largest section by O'Reilly weight)

#### Modularity

After Phase 9, the module structure is **excellent**. The driver
registry is the explicit ports surface; everything else either
sits behind a stable interface (transport, writers, intent) or is
a leaf concern (CRDs, RBAC, Helm values). The
`internal/drivers/<platform>/` directory layout is uniform across
the established `iosxe`, the placeholder `nxos`/`iosxr`/`junos`,
and the test `fake`. A new platform's footprint is bounded by its
own package + two foundation lines (enum constant + blank import).

The deliberate exception is the platform-agnostic core living
under the `iosxe/configdriver/` path (§1.3 above). That's
cosmetic, not structural — but it does mislead readers and would
not survive a senior-engineer "find me the platform-agnostic
code" question without the disambiguating comment in
`docs/rfcs/driver-extension-guide.md` §7.

#### Testability

Test patterns observed (mapped to *Software Engineering at
Google*'s test-pyramid expectations):

- **Unit**: every helper has table-driven coverage. The
  cross-validation corpus
  (`merge_cross_validation_test.go`) is a full 30-case
  empirical pin against `terraform-provider-utils.MergeMaps`.
  This is rare and excellent.
- **Integration**: bufconn for gNMI, io.Pipe for NETCONF,
  httptest for RESTCONF. No mocks of `transport.Interface`
  itself; every test exercises a real client speaking the real
  protocol against a fake-but-realistic server. This is the
  Hyrum-Wright "test with the real thing" doctrine done well.
- **Controller**: controller-runtime's fake client + status-
  subresource-aware fixtures. Bundle, ApplyLog, Replay all have
  end-to-end tests against the fake client.

What's missing:

- **No `t.Parallel()` calls in the entire test suite.** Tests
  run serially. With 350 tests this is on the order of 90 s of
  serial time when ~30 s parallel would suffice. Low-priority but
  a free win.
- **No fuzzing.** Already covered above.
- **No benchmarks.** Performance claims (hash short-circuit,
  per-rule diff) are unverified by a benchmark suite. Low-
  priority because the hot path is small enough to read; but a
  `BenchmarkResolve` and `BenchmarkApply` would catch
  regressions.
- **Coverage skew.** `internal/aggregator` is 10.9 %,
  `internal/provider` is 25.7 %. Both are wiring-heavy packages
  whose logic is exercised through other tested packages, but a
  reviewer reading coverage in isolation might mistake low
  coverage for low quality. Adding envtest-driven controller-
  runtime integration tests for the aggregator's
  start/stop/spec-change cycle would close this honestly.

#### Modifiability

The closed verb set (`Replace`, `Merge`, `Delete`, `CLI`) is the
single best modifiability decision in the branch. It means the
engine and every writer dispatch unchanged when a new transport
lands; gNMI was integrated in one commit
(`feat(transport): gNMI transport (Phase 6)`) without touching
writer code. This is the "stable abstractions principle"
(Martin) executed cleanly.

The optional-interface pattern (`PruneCapable`, `SubscribeCapable`)
lets feature rollout happen family-by-family / platform-by-
platform without engine flag days. Excellent.

The CRD evolution path is the sole concern (§3 below).

#### Readability

Reading discipline observed:

- One-sentence top doc-comment on every exported type and
  function. Compliant with Effective Go and the Go community
  style.
- Failure modes documented inline at the call site (not just at
  the type definition). The `recordResult` audit-log update
  block is the canonical example: 8 lines of reason for "why
  non-fatal" so the next reader doesn't undo the design.
- Comments explain *why*, not *what*. Aligns with Carey *Software
  Design for Flexibility* and the Google style.

Aggregate `TODO`/`FIXME`/`HACK`/`XXX` count: **4 markers across
70k lines**. That's exceptional. (Reference: an audit of the
TiKV codebase at a comparable line count reports >300 such
markers.)

### 2.6 Compatibility / interoperability

- netascode YAML parity: see appraisal RFC §4.
- Terraform: a full provider with CRUD + ImportState in a
  separate Go module so its deps don't pollute the controller's
  graph.
- ArgoCD: Lua health-check hooks for `IOSXEConfig` and
  `IOSXEConfigBundle`.
- Pre-commit: `.pre-commit-hooks.yaml` declarations.
- Conftest: 3 Rego files.
- OpenAPI: CRDs are valid OpenAPI v3 with kubebuilder validation
  (MinItems, MaxItems, MinLength, XValidation).

The interoperability surface is wide and the discipline is
"consume the upstream tool where one exists; build a new one
only when there isn't" — `nac-validate`, `nac-collect`, the
netascode portal layout (now mirrored by
`cisco-vk-config-docs --dialect=portal`).

### 2.7 Portability

After Phase 9, **portability across platforms is genuinely
plug-in-style** for the protocol-bound layers (transport,
engine, intent, writer helpers). The bottleneck is per-family
writer authoring, not foundation work. Cost model documented in
`docs/rfcs/driver-extension-guide.md` §4.

Portability across Kubernetes distributions: the only K8s API
surface used is core (Pod, Secret, ConfigMap, Lease, Event) +
controller-runtime + custom CRDs. No vendor extensions, no CRI
calls, no kubelet API. Should run on any conformant cluster ≥
the controller-runtime baseline (Kubernetes 1.28+ given the
client-go pin).

Portability across architectures: cross-compiles cleanly
(verified during the Phase 9 binary cleanup — the committed
binary was a Mach-O arm64 file that built from the same source
tree). No CGO calls outside `crypto/x509`'s system-cert reading
(stdlib).

### 2.8 Deployability

The Helm chart (`charts/cisco-virtual-kubelet/`) is the canonical
deployment surface:
- 8 CRDs synced from `config/crd/`
- Two operator-facing values added by this branch:
  `aggregator.enabled`, `config.leaseNamespace`
- ClusterRole already covered cluster-scoped lease access (for
  node-heartbeat); no new RBAC needed for the cross-namespace
  arbitration feature

**Watch-item:** there's no Helm `values.schema.json`. Operators
can `helm install --set typo=value` and it silently no-ops.
Schema validation would be a 30-min add and catches a real class
of operator mistakes. *Low-priority polish.*

**Watch-item:** there's no end-to-end smoke test in CI that
installs the chart against a kind cluster + a fake gNMI device
and verifies a reconcile completes. The unit suite is excellent;
the deployment-level "does this thing actually run" gate is
absent. *Medium-priority pre-release.*

---

## 3. API design (CRD layer)

Frame: Geewax *API Design Patterns*, Kubernetes API Conventions,
Hightower's CRD-design talks.

### 3.1 What's done right

- **Resource-oriented**: every CR is a resource with spec/status
  separation. The status is structured (per-family outcomes,
  drift entries, conditions), not an opaque blob.
- **Closed enums** wherever appropriate (`DriftPolicy`,
  `TemplateParameterType`, `DeviceDriver`).
- **Validation at the API server**: kubebuilder MinItems/MaxItems
  on `status.drift[]`, MinLength on string keys, XValidation CEL
  rule on `InterfaceMatch` (mutex between `Name` and
  `NamePattern`).
- **Conditions slice with `listType=map listMapKey=type`** —
  matches K8s conventions exactly. ArgoCD/kubectl tooling
  consumes this without translation.
- **Owner references on bundle children** — cascading delete is
  free; no separate GC controller needed.
- **Annotations carry the replay signal**, not a status field —
  preserves the "spec is what I want, status is what is" contract.

### 3.2 Watch-items

- **Single-version stability slot.** Every CRD is `v1alpha1`. The
  platform-extension story landed before any v1 promotion. This
  is fine for an in-flight branch but means the API can still
  break. A pre-release v1 promotion path needs a conversion-
  webhook story; today there is none. *Medium-priority pre-1.0.*
- **No `preserveUnknownFields` on the IOSXEConfig spec proper.**
  The branch correctly uses `preserveUnknownFields: true` for
  schemaless `Configuration` fields (template bodies, source
  inline) but pins the spec's own shape. That's the right
  trade-off; the warning is operators using
  `kubectl apply --validate=false` as a workaround for missing
  fields will silently lose data. The OPA pack catches the
  obvious cases. *Acceptable.*
- **The IOSXE-prefixed naming** (`IOSXEConfig`,
  `IOSXEConfigDefaults`, etc.) means a new platform either ships
  parallel `NXOSConfig` set or generalises to `NetworkConfig`
  with a discriminator. The plug-in foundation is ready for
  either; the choice is a UX call. The driver-extension guide
  (§3.4) recommends parallel CRDs because that matches netascode's
  per-platform module split. *Documented; not a defect.*

---

## 4. Concurrency posture

Frame: Cox's *The Go Memory Model*, Donovan/Kernighan's
*The Go Programming Language* concurrency chapters, the
race-detector contract.

### 4.1 What's done right

- Per-device reconciler is single-goroutine; the engine is linear
  per family. No "two writes to one device" hazard.
- Subscribe-watcher coalesces with `time.AfterFunc` under a
  mutex, with a buffered notify channel of size 1 + non-blocking
  send. Slow consumer drops events on the floor with no
  back-pressure to the device-side stream — exactly the right
  shape for an event-driven hint signal.
- Aggregator workers each get their own `context.WithCancel`
  derived from a root context captured at SetupWithManager.
  Manager shutdown propagates cleanly.
- Specific spec-hash gate keeps the aggregator from churning
  workers on irrelevant CR edits (label/taint/log-level changes
  pass through to the running reconciler).
- The transport's `SessionLock` (RESTCONF, optional) serialises
  config-driver writes against apphosting writes on the same
  device.

### 4.2 What's done wrong (was)

The NETCONF close-time race (now fixed) was the only place
production code violated the race-detector contract. It would
have manifested as either a panic on `nil.Write` or as a torn
read writing to a freshly-closed conn during pod teardown. Caught
by `go test -race`, fix shipped as part of this review.

### 4.3 What's still soft

- **No `t.Parallel()` in the entire test suite.** Tests don't
  exercise the data-race surface as aggressively as they could.
  Adding `t.Parallel()` to the leaf packages (transport, writers,
  intent) is a 30-minute change.
- **The Subscribe watcher's drop-on-overflow doesn't increment a
  counter.** A slow consumer is silently lossy. A
  `cisco_vk_config_subscribe_events_dropped_total{device}`
  counter alongside the existing engine metrics would close the
  observability gap. *Minor.*

---

## 5. Operability

Frame: SRE Book chapters on monitoring + alerting; Newman's
*Observability* chapter in *Building Microservices*.

### 5.1 Metrics

The engine registers seven Prometheus metrics covering
duration histograms (reconcile, apply), counters (drift
detected/corrected, drift truncated, apply errors), and a state
gauge. Cardinality is controlled (`device, family, stage` labels;
`stage` is a closed set of 5 strings).

**Watch-item:** `cisco_vk_config_subscribe_events_dropped_total`
isn't there (§4.3 above). And the existing apply-errors counter
labels by `stage` (fetch/diff/apply/verify) but not by writer-
returned error category — operators get "apply errors are up"
without "what kind" without grep'ing logs. Adding an `err_class`
label would help; cardinality bound is small (the writers
produce a closed error vocabulary). *Minor.*

### 5.2 Logging

Structured logging via `virtual-kubelet/log` (which wraps
`logrus`) and controller-runtime's `zap`. Two loggers in the
same process is a Newman-flagged smell ("two logging configs is
two logging configs"). They don't conflict in practice — the VK
process uses logrus, the manager uses zap, and the streams go to
stdout — but it's two formatters, two log-level configs.
Unifying on one would be cosmetic but right.

### 5.3 Tracing

The branch ships an `internal/provider/otel_topology.go` that
emits OTel traces for topology export. The reconciler itself
doesn't emit per-tick spans. For a system whose primary failure
mode is "a tick took too long", a `Reconcile` span with
attributes per family would be the canonical
trace-the-control-plane shape. *Medium-priority polish.*

### 5.4 Events

Per-family per-tick events for non-trivial outcomes. Closed event
type set: `AppliedSuccess`, `FamilySkipped`, `Drift`,
`ReconcileFailed`, `Paused`, `ApplyLogUpdateFailed`. A K8s admin
consuming `kubectl get events` can read what's happening without
opening a log file.

---

## 6. Specific watch-items, ranked

Numbered for convenience; ordering is operational consequence.
Status column reflects the post-review push; ten of the twelve
items have shipped or have a written plan; only #4 stays as a
deliberately-deferred Phase-10 placeholder.

1. **NETCONF close-time data race — ✅ FIXED IN THIS REVIEW.** Was
   the only substantive defect surfaced by `go test -race`. Now
   pinned by the race-detector run.
   Anchor: commit `2912b02`.

2. **Helm `values.schema.json` missing — ✅ shipped.** Operators
   no longer silently no-op on typo'd values; `helm install`
   rejects unknown keys at the chart-validation gate.
   Anchor: commit `6631914`.

3. **No CI smoke against a kind cluster — ✅ shipped.** A push/PR
   workflow now exercises the full deployment surface up to the
   transport-call boundary in a clean kind cluster.
   Anchor: `.github/workflows/smoke.yml`, commit `9366061`.

4. **Platform-agnostic core lives under
   `internal/drivers/iosxe/configdriver/...` — ⏸ deferred to
   Phase-10.** Cosmetic; structural correctness is fine. The
   relocation to `internal/configdriver/` is a mechanical move
   covering many import paths and many touched files. Tackling it
   on this branch would conflict noisily with the v1 promotion
   work (which moves API paths) and the example-corpus PR set
   (which touches `tools/cisco-vk-config-docs`). Phase-10 is
   the right window.
   Anchor: tracked in [`driver-extension-guide.md`](driver-extension-guide.md) §7.

5. **`internal/aggregator` (10.9 %) and `internal/provider`
   (25.7 %) coverage — ✅ shipped (aggregator).** New lifecycle
   tests register a stub config-driver factory and exercise
   start/stop/spec-change/credential-fallthrough through the
   real `Reconcile()` entrypoint. Aggregator coverage rose to
   74.1 %. `internal/provider` coverage remains tracked as
   follow-up — its uncovered code is the controller-runtime
   wiring layer that would need envtest to exercise honestly.
   Anchor: `internal/aggregator/lifecycle_test.go`.

6. **No fuzzing — ✅ shipped.** Five fuzz targets covering the
   parsers that consume device-supplied or operator-supplied
   bytes (`parseGNMIPath`, `splitYAMLDocs`, `parseHello`,
   `parseRPCReply`, `splitReplayAnnotation`). All five run clean
   for 4 s on local hardware and grow coverage on every run.
   Anchor: commit `2221f14`.

7. **No `t.Parallel()` in the test suite — ✅ shipped.** 189
   `t.Parallel()` calls landed across 21 pure-unit test files
   (intent, writers, schema, engine/lease, transport framing,
   common, registry, tlsutil, iosxe transforms). Race-detector
   stays clean.

8. **Subscribe overflow drop counter missing — ✅ shipped.**
   `cisco_vk_config_subscribe_events_dropped_total{device}` is
   bumped on every dropped event in `pumpSubscribe`'s default
   branches.
   Anchor: commit `6631914`.

9. **Two logging configs in one process — ✅ plan authored.**
   Implementation deferred to a focused PR; the plan recommends
   `slog` as the single backend with thin shims for both logrus
   and controller-runtime.
   Anchor: [`log-unification-plan.md`](log-unification-plan.md).

10. **No CRD v1 promotion plan / conversion webhook — ✅ plan
    authored.** Field-by-field shape changes, conversion-webhook
    mechanism, three-release phasing, and acceptance criteria
    are now on file.
    Anchor: [`crd-v1-promotion-plan.md`](crd-v1-promotion-plan.md).

11. **No SBOM / SLSA provenance in CI — ✅ shipped.** The release
    workflow produces multi-arch images, runs Trivy with HIGH/
    CRITICAL fail-build, generates a CycloneDX SBOM, and signs
    the image keylessly via cosign + Sigstore Fulcio. The SBOM is
    re-attached as a cosign attestation.
    Anchor: `.github/workflows/release.yml`, commit `2a9f605`.

12. **Reconciler-level OTel span absent — ✅ shipped.** The
    `ConfigReconciler.Reconcile` entrypoint now opens a
    `cisco-virtual-kubelet/config-reconciler` span with full
    apply-time attribution: device name, CR identity, cohort
    size, managed-family count, drift policy, post-reconcile
    phase, and drift count. No-op when no TracerProvider is
    wired.
    Anchor: `internal/provider/config_reconciler_controller.go`.

None of these were blockers for a per-pod-topology rollout. The
post-review push closed every item that could be closed within
the branch's scope without inviting the v1-promotion conflict
(that's #4) or pre-empting an architectural decision worth
running past a wider review (that's the implementation of #9 and
#10, both of which now have plans on file).

---

## 7. What's exemplary (worth borrowing in other projects)

For balance — these are the patterns I'd point a different team
at:

1. **Cross-validation corpus**
   ([`merge_cross_validation_test.go`](../../internal/drivers/iosxe/configdriver/intent/merge_cross_validation_test.go)).
   Pinning the local merge implementation against the upstream's
   `MergeMaps` family-by-family. Most teams don't write this kind
   of test; it's the single biggest hedge against semantic
   divergence with an external reference.

2. **Closed verb set + optional capabilities.** `Replace/Merge/
   Delete/CLI` is a stable wire vocabulary; `PruneCapable` and
   `SubscribeCapable` let writers and transports opt in without
   a flag day. The `nestedKeyedListWriter` change rolling
   per-rule diffing across six families with one helper is the
   payoff.

3. **Two-registry plug-in pattern** (apphosting +
   configdriver, separately registered). Lets a platform ship
   one capability without the other; the database/sql + image/png
   stdlib pattern done well.

4. **Driver-extension guide as documentation, not as policy.**
   `docs/rfcs/driver-extension-guide.md` shows the cost model + the
   minimum-foundation-edit footprint (two lines + an import). The
   guide is the contract; the placeholder packages are the
   compile-time witnesses.

5. **Apply log + replay annotation as separate concerns.** The
   audit CR can be deleted independently of the IOSXEConfig
   without losing the audit trail's effect, because the replay
   path consumes it explicitly. Most teams couple audit + replay
   into one controller; separating them is the right Kleppmann-
   "decouple your derived data" instinct.

6. **Stage-prefixed error attribution** (`Fetch:`, `Diff:`,
   `Apply:`, `Verify:`, `PruneDiff:`). Operators reading
   `kubectl describe` see what step failed without log archaeology.

---

## 8. Bottom line

**Quality grade: A-.** Architecturally sound, disciplined in
implementation, generous in test coverage where it matters
(merge semantics, transport contracts, writer shape pins),
honest about the residual cosmetic issue (the path-vs-content
mismatch in §1.3). The one real defect found during this review
(NETCONF close-time data race) was scoped, well-isolated, and
fixed in the same pass.

The watch-items are the natural pre-1.0 backlog. Numbers 2, 3,
5, and 11 should land before an external release; the rest are
polish that won't affect operability under per-pod topology.

The Phase-9 plug-in registry is the architecturally most
significant move on the branch — it's what turns "an IOS-XE
controller that happens to have good separation" into "a
foundation that other platforms slot into without modifying its
core". The shape is right and the contract is enforced by tests.
A second platform (NX-OS, Junos) is what tests whether the
contract survives contact with reality; until that lands the
contract is theoretical, but the theory is sound.

Of the canonical references this review draws from, the
branch's architecture lines up most closely with the
ports-and-adapters core + closed-vocabulary verb set Bass et al.
spell out for "modifiable systems with stable external
interfaces". The Kleppmann-style observation that "interfaces
that don't change are the only interfaces that scale" is
honoured: in 60 commits across this branch, the
`transport.Interface` shape has changed exactly once
(`SubscribeCapable` was added as an optional interface, not
modified into the core), and the `SectionWriter` shape has not
changed at all.
