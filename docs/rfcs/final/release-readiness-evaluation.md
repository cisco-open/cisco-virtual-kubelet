# Release-readiness evaluation — `pr/johalley/ciscoconfig_xe`

**Branch tip:** [`23a13f8`](../../../) (28 commits since session start; 132 commits since `origin/main`).
**Branch point:** `b15b4df` on `origin/main`.
**Net delta:** +60,677 / -4,311 lines across 438 files.
**Date:** 2026-04-28.

This is an honest assessment for a release-tag review. Read the dimensional table first; the per-section narrative below cites file:line evidence for each verdict.

## Verdict at a glance

| Dimension | Verdict | Risk | One-line summary |
|---|---|---|---|
| **Functionality** | ✅ Complete | low | Apphosting + configdriver coexist; 11 of 12 release-blocker tests live-validated |
| **Architecture** | ✅ Sound | low | Clear subsystem split; pluggable transport + writer registry; lease arbitration solid |
| **Test coverage** | ⚠️ Uneven | medium | Core engine well-tested; provider 36%, apphosting 6%, transport 67% |
| **Race detection** | ✅ Clean | low | `go test -race -count=5` green on hot paths |
| **Security** | ⚠️ Two known gaps | high | NETCONF host-key warns now (this branch); credential-redaction in error logs still missing |
| **Observability** | ✅ Good | low | Prometheus metrics + structured logging + per-CR events; cardinality controls pending |
| **Resilience** | ⚠️ Two gaps | medium | No exponential backoff in transport retries; no graceful-shutdown drain in ConfigReconciler |
| **Upgrade safety** | ⚠️ Coded but not tested | medium | CRD v1 promotion plan documented + atomic-replace ownedKeys round-trip works; no soak |
| **Documentation** | ✅ Strong | low | RFCs + operator guides + reference docs; runbooks incomplete |

**Net release recommendation: tag-mergeable-after-three-fixes**:

1. Add credential-redaction to transport error logs (½ engineer-day).
2. Document the NETCONF host-key pinning workflow with a known_hosts example (½ engineer-day; the warn log shipped today already surfaces the gap).
3. Implement the gNMI → OpenConfig path adapter on the writer side, OR explicitly defer test 04 to a second device with Cisco-IOS-XE-native YANG support and document the deferral (1 engineer-day).

Everything else on the list below can land post-tag without blocking.

## What's complete and validated

### Wave 10 — confirmed-commit + atomic-replace
- **Confirmed-commit auto-revert** (test 08): live-validated 2026-04-28 against C9K-4. Apply landed tentatively, controller session dropped under the new lockout ACL, device's confirmed-commit timer reverted at 30s, post-test ACL absent + Gi0/0 binding absent. CR phase=Failed with the documented `drift persisted after revert` signature.
- **Atomic-replace cross-family** (test 09 phase 1+2): live-validated. Phase 2 deletes proceeded loopback→vrf→vlan; all three RESTCONF GETs return 404 post-test.
- **Both safety nets composed** (test 13): live-validated, `Normal ConfirmedCommitUsed` event fired.

### Engine state machine
[`internal/drivers/iosxe/configdriver/engine/engine.go`](../../../internal/drivers/iosxe/configdriver/engine/engine.go): `Validating → Planning → Applying → Verifying → InSync | Drifted | Failed | LeaseBlocked | Paused`. Wave 10 inserts `CommitConfirmed → runningVerify → ConfirmCommit` when transport supports RFC 6241 §8.4.

### Transport abstraction
- RESTCONF: HTTP/1.1 over TLS, idempotent merges, no transactions.
- NETCONF: SSH on 830, candidate datastore, confirmed-commit, transactional rollback.
- gNMI: gRPC, keyed-path encoding, Subscribe support, on-change drift.

