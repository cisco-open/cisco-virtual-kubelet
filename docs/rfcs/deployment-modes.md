# Deployment modes — RESTCONF, NETCONF, gNMI

**Audience:** operators deploying `cisco-virtual-kubelet` against Cisco IOS-XE devices. Maintainers wanting the architectural reference should read [`./transport-architecture.md`](./transport-architecture.md) instead.

This document is the single setup guide for choosing and operating each transport. It covers device-side prereqs, CRD configuration, expected metrics/events, and the per-mode pitfalls surfaced by recent live-device validation.

**Tested against:** Cat9300-24P / IOS-XE 17.18.2 (cat9k-smoke device, 2026-04-27 evidence bundle).
**Last validated:** branch `pr/johalley/ciscoconfig_xe`, image tag `phase9-crb-fix` (= v29 = commit `4b0ef79`).

---

## 1. Quick start — choosing a mode

| You want… | Use this mode |
|---|---|
| Minimal device configuration; stateless writes; no transactional safety nets | **RESTCONF** |
| Multi-family atomic apply (one transaction commits or none of it does), candidate datastore, confirmed-commit auto-revert | **NETCONF candidate-only** |
| Telemetry-driven drift detection with on-change subscriptions; structured paths for interface names with `/` | **gNMI** |
| Both NETCONF safety nets AND gNMI subscribe | **NETCONF + gNMI on separate CRs** (single transport per CiscoDevice today; per-family transport override is on the forward work list) |

Defaults per transport: RESTCONF picks port 443, NETCONF picks 830, gNMI picks 50052. The transport-aware factory ignores carry-over values like `spec.port: 443` when you switch a CR to a different transport — see commit `b2c1189` for the rationale.

---

## 2. Common prerequisites

### 2.1 Cluster-side

- A running Kubernetes cluster (kind, k3s, EKS, etc.) with the cisco-vk operator's CRDs applied.
- The operator manager (`cisco-vk manager`) running with `--vk-image=<container-registry-tag>`.
- A Secret containing the device password under key `password`.
- A `CiscoDevice` CR pointed at that Secret via `spec.credentialSecretRef`.

### 2.2 Device-side (general)

- Local user with privilege 15 on the device. The cisco-vk pod authenticates as this user for all transports.
- A management-plane interface reachable from the cluster network (verify with `kubectl exec ... -- nc -zv <device> <port>` if needed).
- Time sync recommended (cosmetic for log correlation, not required for correctness).

### 2.3 CiscoDevice CR template

```yaml
apiVersion: cisco.vk/v1alpha1
kind: CiscoDevice
metadata:
  name: cat9k-smoke
  namespace: cisco-vk-smoke
spec:
  address: 10.1.1.1
  driver: XE
  username: cisco
  credentialSecretRef:
    name: cat9k-creds
  tls:
    enabled: true
    insecureSkipVerify: true     # for lab; prefer a CA-signed cert in prod
  transport: restconf            # see §3, §4, §5 for mode-specific knobs
  port: 443                      # see the transport-aware default rules
  region: lab
  zone: smoke
```

`transport` and `port` are the only fields that change per mode. Every other field is mode-agnostic.

---

## 3. RESTCONF mode

The simplest and most-conservative deployment. Production-default if you don't need transactional / confirmed-commit / atomic-replace.

### 3.1 Device-side prereqs

```
ip http secure-server
restconf
```

Verify:
```
show running-config | include restconf
show platform software yang-management process | include nginx|confd
```

### 3.2 CR config

```yaml
spec:
  transport: restconf
  port: 443                # default; can omit
  tls:
    enabled: true
```

### 3.3 What's available

| Feature | RESTCONF |
|---|---|
| Per-family writes | ✅ |
| Drift detection (report / revert) | ✅ |
| `writeStartup: true` save-startup | ✅ via `/restconf/operations/cisco-ia:save-config` |
| Multi-family atomic apply | ❌ — engine writes per-family in dependency order; partial-drift possible if a later family fails |
| `spec.confirmTimeoutSeconds` | ❌ — engine emits `ConfirmedCommitFallback` Warning event with reason `transport does not implement ConfirmedCommitter` |
| `spec.atomicReplace: true` | ❌ — same fallback semantics |
| Telemetry subscribe | ❌ |

### 3.4 Expected metrics

```
cisco_vk_config_mutate_ops_total{transport="restconf",verb="REPLACE"}  ≥ 0
cisco_vk_config_mutate_ops_total{transport="restconf",verb="MERGE"}    ≥ 0
cisco_vk_config_mutate_ops_total{transport="restconf",verb="DELETE"}   ≥ 0
cisco_vk_config_save_startup_total{outcome="ok",transport="restconf"}  ≥ 1   (when writeStartup=true)
```

### 3.5 Common errors

| Symptom | Likely cause |
|---|---|
| `tls: first record does not look like a TLS handshake` | RESTCONF dial hit a non-HTTPS port. Verify `spec.tls.enabled: true` and the device has `ip http secure-server`. |
| `404 on /restconf/operations/cisco-ia:save-config` | Pre-fix image (before commit `61566dc`). Upgrade. |
| `403 forbidden` | User lacks privilege 15, or AAA misconfigured on the device. |