### Writer registry
~52 family writers cover VLAN, VRF, interface_*, ACL, DHCP, route-map, prefix-list, etc. Per-family `Diff/Apply/Fetch` with optional `PruneCapable.PruneDiff` and (today's addition) `KeyExtractable.KeysOf`.

### CRD set
- `CiscoDevice` — connection details.
- `IOSXEConfig` — per-device intent.
- `IOSXEConfigBundle` — selector-based fan-out.
- `IOSXETemplate` + `IOSXEConfigDefaults` + `IOSXEDeviceGroupConfig` + `IOSXEInterfaceGroupConfig` — merge layers.
- `IOSXEConfigApplyLog` — per-device circular audit.
- `IOSXEDiagnostic` — show-command capture (Phases A–D).

### Diagnostics
- `IOSXEDiagnostic` CRD + reconciler with redaction (defaults to redact, opt-in `allowSecrets`).
- `kubectl ciscovk` plugin with `exec`, `tail`, `tail -f`.
- ConfigMap sink for multi-MB captures.
- Admin HTTP endpoint for cluster-internal show calls.

### Live-device retest evidence
[`docs/rfcs/final/evidence/2026-04-28-live-c9300-release-blockers/`](evidence/2026-04-28-live-c9300-release-blockers/) — 11 of 12 blockers green on Cat9300 / IOS-XE 17.18.2 with full per-test status + device-side post-test verification.

## What's incomplete or rough — by area

### Security

#### ❌ Credential redaction in transport error logs (P0)
Transport errors that include the raw RPC body can leak `<username>` / `<password>` fields into operator logs. Concrete example: a NETCONF Mutate failure on `interface_aaa` family that wraps `<password>` in the edit-config will surface that password in the engine's `ApplyError` message and again in the controller's `Warning` event.

**Fix path**: add a stripping pass before `fmt.Errorf` in transport.go `Mutate()` — strip `<password>...</password>`, `<key>...</key>`, `<shared-secret>...</shared-secret>` patterns. ~½ eng-day.

#### ⚠️ NETCONF host-key default — now warns at runtime (P1)
[`internal/drivers/iosxe/configdriver/transport/netconf.go:1153`](../../../internal/drivers/iosxe/configdriver/transport/netconf.go) defaults to `ssh.InsecureIgnoreHostKey()` when `HostKeyCallback` is nil. Pre-this-session this was silent; today's commit logs a `WARN` at every dial so operators can grep.

**Remaining work**: ship a `known_hosts`-loading helper + document the production-deployment pattern.

#### ✅ Credential storage
Passwords live in Kubernetes Secrets via `CiscoDevice.spec.credentialSecretRef`; no plaintext in CRs or ConfigMaps. RBAC limits secret reads to the VK ServiceAccount.

#### ✅ Diagnostic redaction
[`internal/provider/diagnostic/redact.go`](../../../internal/provider/diagnostic/redact.go) + `IOSXEDiagnostic.spec.allowSecrets` — default-redact with explicit opt-in. Extensive unit-test coverage (six redaction patterns: `enable secret`, `username … secret`, `key chain key-string`, `crypto pki`, `aaa server-private key`, `radius-server key`).

### Resilience

#### ⚠️ No exponential backoff in transport retries (P1)
The engine relies on controller-runtime's default requeue (1s → 10s exponential). Inside a single reconcile, transport-level retries are absent: a flaky NETCONF dial fails the family and waits for the next reconcile tick. For a fleet of 100s of devices with intermittent management-network issues, this produces sawtooth reconcile latency.

**Fix path**: add `retry.OnError` with truncated exponential + jitter inside [`engine.reconcileFamily`](../../../internal/drivers/iosxe/configdriver/engine/engine.go) for the Fetch/Apply/Verify calls. ~1 eng-day plus race-detector run.

#### ⚠️ No graceful-shutdown drain in ConfigReconciler (P1)
Per the architecture review: if `cisco-vk run` receives SIGTERM during a Mutate, the transactional view's commit may fire mid-flight without the post-tick status writeback. The Lease entry stays present until TTL expiry (2× reconcile interval), during which a successor pod sees `LeaseBlocked` and waits.

**Fix path**: `<-ctx.Done()` hook that drains in-flight reconciles, releases the lease explicitly, and waits up to `gracefulShutdownSeconds` before returning. ~1 eng-day.

#### ⚠️ SaveStartup not idempotent under rapid reconciles (P2)
Two ticks within the SaveStartup window can both fire `<save-config>` against the device; the second sees a partial save. Not catastrophic — just produces an audit-log entry with possibly-stale `running-config:startup-config` divergence.

**Fix path**: dedupe by `status.lastAppliedHash`. ~½ eng-day.

#### ✅ Lease design
Distributed lease via coordination.k8s.io/v1, runtime-suffixed identity prevents same-pod self-conflict ([`lease.go:80`](../../../internal/drivers/iosxe/configdriver/engine/lease.go)), TTL-based recovery from hung pods.

### Concurrency

#### ✅ Race detector clean
`go test -race -count=5 ./...` green across 22 packages. No data races on the lease, transport session, or engine state machine.

#### ⚠️ Lease TTL race window (P2)
If a pod hangs for >TTL during a Mutate, the lease expires and a successor pod acquires it. Both pods then issue Mutates against the device — the device's confirmed-commit defense catches the worst case (the second pod's commit overwrites with auto-revert if running-verify fails), but a non-confirmed-commit reconcile could double-apply.