---

## 4. NETCONF mode

Two sub-modes share the same `spec.transport: netconf`:

- **Writable-running mode.** Device advertises `:writable-running:1.0` AND `:candidate:1.0`. `spec.transactional: false` writes directly to running. `spec.transactional: true` uses the candidate datastore.
- **Candidate-only mode.** Device advertises `:candidate:1.0` but NOT `:writable-running:1.0` — i.e. operator enabled `netconf-yang feature candidate-datastore`. The transport detects this at hello time and the implicit-tx auto-promote path wraps non-transactional Mutate calls in `lock(candidate)` + `commit` + `unlock`. **Production-validated as of 2026-04-27.**

Candidate-only is the recommended mode if you want Wave-10 features.

### 4.1 Device-side prereqs

Mandatory:
```
netconf-yang
```

For candidate-only mode (Wave-10 features):
```
netconf-yang feature candidate-datastore
```

The `feature candidate-datastore` line triggers a NETCONF service restart (~30 s). Operators must schedule this — the cisco-vk pod will see `connection refused` on port 830 during the restart.

Verify:
```
show platform software yang-management process | include confd|ncsshd
show running-config | include netconf-yang
```

`confd` and `ncsshd` should be `Running`.

### 4.2 CR config

```yaml
spec:
  transport: netconf
  port: 830                          # default; can omit
  # tls is a NO-OP for NETCONF — SSH transport ignores TLS settings.
  # transactional + writeStartup + driftPolicy are independent of mode.

# Per-CR (IOSXEConfig) opt-ins:
# transactional: true       — use candidate explicitly via StartTransaction/Commit
# confirmTimeoutSeconds: 30 — Wave 10.2 auto-revert (see §6)
# atomicReplace: true       — Wave 10.3 single-transaction apply across families
```

**Port-default note.** The transport factory treats `spec.port: 0`, `80`, or `443` as "not a NETCONF intent" and falls back to 830. So a CiscoDevice that previously ran RESTCONF with `port: 443` and is flipped to `transport: netconf` works without editing `port`.

### 4.3 What's available

| Feature | NETCONF (writable-running) | NETCONF (candidate-only) |
|---|---|---|
| Per-family writes | ✅ direct to running | ✅ via implicit-tx |
| `spec.transactional: true` | ✅ candidate + commit | ✅ candidate + commit |
| `writeStartup: true` save-startup | ✅ Cisco-IA RPC | ✅ Cisco-IA RPC |
| Multi-family atomic apply | ✅ when `transactional: true` | ✅ implicit-tx wraps batch |
| `spec.confirmTimeoutSeconds` | ✅ when device advertises `:confirmed-commit:1.0` | ✅ same |
| `spec.atomicReplace: true` | ✅ | ✅ |
| Telemetry subscribe | ❌ | ❌ |

### 4.4 Expected metrics

```
cisco_vk_config_mutate_ops_total{transport="netconf",verb="MERGE"}      ≥ 1
cisco_vk_config_transactions_total{outcome="commit",transport="netconf"}     ≥ 1
cisco_vk_config_transactions_total{outcome="start_failed",transport="netconf"}  ≥ 0  (occasional, e.g. parallel RESTCONF lock)
cisco_vk_config_save_startup_total{outcome="ok",transport="netconf"}    ≥ 1   (when writeStartup=true)
```

`start_failed` ticking up under sustained load typically means another session (RESTCONF, gNMI, or another NETCONF client) is holding the candidate lock. The engine's reconcile-retry path absorbs transient races without operator action.

### 4.5 Common errors

| Symptom | Likely cause |
|---|---|
| `connection refused` on `:830` | `netconf-yang` not enabled on device, or `confd` is down. Run `show platform software yang-management process`. |
| `rpc-error: Unsupported capability :writable-running` | Pre-fix image (before commit `9e08b07`) on a candidate-only device. Upgrade. |
| `rpc-error: lock-denied (info=<session-id>N)` | Another session holds candidate. Transient if N matches a known parallel client; reconcile retry absorbs it. Persistent → operator should `clear configuration lock` on device. |
| `rpc-error: unknown-element <bad-element>banner</bad-element>` | Pre-fix image (before commit `d27016d`). Upgrade. |
| `rpc-error: unknown-element <bad-element>Loopback=9997</bad-element>` | Pre-fix image (before commit `88ac685`). Upgrade. |
| Phase=Failed with "1 op(s) applied but 1 still pending" repeatedly on NETCONF | Pre-fix image (before commit `88ac685`) — Fetch shape mismatch. Upgrade. |

### 4.6 RESTCONF coexistence on the same device

NETCONF + RESTCONF share `<candidate/>` lock arbitration, so a CRD-managed cisco-vk pod and an out-of-band RESTCONF session (e.g. operator CLI tooling) WILL race. Symptoms: intermittent `start_failed` transactions metric increments and `lock-denied` rpc-errors. Two ways to address:

1. **Avoid concurrent management.** Quiesce out-of-band sessions during the maintenance window.
2. **Accept the race.** The implicit-tx retry path absorbs transient denials; the engine emits `ApplyFailed` Warning events for each lock-denied so operators see them but the next reconcile typically wins.

---

## 5. gNMI mode

Used primarily for telemetry-driven drift detection and for keyed paths whose values contain `/` (e.g. interface names like `GigabitEthernet 0/0/0`).

### 5.1 Device-side prereqs

```
gnxi
gnxi server
```

`gnxi` (replaces the older `gnmi-yang` on IOS-XE 17.18+) listens insecure on port 50052 by default. The cisco-vk pod authenticates with HTTP-Basic auth in gRPC metadata.

For TLS-secured gNMI:
```
gnxi secure-init
gnxi secure-server
```

…then `spec.tls.enabled: true` on the CiscoDevice. (Default port for the secure listener is 50051; check `show running-config | include gnxi`.)

Verify:
```
show platform software yang-management process | include gnmib
```

### 5.2 CR config

```yaml
spec:
  transport: gnmi
  port: 50052                # IOS-XE 17.18+ insecure default
  tls:
    enabled: false           # set true if using gnxi secure-server
```

For older `gnmi-yang` builds on 50051, set `port` explicitly.

### 5.3 What's available