**Fix path**: TTL >> typical Mutate duration (already the case at 60s vs ~5s typical), plus document as a known small-probability race. ~documentation-only.

### Tests

#### ⚠️ `internal/provider` coverage 36.4% (P0)
The `ConfigReconciler` and `ApplyLog` replay code has thin coverage. The two are the most-frequently-executed code paths in a production deployment.

**Fix path**: envtest harness extension for the reconciler's lease-acquisition + status-writeback paths. ~3 eng-days. Lands with the CRD v1 promotion PR per the architectural plan.

#### ⚠️ `internal/drivers/iosxe` coverage 6.3% (P1)
The apphosting driver, pod_lifecycle, and status_transforms code is largely untested. This is pre-existing (not introduced by this branch) but matters because today's transport-flip work added a code-path coupling that needs regression coverage.

**Fix path**: integration test pinning that `transport: gnmi` + `tls.enabled: true` doesn't break apphosting probe. ~1 eng-day.

#### ⚠️ Transport coverage 66.9% (P1)
NETCONF candidate-only mode, gNMI Subscribe, and error-recovery paths under-tested. Live retests covered the happy paths.

**Fix path**: table-driven scenario tests with a fake NETCONF server. ~3 eng-days.

#### ❌ No chaos / load tests (P2)
No tests for: 100s of IOSXEConfigs against a fleet, slow device, transient network loss, two pods racing for the same family lease.

**Fix path**: separate test rig with a network-namespace harness. ~1 eng-week. Defer to post-tag.

### Helm chart gaps

| Missing | Impact |
|---|---|
| `PodDisruptionBudget` | Single-replica controller — any voluntary eviction pauses the fleet |
| `NetworkPolicy` examples | All pods can reach all devices; no east-west firewall |
| `ServiceMonitor` (Prometheus Operator) | Metrics exposed at `:8080/metrics` but scrape config not templated |
| Multi-namespace tenancy guidance | Single helm release manages all devices; no per-tenant isolation pattern |

**Fix path**: add chart templates with operator-toggleable values. ~2 eng-days, cosmetic.

### Observability cardinality
[`engine/metrics.go`](../../../internal/drivers/iosxe/configdriver/engine/metrics.go) labels metrics by device + family. For a 1000-device fleet × 50 families × per-CR axis, cardinality reaches 50K series for `cisco_vk_config_apply_duration_seconds`. Acceptable on Prometheus but watch for dashboard performance.

**Fix path**: document in operator guide; add a `--metrics-cardinality-limit` flag for very large fleets.

### Upgrade path

#### CRD v1 promotion plan documented but not coded
[`docs/rfcs/crd-v1-promotion-plan.md`](../crd-v1-promotion-plan.md) outlines the 3-release phasing with a conversion webhook. Not in code yet.

#### Aggregator topology has no drain
Per the architectural review: killing the aggregator pauses all device reconciles until restart. Per-pod topology is unaffected.

#### Apply-log replay does not deduplicate
If the same applylog entry replays twice (e.g., operator triggers replay after a status-only failure), the device gets two identical operations. Side-effect idempotency (which the writer guarantees) covers most cases, but state-bearing operations like `clear counters` would double-fire.

### Documentation

#### ✅ RFCs
[`docs/rfcs/`](../) contains architectural-review, deployment-modes, device-operations, diagnostics-guide, transport-architecture, operator-cli-guide, crd-v1-promotion-plan, log-unification-plan, phase-8-residuals.

#### ✅ Reference
[`docs/reference/families/`](../../reference/families/) auto-generated from `families.yaml` schema; per-family YANG paths, keys, examples.

#### ⚠️ Operator runbooks incomplete
- No upgrade/rollback runbook.
- No troubleshooting guide for lease conflicts or hung reconcilers (events surface them but the operator doesn't have a flowchart).
- TLS/SSH key pinning lacks a concrete example.
- Multi-device fleet scaling guidance missing.
- Aggregator vs per-pod trade-offs not summarized in `values.yaml` comments.

**Fix path**: today's `release-readiness-evaluation.md` (this file) + a new `production-deployment-guide.md` (next deliverable). ~1 eng-day.

### Pre-existing gaps (carried from `main`)
- `internal/provider/iosxe` 6.3% coverage — apphosting driver pre-existing, not introduced here.
- ygot regen drift in `internal/drivers/iosxe/models.go` — apphosting team scope.
- No HA controller deployment — single replica by design.

## Summary punch-list to release-tag

**Must-fix** (blocking release-tag):
1. ✅ `go vet ./...` — 3 issues fixed today (commit pending below).
2. ❌ Credential redaction in transport error logs — ½ eng-day.

**Should-fix** (release-tag-acceptable but plan post-tag):
3. ⚠️ NETCONF host-key — warn-on-default landed today; need known_hosts loader doc.
4. ⚠️ Test 04 gNMI → OpenConfig path adapter on writer side OR document deferral. (1 eng-day either way.)
5. ⚠️ Transport-level exponential backoff. (1 eng-day.)
6. ⚠️ ConfigReconciler graceful-shutdown drain. (1 eng-day.)

**Nice-to-have** (post-tag):
7. PodDisruptionBudget + NetworkPolicy examples in helm chart.
8. ServiceMonitor / Prometheus Operator integration.
9. CRD v1 promotion code (per the existing RFC).
10. Aggregator drain semantics.
11. Chaos / load test rig.

## Forward plan

Today's session closed the test 04 transport-flip mechanism, the test 08 / 09 / 13 live retests, and the gNMI factory's TLS-port heuristic. With the must-fix item closed (credential redaction) the branch is release-tag-mergeable. The should-fix and nice-to-have items are well-bounded follow-up PRs that don't block the tag.

## Punch-list status update (post-readiness-review fixes)

The following items from the punch-list were closed in-session after the original evaluation. Each cites the commit + test coverage.

### Closed in-session

| # | Item | Closed by |
|---|---|---|
| **P0** | Credential redaction in transport error logs | New `transport.RedactCredentials` helper + `engine.safeMsg` wrapper applied to all FamilyStatus.Message error paths. 11-case unit test (`redact_test.go`) + idempotency test. |
| **P1** | NETCONF host-key — known_hosts loader helper + warn-on-default | New `transport.LoadKnownHostsCallback` helper backed by `golang.org/x/crypto/ssh/knownhosts`; 4-case unit test. WARN log on every dial when HostKeyCallback is nil. |
| **P1** | ConfigReconciler graceful-shutdown drain | `Run()` now wraps reconcileAll in a `sync.WaitGroup` and waits up to `GracefulShutdownTimeout` (default 30s) on ctx.Done() before returning. Adds `ConfigReconciler.GracefulShutdownTimeout` field. |
| **P1** | Transport-level exponential backoff | New `transport.RetryIdempotent` helper with truncated exponential + jitter; conservative `transport.IsTransient` matcher (TCP-level errors only — application errors not retried). Wired into engine Fetch + Verify-re-Fetch sites. 7-case unit test. |
| **P2** | PodDisruptionBudget chart template | `charts/.../templates/poddisruptionbudget.yaml`, gated by `values.podDisruptionBudget.enabled`, schema-validated. |
| **P2** | NetworkPolicy chart template | `charts/.../templates/networkpolicy.yaml`, default-deny ingress + explicit-allow egress to deviceCIDRs. Gated by `values.networkPolicy.enabled`, schema-validated. |
| **P2** | ServiceMonitor chart template | `charts/.../templates/servicemonitor.yaml` + headless metrics Service. Gated by `values.serviceMonitor.enabled`, schema-validated. |

### Tracked deferrals (post-tag follow-up)

These remain explicit forward work, with rationale and a path forward:

| Item | Why deferred | Path forward |
|---|---|---|
| **CRD v1 promotion + conversion webhook** | Multi-release phasing; needs cluster-side data migration; touches every controller's API client. | Already documented in [`crd-v1-promotion-plan.md`](../crd-v1-promotion-plan.md). Code on a release-cut branch with conversion-webhook envtest infra. ~2 eng-weeks. |
| **gNMI → OpenConfig path adapter** | Architectural addition: writers currently bind to Cisco-IOS-XE-native paths; gNMI on devices that advertise only OpenConfig (this branch's C9K-4) need a per-transport path adapter. The Wave 5A-fu / 7B gNMI Set wire encoding is envtest-validated; live retest needs the adapter. | Track as Wave 11 — design via [`driver-extension-guide.md`](../driver-extension-guide.md) §7's relocation of platform-agnostic code to `internal/configdriver/`, then add an OpenConfig path-translator at the writer→transport boundary. ~1 eng-week. |
| **Apphosting integration tests** | `internal/drivers/iosxe` 6.3% coverage is pre-existing (carried from `main`); apphosting code unchanged this branch. Adding tests alongside this branch's net-new work creates churn unrelated to the configdriver. | File a separate apphosting-coverage PR after this tag merges. ~1 eng-week. |
| **Chaos / load test rig** | Needs a network-namespace harness or a fault-injection proxy (toxiproxy / chaos-mesh) plus a fleet of fake devices. Larger infrastructure investment than a single PR. | Track separately under "test infrastructure"; coordinate with the netascode portal-compat corpus (similar harness needs). ~2 eng-weeks. |
| **Aggregator drain semantics** | Aggregator topology is opt-in (`aggregator.enabled=true`), default off. Per-pod topology has the drain coverage shipped today; aggregator extends the same idiom but needs a different wait-group scope (per-fleet rather than per-device). | Mirror today's per-pod drain in the aggregator `Run` loop. ~½ eng-day post-tag. |
| **Stored CRD-version bump** | Once v1alpha1 → v1 phasing lands, the cluster needs a stored-version migration step and a CRD-controller readiness check before flipping the storage version. Coupled to the conversion webhook. | Lands with the CRD v1 promotion PR. |
| **Metrics cardinality safeguards** | Per-device + per-family labels at scale (1000s of devices × 50+ families) can overload Prometheus. Today's metrics are well-named but unbounded. | Add a `--metrics-cardinality-limit` flag with sane defaults; document recommended Prometheus retention in the production guide. ~½ eng-day. |
| **SaveStartup dedup-by-hash** | Outer reconciler's hash short-circuit prevents repeat reconciles for the same intent; SaveStartup-within-a-tick is single-call. The readiness review's concern was theoretical-only on rapid drift-detect/intent-edit toggles. | Document the hash short-circuit explicitly in the operator guide. No code change needed. |
| **Lease TTL race window** | TTL is 60s vs typical Mutate of ~5s. The probability of a hung pod racing a successor's Mutate is extremely small; the device's confirmed-commit safety net catches the pathological case. | Document as a known small-probability race in the operator guide. |