| Feature | gNMI |
|---|---|
| Per-family writes | ✅ |
| `spec.transactional: true` | ✅ — single SetRequest with all updates |
| `writeStartup: true` save-startup | ❌ — returns ErrUnsupported. Engine falls back to skipping the save. |
| Multi-family atomic apply | ✅ |
| `spec.confirmTimeoutSeconds` | ❌ — `ConfirmedCommitFallback` event (Cisco hasn't shipped gNMI confirmed-commit) |
| Telemetry subscribe | ✅ ON_CHANGE / SAMPLE |
| Keyed paths with `/` in values | ✅ — `pathSpecForInterface` preserves the value verbatim |

### 5.4 Expected metrics

```
cisco_vk_config_mutate_ops_total{transport="gnmi",verb="REPLACE"}  ≥ 0
cisco_vk_config_mutate_ops_total{transport="gnmi",verb="MERGE"}    ≥ 0
cisco_vk_config_transactions_total{outcome="commit",transport="gnmi"} ≥ 0
```

Note: gNMI's "transaction" is in-memory accumulation flushed by `Commit`, so the `transactions_total` counter is bumped on each `Commit` call. `outcome="start_failed"` is rare on gNMI (no remote lock involved).

### 5.5 Common errors

| Symptom | Likely cause |
|---|---|
| `connection refused` on `:50052` | `gnxi server` not enabled, or running on a different port (older `gnmi-yang` builds). Set `spec.port` explicitly. |
| `Unauthenticated` gRPC error | Wrong username/password or AAA failure. |
| `tls handshake failure` | `gnxi secure-server` not enabled but `spec.tls.enabled: true`. |
| Element not found for keyed path | Pre-fix image (before Wave 5A-fu, commit `c414a51`). Upgrade. |

---

## 6. Wave-10 features (NETCONF + gNMI)

Two opt-in safety nets on `IOSXEConfig`. Available when `spec.transactional: true` AND the transport advertises the matching capability.

### 6.1 `spec.confirmTimeoutSeconds`

Engine path: `CommitConfirmed(timeout) → runningVerify → ConfirmCommit | (omit ConfirmCommit + Discard → device auto-reverts at timeout)`.

Use case: protect against management-plane-breaking applies (test 08 in the runbook is the canonical example — a deliberately-bad ACL that severs the controller's session). Without `ConfirmCommit`, the device reverts to the previous committed state at `confirmTimeoutSeconds` and the controller can reconnect.

```yaml
spec:
  transactional: true
  confirmTimeoutSeconds: 30        # 1–600 seconds, clamped at transport boundary
  managedFamilies: [access_list_extended]
  source:
    inline:
      access_list_extended: ...
```

Events to expect on `kubectl describe iosxeconfig`:
- `Normal ConfirmedCommitUsed` — confirmed-commit happy path
- `Warning ConfirmedCommitFallback reason=...` — fallback case (see §6.3)

### 6.2 `spec.atomicReplace: true`

Engine treats the resolved intent as authoritative for the managed families: every add + delete + update lands in **one** transaction with cross-family `depends_on` ordering from `schema/families.yaml`. Used when partial-drift between families is intolerable (e.g. removing a VRF and the interfaces in it must happen together).

```yaml
spec:
  transactional: true
  atomicReplace: true
  managedFamilies: [vlan, vrf, interface_loopback]
  source: ...
```

### 6.3 Composing the two

`atomicReplace: true` + `confirmTimeoutSeconds: 30` is the recommended-default for production CRs that touch routing or interfaces. Test 13 is the live-device proof.

### 6.4 CRD bump prerequisite

Both fields require the chart's CRD at [`charts/cisco-virtual-kubelet/crds/config.cisco.vk_iosxeconfigs.yaml`](../../charts/cisco-virtual-kubelet/crds/config.cisco.vk_iosxeconfigs.yaml). Older clusters with the pre-Wave-10 CRD reject CRs with `unknown field "spec.confirmTimeoutSeconds"`. Operator-scheduled apply unblocks tests 08/09/10/11/13:

```
kubectl apply -f charts/cisco-virtual-kubelet/crds/config.cisco.vk_iosxeconfigs.yaml
```

---

## 7. Per-pod kubelet vs aggregator-mode topology

Two deployment topologies; the transport choice is identical in both.

### 7.1 Per-pod kubelet (recommended)

One `cisco-vk run` pod per `CiscoDevice`. The operator manager creates a `Deployment` for each CiscoDevice; the pod runs both the apphosting kubelet and the configuration reconciler. Transport selection happens once at pod startup via the deferred-dial pattern (`internal/provider/config_reconciler.go`):

1. Reconciler is constructed with a nil transport.
2. A background goroutine retries `transport.For(spec, password, opts)` every 30 s until the dial succeeds.
3. On success, the transport is hot-swapped into the reconciler via `SetTransport` (`atomic.Pointer`).
4. Subsequent reconciles read the live transport via `GetTransport`.

This is the topology used by the live-device evidence bundles. **It is the recommended deployment construct as of 2026-04-27.**

### 7.2 Aggregator-mode (corner-case pattern)

Single `cisco-vk manager` process with `AggregatedReconciler` watching all CiscoDevices and spawning one `deviceWorker` goroutine per device. Same `transport.For` factory; no per-pod deferred-dial.

Use cases:
- Air-gapped operator environments where running a per-device pod is operationally awkward.
- Bench-test rigs validating the engine without container overhead.

Not recommended for general production. Tests 02 + 06 + 07 should still pass over both topologies; the live-device evidence bundle on this branch covers per-pod only.

---

## 8. Migrating between transports

Switching `spec.transport` on a live `CiscoDevice` triggers a controller-driven ConfigMap regeneration; the kubelet pod must be restarted to pick up the new transport. Today that means deleting the per-device pod (`kubectl delete pod -n <ns> -l app.kubernetes.io/instance=<device>`).

In-flight reconciles drain cleanly because the controller-runtime managers in the per-pod kubelet handle SIGTERM + lease release; the new pod starts in deferred-dial state and converges on the new transport.

Pitfall: the deferred-dial loop terminates as soon as ANY transport dial succeeds. If you flip transport back and forth quickly, the pod may settle on the wrong one. Cleanest path: make one change, wait for `phase: Ready`, verify metrics show `transport="<new>"` labels, then continue.

---

## 9. Live-device evidence bundles

Cross-references for "what's been validated" per mode:

| Bundle | Modes covered |
|---|---|
| [`final/evidence/2026-04-27-live-c9300-v12-production-ready/`](./final/evidence/2026-04-27-live-c9300-v12-production-ready/) | RESTCONF: tests 02, 06, 07, 11 PASS |
| [`final/evidence/2026-04-27-live-c9300-netconf-candidate-only/`](./final/evidence/2026-04-27-live-c9300-netconf-candidate-only/) | NETCONF candidate-only: tests 06, 07 PASS |
| [`final/evidence/2026-04-27-live-c9300-netconf-probe-tier1/`](./final/evidence/2026-04-27-live-c9300-netconf-probe-tier1/) | NETCONF dial diagnostics (#6(a) narrowing) |

Operator-runnable retest playbook with per-test pre-state / verify / rollback: [`final/release-blocker-tests/RUNBOOK.md`](./final/release-blocker-tests/RUNBOOK.md).

---

## 10. Troubleshooting decision tree

```
phase=Failed?
├── reason=DialFailed
│   ├── connection refused → device-side service not running (see §3.5/§4.5/§5.5)
│   └── i/o timeout → firewall / wrong port → check port-default rules in §1
├── reason=ApplyFailed
│   ├── rpc-error unknown-element → upgrade to image with commit 88ac685+
│   ├── rpc-error lock-denied → transient; reconcile retry absorbs (see §4.5)
│   └── tls handshake → §3.5 (RESTCONF) or §5.5 (gNMI secure)
├── reason=Failed message="drift persisted after revert"
│   └── upgrade to image with commit 88ac685 (NETCONF Fetch shape fix)
└── reason=ValidationFailed
    └── unknown field spec.confirmTimeoutSeconds/atomicReplace
        → apply charts/.../config.cisco.vk_iosxeconfigs.yaml (see §6.4)
```

---

## 11. Configuration examples

Every example below is copy-pasteable. Replace `10.1.1.1`, `cisco`, and `<device-password>` with your lab values. Each example assumes namespace `cisco-vk-smoke` exists; `kubectl create namespace cisco-vk-smoke` if not.

### 11.1 RESTCONF — managing the banner motd

The simplest deployment construct. One Secret, one CiscoDevice, one IOSXEConfig.

```yaml
---
apiVersion: v1
kind: Secret
metadata:
  name: cat9k-creds
  namespace: cisco-vk-smoke
type: Opaque
stringData:
  password: <device-password>
---
apiVersion: cisco.vk/v1alpha1
kind: CiscoDevice
metadata:
  name: cat9k-smoke
  namespace: cisco-vk-smoke
spec:
  driver: XE
  address: "10.1.1.1"
  username: cisco
  credentialSecretRef:
    name: cat9k-creds
  tls:
    enabled: true
    insecureSkipVerify: true     # production: use a CA-signed cert
  transport: restconf
  port: 443
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: example-restconf-banner-data
  namespace: cisco-vk-smoke
data:
  data.nac.yaml: |
    iosxe:
      devices:
        - name: cat9k-smoke
          configuration:
            banner:
              motd:
                banner: "Welcome — restricted to authorised personnel"
---
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEConfig
metadata:
  name: example-restconf-banner
  namespace: cisco-vk-smoke
spec:
  deviceRef:
    name: cat9k-smoke
  managedFamilies:
    - banner
  transactional: false             # RESTCONF has no transactions
  writeStartup: false
  driftPolicy: revert
  driftDetectInterval: 1m
  source:
    configMapRef:
      name: example-restconf-banner-data
      key: data.nac.yaml
```

After apply, expect:
```
$ kubectl get iosxeconfig example-restconf-banner -n cisco-vk-smoke
NAME                       PHASE    AGE
example-restconf-banner    InSync   30s

$ curl -s :8080/metrics | grep mutate_ops_total | grep restconf
cisco_vk_config_mutate_ops_total{device="cat9k-smoke",transport="restconf",verb="MERGE"} 1
```

### 11.2 NETCONF writable-running — transactional banner

Use when the device advertises both `:candidate:1.0` and `:writable-running:1.0`. The engine uses candidate + commit because `transactional: true`.

```yaml
---
apiVersion: cisco.vk/v1alpha1
kind: CiscoDevice
metadata:
  name: cat9k-smoke
  namespace: cisco-vk-smoke
spec:
  driver: XE
  address: "10.1.1.1"
  username: cisco
  credentialSecretRef:
    name: cat9k-creds
  # tls.enabled is a NO-OP for NETCONF (SSH transport)
  transport: netconf
  port: 830
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: example-netconf-banner-data
  namespace: cisco-vk-smoke
data:
  data.nac.yaml: |
    iosxe:
      devices:
        - name: cat9k-smoke
          configuration:
            banner:
              motd:
                banner: "Welcome — NETCONF transactional"
---
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEConfig
metadata:
  name: example-netconf-banner
  namespace: cisco-vk-smoke
spec:
  deviceRef:
    name: cat9k-smoke
  managedFamilies:
    - banner
  transactional: true              # candidate datastore + commit
  writeStartup: true               # also persist to startup-config
  driftPolicy: revert
  driftDetectInterval: 1m
  source:
    configMapRef:
      name: example-netconf-banner-data
      key: data.nac.yaml
```

Expected metrics:
```
cisco_vk_config_mutate_ops_total{transport="netconf",verb="MERGE"}     ≥ 1
cisco_vk_config_transactions_total{outcome="commit",transport="netconf"} ≥ 1
cisco_vk_config_save_startup_total{outcome="ok",transport="netconf"}   ≥ 1
```

### 11.3 NETCONF candidate-only — interface_loopback with implicit-tx

The recommended NETCONF deployment construct. Device-side prereq: `netconf-yang feature candidate-datastore`. Engine sees `:candidate:1.0` advertised but `:writable-running:1.0` absent, and the implicit-tx auto-promote wraps non-transactional ops in lock+commit+unlock automatically.

```yaml
---
# CiscoDevice spec same as §11.2 (transport=netconf, port=830).

---
apiVersion: v1
kind: ConfigMap
metadata:
  name: example-candidate-only-data
  namespace: cisco-vk-smoke
data:
  data.nac.yaml: |
    iosxe:
      devices:
        - name: cat9k-smoke
          configuration:
            interface_loopback:
              interfaces:
                - name: 9990
                  description: "Production loopback — managed by cisco-vk"
                  ipv4_address: 10.255.255.90
                  ipv4_address_mask: 255.255.255.255
---
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEConfig
metadata:
  name: example-candidate-only-loopback
  namespace: cisco-vk-smoke
spec:
  deviceRef:
    name: cat9k-smoke
  managedFamilies:
    - interface_loopback
  transactional: false             # implicit-tx still gives atomic apply
  writeStartup: false
  driftPolicy: revert
  driftDetectInterval: 1m
  source:
    configMapRef:
      name: example-candidate-only-data
      key: data.nac.yaml
```

This is the construct validated by the [2026-04-27 candidate-only evidence bundle](./final/evidence/2026-04-27-live-c9300-netconf-candidate-only/SUMMARY.md). Tests 06 + 07 over this construct land at phase=InSync.

### 11.4 gNMI — interface_ethernet keyed path

Used for keyed paths whose values contain `/` (interface names like `1/0/24`) and for telemetry-driven drift detection.

```yaml
---
apiVersion: cisco.vk/v1alpha1
kind: CiscoDevice
metadata:
  name: cat9k-smoke
  namespace: cisco-vk-smoke
spec:
  driver: XE
  address: "10.1.1.1"
  username: cisco
  credentialSecretRef:
    name: cat9k-creds
  tls:
    enabled: false                 # default gnxi insecure; set true for gnxi secure-server
  transport: gnmi
  port: 50052                      # IOS-XE 17.18+ insecure default
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: example-gnmi-data
  namespace: cisco-vk-smoke
data:
  data.nac.yaml: |
    iosxe:
      devices:
        - name: cat9k-smoke
          configuration:
            interface_ethernet:
              interfaces:
                - type: GigabitEthernet
                  name: "1/0/24"
                  description: "Managed by cisco-vk via gNMI"
---
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEConfig
metadata:
  name: example-gnmi-iface
  namespace: cisco-vk-smoke
spec:
  deviceRef:
    name: cat9k-smoke
  managedFamilies:
    - interface_ethernet
  transactional: true              # gNMI Set is atomic — single SetRequest
  writeStartup: false              # gNMI does not implement SaveStartup
  driftPolicy: revert
  driftDetectInterval: 30s
  source:
    configMapRef:
      name: example-gnmi-data
      key: data.nac.yaml
```

Expected metrics:
```
cisco_vk_config_mutate_ops_total{transport="gnmi",verb="MERGE"}     ≥ 1
cisco_vk_config_transactions_total{outcome="commit",transport="gnmi"} ≥ 1
```

### 11.5 Wave 10 — confirmed-commit (auto-revert safety net)

Protects against management-plane-breaking applies (bad ACL, wrong SVI on the management VLAN, etc.). If the running-config verify after candidate-commit fails, the engine deliberately omits `ConfirmCommit` and the device's own RFC 6241 timer reverts at `confirmTimeoutSeconds`.

Requires: `transport: netconf` AND device advertises `:confirmed-commit:1.0` AND `transactional: true` AND the chart's Wave-10 CRD applied (see §6.4).

```yaml
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: example-confirmed-commit-data
  namespace: cisco-vk-smoke
data:
  data.nac.yaml: |
    iosxe:
      devices:
        - name: cat9k-smoke
          configuration:
            access_list_extended:
              acls:
                - name: PROD-MGMT-OOB
                  entries:
                    - sequence: 10
                      action: permit
                      protocol: ip
                      source: any
                      destination: 10.0.0.0/8
---
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEConfig
metadata:
  name: example-confirmed-commit
  namespace: cisco-vk-smoke
spec:
  deviceRef:
    name: cat9k-smoke
  managedFamilies:
    - access_list_extended
  transactional: true
  confirmTimeoutSeconds: 30        # 1–600s; clamped at transport boundary
  writeStartup: false
  driftPolicy: revert
  driftDetectInterval: 1m
  source:
    configMapRef:
      name: example-confirmed-commit-data
      key: data.nac.yaml
```

Expected events on success:
```
$ kubectl describe iosxeconfig example-confirmed-commit -n cisco-vk-smoke
Events:
  Type    Reason                Age   Message
  Normal  ConfirmedCommitUsed   30s   confirmed-commit + verify + commit (timeout 30s)
```

On verify-failure path the device auto-reverts; the operator sees an `ApplyFailed` Warning event with the verify-time drift detail.

### 11.6 Wave 10 — atomic replace (cross-family)

Treats the resolved intent as authoritative for managed families. Adds + deletes + updates land in **one** transaction, with cross-family dependency ordering from `schema/families.yaml`. Used when partial-drift between families is intolerable (e.g. removing a VRF must happen with the loopbacks in it, in one shot).

```yaml
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: example-atomic-replace-data
  namespace: cisco-vk-smoke
data:
  data.nac.yaml: |
    iosxe:
      devices:
        - name: cat9k-smoke
          configuration:
            vlan:
              vlans:
                - id: 200
                  name: payroll-segment
            vrf:
              vrfs:
                - name: PAYROLL-VRF
                  rd: "65000:200"
            interface_loopback:
              interfaces:
                - name: 200
                  description: "Payroll-VRF loopback"
                  vrf: PAYROLL-VRF
                  ipv4_address: 10.200.0.1
                  ipv4_address_mask: 255.255.255.255
---
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEConfig
metadata:
  name: example-atomic-replace
  namespace: cisco-vk-smoke
spec:
  deviceRef:
    name: cat9k-smoke
  managedFamilies:
    - vlan
    - vrf
    - interface_loopback
  transactional: true
  atomicReplace: true              # single transaction across all three families
  writeStartup: false
  driftPolicy: revert
  driftDetectInterval: 1m
  source:
    configMapRef:
      name: example-atomic-replace-data
      key: data.nac.yaml
```

To later REMOVE everything atomically, replace the ConfigMap's data with an empty configuration block (`iosxe.devices[0].configuration: {}`) and re-apply — `atomicReplace` will issue deletes for all three families in one transaction in reverse-dependency order (interface_loopback → vrf → vlan).

### 11.7 Wave 10 — composed (atomic-replace + confirmed-commit)

The recommended-default for production CRs touching routing or interfaces. Test 13 in the runbook validates this construct.

```yaml
---
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEConfig
metadata:
  name: example-wave10-composed
  namespace: cisco-vk-smoke
spec:
  deviceRef:
    name: cat9k-smoke
  managedFamilies:
    - vlan
    - vrf
    - interface_loopback
  transactional: true
  atomicReplace: true              # one transaction across families
  confirmTimeoutSeconds: 30        # auto-revert if running-verify fails
  writeStartup: true               # persist on success
  driftPolicy: revert
  driftDetectInterval: 1m
  source:
    configMapRef:
      name: example-atomic-replace-data
      key: data.nac.yaml
```

Two `ConfirmedCommitUsed` events expected (one per atomic-replace phase: establish + cleanup); zero `ConfirmedCommitFallback` events; zero auto-reverts on the happy path.

### 11.8 Inline source (no ConfigMap)

For small / one-off configs you can inline the netascode YAML in the IOSXEConfig itself instead of a ConfigMap reference.

```yaml
---
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEConfig
metadata:
  name: example-inline-banner
  namespace: cisco-vk-smoke
spec:
  deviceRef:
    name: cat9k-smoke
  managedFamilies:
    - banner
  transactional: false
  driftPolicy: revert
  driftDetectInterval: 1m
  source:
    inline:
      iosxe:
        devices:
          - name: cat9k-smoke
            configuration:
              banner:
                motd:
                  banner: "Inline configuration sample"
```

`spec.source.inline` is a schemaless object (`runtime.RawExtension` under the hood — `+kubebuilder:pruning:PreserveUnknownFields`), NOT a string literal. Trade-off: inline edits roll the IOSXEConfig generation, ConfigMap edits don't. ConfigMaps are the better choice when the configuration is updated by GitOps tooling that uses `kubectl apply -f` semantics.

---

## 12. Topology examples — per-pod kubelet vs aggregator-mode

### 12.1 Per-pod kubelet (recommended)

Default Helm topology — `aggregator.enabled: false`. The controller manager watches `CiscoDevice` CRs and creates a per-device Deployment automatically; you configure only the chart, not per-device pod manifests.

```yaml
# values-perpod.yaml
# helm install cisco-vk ./charts/cisco-virtual-kubelet -f values-perpod.yaml

# Shared default image for both the controller and the per-device pods.
# Override per-role with controllerImage / vkImage if they should diverge.
image:
  repository: containers.dmz.cisco.com:5000/cisco-vk
  tag: v29
  pullPolicy: IfNotPresent

# Per-role overrides. Empty repository means "use the shared image above."
controllerImage:
  repository: ""
  tag: ""
  pullPolicy: ""
vkImage:
  repository: ""
  tag: ""
  pullPolicy: ""

aggregator:
  enabled: false                   # per-pod is the recommended construct

controller:
  leaderElect: false               # set true when running ≥2 controller replicas
  metricsBindAddress: ":8080"
  healthProbeBindAddress: ":8081"

config:
  # Set when IOSXEConfig CRs in different tenant namespaces target the same
  # device — leases for per-(device, family) arbitration land here so they
  # don't fragment per-namespace.
  leaseNamespace: ""               # default: per-pod namespace

resources:
  requests: { cpu: 100m, memory: 64Mi }
  limits:   { cpu: 200m, memory: 128Mi }
```

After install, apply CiscoDevice + IOSXEConfig CRs (per §11) and the controller fans out a Deployment per device under labels `app.kubernetes.io/managed-by=ciscodevice-controller`. To switch a device's transport, patch its CiscoDevice (`kubectl patch ciscodevice <name> -n <ns> --type=merge -p '{"spec":{"transport":"netconf"}}'`) and delete the per-device pod so it picks up the new ConfigMap via deferred-dial.

### 12.2 Aggregator-mode (corner-case)

Single controller process with one `deviceWorker` goroutine per device — no per-device pods. Use only when running per-device pods is operationally awkward (air-gapped operator, bench-test rig). Live-device evidence on this branch was captured against per-pod only; aggregator is exercised by unit + envtest.

```yaml
# values-aggregator.yaml
image:
  repository: containers.dmz.cisco.com:5000/cisco-vk
  tag: v29
  pullPolicy: IfNotPresent

aggregator:
  enabled: true                    # in-process per-device ConfigReconciler

controller:
  leaderElect: false
  metricsBindAddress: ":8080"
  healthProbeBindAddress: ":8081"

# Bump resources — one pod handles every CiscoDevice.
resources:
  requests: { cpu: 250m, memory: 512Mi }
  limits:   { cpu: 2000m, memory: 2Gi }
```

Trade-offs to weigh before choosing aggregator-mode:
- One pod handles every CiscoDevice → memory and CPU scale with the device count
- A controller-pod restart pauses reconcile for every device simultaneously (per-pod is failure-isolated)
- Single `/metrics` endpoint and log stream — easier to centrally scrape, harder to diagnose per-device issues

---

## 13. Resolution-chain examples

The engine's intent resolver merges the following layers in fixed order before each reconcile: **defaults → device-groups → interface-groups → templates → per-device source → per-family secret refs**. Use the lower layers for cross-cutting baselines, the higher layers for device-specific intent.

### 13.1 Cluster-wide baseline (`IOSXEConfigDefaults`)

```yaml
---
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEConfigDefaults
metadata:
  name: default                    # singleton; name MUST be "default"
spec:
  configuration:
    cdp:
      run: true                    # CDP on by default everywhere
    system:
      login_on_failure: true
      login_on_success: true
    logging:
      buffered:
        size: 65536
        severity: informational
```

### 13.2 Device-group scope (`IOSXEDeviceGroupConfig`)

```yaml
---
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEDeviceGroupConfig
metadata:
  name: access-switches
  namespace: cisco-vk-smoke
spec:
  deviceSelector:
    matchLabels:
      role: access                 # matches CiscoDevice.metadata.labels.role
  configuration:
    snmp_server:
      community:
        - name: ops-readonly
          access: ro
        - name: ops-readwrite
          access: rw
          acl: "10"
```

Apply a matching label on the CiscoDevice: `kubectl label ciscodevice <name> role=access`.

### 13.3 Reusable parameterised fragment (`IOSXETemplate`)

```yaml
---
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXETemplate
metadata:
  name: standard-loopback
  namespace: cisco-vk-smoke
spec:
  parameters:
    - name: id
      type: int
      required: true
    - name: address
      type: ipv4
      required: true
    - name: description
      type: string
      default: "Standard loopback"
  configuration:
    interface_loopback:
      interfaces:
        - name: "{{ .id }}"
          description: "{{ .description }}"
          ipv4_address: "{{ .address }}"
          ipv4_address_mask: 255.255.255.255
```

Reference from an `IOSXEConfig`:

```yaml
spec:
  templateRefs:
    - name: standard-loopback
      values:
        id: 9991
        address: 10.255.255.91
        description: "Telemetry endpoint"
```

### 13.4 Selector-based fan-out (`IOSXEConfigBundle`)

Apply the same configuration to every CiscoDevice matching a selector — the controller materialises N child `IOSXEConfig` CRs with owner-references for GC.

```yaml
---
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEConfigBundle
metadata:
  name: lab-banner-rollout
  namespace: cisco-vk-smoke
spec:
  deviceSelector:
    matchLabels:
      env: lab
  template:                          # IOSXEConfigTemplateSpec — no nested .spec
    managedFamilies:
      - banner
    transactional: false
    driftPolicy: revert
    source:
      inline:
        iosxe:
          devices:
            - configuration:
                banner:
                  motd:
                    banner: "LAB DEVICE — non-production data"
```

The controller fills `deviceRef` on every generated child IOSXEConfig at materialisation time and substitutes the device name into `iosxe.devices[].name`.

### 13.5 Secret refs for sensitive families

When a family includes sensitive values (SNMPv3 auth keys, RADIUS shared secrets), reference them from a Secret instead of inlining:

```yaml
---
apiVersion: v1
kind: Secret
metadata:
  name: snmp-secrets
  namespace: cisco-vk-smoke
type: Opaque
stringData:
  data.nac.yaml: |
    iosxe:
      devices:
        - configuration:
            snmp_server:
              v3:
                users:
                  - name: ops-monitoring
                    auth_protocol: sha
                    auth_password: <secret>
---
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEConfig
metadata:
  name: snmp-with-secrets
  namespace: cisco-vk-smoke
spec:
  deviceRef: { name: cat9k-smoke }
  managedFamilies: [snmp_server]
  transactional: true
  source:
    inline:
      iosxe:
        devices:
          - name: cat9k-smoke
            configuration: {}      # populated entirely from secretRefs
  secretRefs:
    - name: snmp-secrets
      key: data.nac.yaml
      family: snmp_server
```

The resolver merges secretRefs into the resolved intent **after** the source layer, scoped to the named family. Secrets never appear in the IOSXEConfig spec or status.

---

## See also

- [`./transport-architecture.md`](./transport-architecture.md) — maintainer-facing architecture reference (wire shapes, capabilities matrix, fix-bundle history)
- [`./final/release-blocker-tests/RUNBOOK.md`](./final/release-blocker-tests/RUNBOOK.md) — operator playbook for the 11 release-blocker live-device retests
- [`./final/README.md`](./final/README.md) — branch implementation reference, with §7 roadmap
- [`../../examples/configs/`](../../examples/configs/) and [`../../examples/gitops-reference/`](../../examples/gitops-reference/) — additional in-tree examples
