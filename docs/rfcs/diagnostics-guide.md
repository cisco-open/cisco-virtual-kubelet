# Diagnostics guide — running IOS-XE show commands from kubectl

**Audience:** operators using cisco-virtual-kubelet to run read-only diagnostic commands (`show ...`, `dir`, `more`) against managed IOS-XE devices. Maintainers wanting the architectural / RFC-style design should read [`./diagnostics-rfc.md`](./diagnostics-rfc.md). For destructive commands (`clear`, `reload`, `write erase`) see [`./device-operations-rfc.md`](./device-operations-rfc.md).

This document covers everything an operator needs to invoke, schedule, and audit diagnostics through cisco-vk on a real device. All examples were validated against a live Cat9300 / IOS-XE 17.18.01 on 2026-04-28 — the captured outputs are in [`./final/evidence/2026-04-28-live-c9300-diagnostics/`](./final/evidence/2026-04-28-live-c9300-diagnostics/SUMMARY.md).

---

## 1. Quick start

### 1.1 Run a show command in one line — `kubectl ciscovk exec`

```bash
kubectl ciscovk exec cat9k-smoke -n cisco-vk-smoke -- show ip route summary
```

Output:
```
# device=cat9k-smoke transport=netconf captured=2026-04-28T06:55:39Z
IP routing table name is default (0x0)
IP routing table maximum-paths is 32
Route Source    Networks    Subnets     Replicates  Overhead    Memory (bytes)
static          2           1           0           336         936
connected       0           22          0           2464        6864
isis            ...
```

The plugin port-forwards to the per-device kubelet pod's admin endpoint and POSTs your command list — no SSH session, no separate credential store, no separate audit trail.

### 1.2 Capture a snapshot declaratively — `IOSXEDiagnostic` CRD

When you want the capture to live in etcd (audit, GitOps, dashboards):

```yaml
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEDiagnostic
metadata:
  name: incident-2026-04-28-snapshot
  namespace: cisco-vk-smoke
spec:
  deviceRef:
    name: cat9k-smoke
  commands:
    - show version
    - show ip route summary
    - show ip ospf neighbor
```

```bash
kubectl apply -f snapshot.yaml
kubectl get iosxediag -n cisco-vk-smoke
```

```
NAME                            DEVICE         COMMANDS   PHASE       LAST                 AGE
incident-2026-04-28-snapshot    cat9k-smoke    3          Completed   2026-04-28T06:55:39Z 12s
```

The full per-command output lives in `.status.results[]` and is reachable via standard `kubectl get -o yaml` / `-o jsonpath` queries.

---

## 2. How it works

### 2.1 Two operator-facing surfaces, same backend

| Surface | When to use | Audit trail | Shape |
|---|---|---|---|
| `kubectl ciscovk exec` plugin | Interactive triage; "right now, what's the state?" | Pod logs only | Streamed stdout to terminal |
| `IOSXEDiagnostic` CRD | Repeatable captures; scheduled monitoring; GitOps; multi-MB outputs needing ConfigMap sink | etcd-backed CR + ConfigMaps | YAML / JSON results in `.status.results[]` |

Both go through the same backend.

### 2.2 The SSH-CLI side-channel

IOS-XE 17.18.x has **no YANG-modelled RPC that returns textual `show` output**:

- The legacy ConfD `<cli-exec>` element was tightened off in 17.18.
- `Cisco-IOS-XE-cli-rpc:config-ios-cli-rpc` accepts the request but its `result` leaf only carries `"RPC request successful"` — designed for config writes.
- Operational state is exposed as structured YANG (`Cisco-IOS-XE-*-oper:*-oper-data`), not as the `show` text operators are used to.

cisco-vk works around this by opening an SSH session on the device's CLI port (22 by default) using the same operator credentials the configdriver session already holds, requesting a vt100 PTY, disabling pagination via `terminal length 0` / `terminal width 0`, and capturing the output between each command's echo and the next prompt. This mirrors how `gnoi cli.exec` works on platforms that ship gNOI.

The architectural rationale is documented in [`./transport-architecture.md`](./transport-architecture.md) §11; the per-version RPC archaeology is in [`./diagnostics-rfc.md`](./diagnostics-rfc.md) §2.

### 2.3 Where the request lands

```
                        ┌──────────────────────────────────────────────┐
                        │  Operator                                    │
                        │   $ kubectl ciscovk exec cat9k-smoke         │
                        │     -n cisco-vk-smoke -- show ip route       │
                        └──────────────────┬───────────────────────────┘
                                           │  port-forward + POST
                                           │  /v1/exec
                                           ▼
                        ┌──────────────────────────────────────────────┐
                        │  Per-device kubelet pod                      │
                        │  (cat9k-smoke-vk-...)                        │
                        │                                              │
                        │  ┌─────────────────┐  ┌─────────────────┐    │
                        │  │  admin server   │  │ IOSXEDiagnostic │    │
                        │  │  127.0.0.1:8082 │  │   reconciler    │    │
                        │  └────────┬────────┘  └────────┬────────┘    │
                        │           │                    │             │
                        │           └─────────┬──────────┘             │
                        │                     │                        │
                        │           ┌─────────▼──────────┐             │
                        │           │ DiagnosticExecer   │             │
                        │           │ (transport iface)  │             │
                        │           └─────────┬──────────┘             │
                        │                     │                        │
                        │           ┌─────────▼──────────┐             │
                        │           │ runShowCommandsVia │             │
                        │           │       SSH          │             │
                        │           └─────────┬──────────┘             │
                        └─────────────────────┼────────────────────────┘
                                              │ SSH(22) PTY shell
                                              ▼
                                    ┌──────────────────────┐
                                    │  IOS-XE device       │
                                    │  10.1.1.1            │
                                    └──────────────────────┘
```

---

## 3. The `kubectl ciscovk exec` plugin

### 3.1 Install

The plugin is a single static binary. Build from source:

```bash
go build -o ~/.local/bin/kubectl-ciscovk ./tools/kubectl-ciscovk/
chmod +x ~/.local/bin/kubectl-ciscovk
```

`kubectl plugin list` should now show `kubectl-ciscovk`. Replace `~/.local/bin` with any directory on your `$PATH`.

### 3.2 Synopsis

```
kubectl ciscovk exec <device> [-n <namespace>] [flags] -- <show-command...>
```

### 3.3 Flags

| Flag | Default | Effect |
|---|---|---|
| `-n`, `--namespace` | (current namespace) | Namespace of the per-device kubelet pod |
| `--allow-secrets` | `false` | Disable default secret-redaction filter |
| `--truncate-bytes N` | `65536` (64 KiB) | Cap each command's output; `0` disables |
| `--port N` | random | Local port for `kubectl port-forward`; `0` = random free |
| `--timeout` | `30s` | Overall timeout for the round-trip |
| `--kubectl PATH` | `kubectl` (from `$PATH`) | Path to kubectl binary |

### 3.4 Examples

#### Single command
```bash
kubectl ciscovk exec cat9k-smoke -n cisco-vk-smoke -- show version
```

#### Quote-protect commands with shell metacharacters
```bash
kubectl ciscovk exec cat9k-smoke -n cisco-vk-smoke -- "show running-config | section interface"
```

#### Multiple commands in one batch
```bash
kubectl ciscovk exec cat9k-smoke -n cisco-vk-smoke -- "show version ; show ip route ; show ip ospf neighbor"
```
(IOS-XE accepts `;`-separated chains in a single line.)

#### Pipe directly to grep / awk
```bash
kubectl ciscovk exec cat9k-smoke -n cisco-vk-smoke -- show ip route \
  | awk '/^[OBR]/{print $1, $NF}'
```

#### Audit-mode (operator with elevated rights)
```bash
kubectl ciscovk exec cat9k-smoke -n cisco-vk-smoke --allow-secrets -- show running-config
```

### 3.5 Output format

The plugin always prints a leading context line on stdout:
```
# device=<name> transport=<restconf|netconf|gnmi> captured=<ISO8601-UTC>
```

Then the per-command output. With multiple commands you get a separator:
```
# device=cat9k-smoke transport=netconf captured=2026-04-28T06:55:49Z

# ─── show version ──────────────────────────────
Cisco IOS XE Software, Version 17.18.01
...

# ─── show ip route ──────────────────────────────
Codes: L - local, C - connected, S - static
...
```

Errors (per-command rejections from the device, truncation notices, transport errors) go to **stderr** with a `# error:` / `# (output truncated...)` prefix so `kubectl ciscovk exec ... > capture.txt` cleanly separates the capture body from operator-facing prose.

### 3.6 Destructive-command refusal

The plugin refuses these prefixes at the client (defence in depth — the admin server only invokes `cli-exec`-shaped paths regardless):

| Prefix | Reason |
|---|---|
| `reload` | Out of scope — see [`./device-operations-rfc.md`](./device-operations-rfc.md) `IOSXEDeviceOp` |
| `write erase` | Same |
| `delete flash:` | Same |
| `format flash:` | Same |
| `clear ` | Out of scope — see `IOSXEMaintenance` in the device-operations RFC |

```bash
$ kubectl ciscovk exec cat9k-smoke -- reload
error: destructive command "reload" is not supported by `exec`; see device-operations-rfc.md for IOSXEMaintenance / IOSXEDeviceOp
```

---

## 4. The `IOSXEDiagnostic` CRD

### 4.1 Spec reference

```yaml
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEDiagnostic
metadata:
  name: <crname>
  namespace: <ns>
spec:
  # ── Required ──────────────────────────────────
  deviceRef:
    name: <CiscoDevice-name>          # required; same namespace
  commands:                           # required; min 1, max 64
    - show version
    - show ip route

  # ── Schedule (optional) ───────────────────────
  schedule:
    interval: 1h                      # Go duration string; min 30s
    # OR
    # cron: "0 * * * *"               # NOT YET IMPLEMENTED — see §10

  # ── Maintenance window (optional) ─────────────
  notBefore: "2026-04-28T13:00:00Z"   # CR sits in Scheduled until then
  notAfter:  "2026-04-28T13:30:00Z"   # CR moves to Expired if not done by then

  # ── Retention (optional) ──────────────────────
  retention:
    maxResults: 24                    # default; 1..200
    truncateAt: "64KiB"               # default; pattern ^[0-9]+(B|KiB|MiB)$

  # ── Audit-mode opt-out (optional) ─────────────
  allowSecrets: false                 # default; true disables redaction

  # ── ConfigMap sink (optional, see §6) ─────────
  outputSink:
    inline: true                      # default; mutually exclusive with configMapRef
    # OR
    # configMapRef:
    #   namePrefix: "snapshot-"       # required; DNS-1123 + optional trailing dash
    #   namespace: ""                 # default: same as the CR
```

### 4.2 Status reference

```yaml
status:
  phase: Completed                    # Pending | Capturing | Completed | Failed | Scheduled | Expired
  observedGeneration: 1
  lastCapture: "2026-04-28T13:00:04Z"
  nextCapture: "2026-04-28T14:00:00Z" # populated when schedule is set
  results:
    - capturedAt: "2026-04-28T13:00:04Z"
      transportError: ""              # set when SSH dial / RPC framing failed
      commands:
        - command: "show version"
          output: |
            Cisco IOS XE Software, Version 17.18.01
            ...
          err: ""                     # per-command failure reason (if any)
          truncated: false            # output clipped at retention.truncateAt
          redacted: false             # secret-redaction filter dropped at least one line
          configMapRef:               # populated only with the ConfigMap sink
            name: snapshot-20260428-130004
            namespace: cisco-vk-smoke
            key: show-version
  conditions:
    - type: Ready
      status: "True"
      reason: Captured
      message: "3 command(s) captured"
      lastTransitionTime: "2026-04-28T13:00:04Z"
```

### 4.3 Printer columns

```
$ kubectl get iosxediag -n cisco-vk-smoke
NAME                       DEVICE         COMMANDS   PHASE       LAST                  AGE
incident-snapshot          cat9k-smoke    3          Completed   2026-04-28T13:00:04Z  4m
ospf-hourly                cat9k-smoke    1          Completed   2026-04-28T13:00:00Z  3h
running-snapshot           cat9k-smoke    1          Completed   2026-04-28T12:55:00Z  10m
```

| Column | jsonPath |
|---|---|
| `DEVICE` | `.spec.deviceRef.name` |
| `COMMANDS` | length of `.spec.commands` |
| `PHASE` | `.status.phase` |
| `LAST` | `.status.lastCapture` |
| `AGE` | `.metadata.creationTimestamp` |

`kubectl get iosxediag` short name `iosxediag`, `iosxediagnostics`, `iosxediagnostic` all work.

### 4.4 Phase enum

| Phase | Meaning |
|---|---|
| `Pending` | Reconciler hasn't observed the CR yet |
| `Capturing` | Transient — SSH-CLI batch in flight (rarely visible at rest) |
| `Completed` | Capture finished; rolling state for scheduled CRs |
| `Failed` | Transport-level error or transport doesn't support diagnostics; per-command failures alone do NOT move the CR to Failed |
| `Scheduled` | Maintenance window: `notBefore` is in the future |
| `Expired` | Maintenance window: `notAfter` passed before any capture |

---

## 5. Common operator workflows

### 5.1 One-shot incident snapshot

Maintenance window optional, retention single-shot:

```yaml
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEDiagnostic
metadata:
  name: inc-2026-04-28-routing-spike
  namespace: cisco-vk-smoke
spec:
  deviceRef: { name: cat9k-smoke }
  commands:
    - show version
    - show ip route summary
    - show ip ospf neighbor detail
    - show platform hardware fed switch active fwd-asic resource tcam utilization
```

`kubectl apply -f snapshot.yaml`, then read `.status.results[0].commands[*].output` once `phase=Completed`.

### 5.2 Hourly fleet rollover (scheduled)

```yaml
spec:
  deviceRef: { name: cat9k-smoke }
  commands: [ "show ip ospf neighbor", "show isis neighbors" ]
  schedule:
    interval: 1h
  retention:
    maxResults: 168                   # 7 days × 24 captures
    truncateAt: "32KiB"
```

The reconciler appends each capture to `.status.results[]` and trims oldest-first when the slice exceeds `maxResults`.

### 5.3 Multi-MB outputs (`show running-config`, `show tech-support`)

Use the ConfigMap sink (next section). A bare inline capture of `show tech-support` would blow past etcd's 1 MiB per-object limit.

### 5.4 GitOps-tracked compliance snapshots

Drop the `IOSXEDiagnostic` YAML into a Flux/ArgoCD repo with `schedule: { interval: 24h }`. Operators can browse the per-day capture history via `kubectl get iosxediag <name> -o yaml | yq '.status.results[]'`.

### 5.5 Audit-mode (security review)

```yaml
spec:
  deviceRef: { name: cat9k-smoke }
  allowSecrets: true                  # bypass redaction
  commands: [ "show running-config" ]
  outputSink:
    configMapRef:
      namePrefix: "audit-fullconfig-"
```

Capture full unredacted running-config to a per-snapshot ConfigMap. The `allowSecrets: true` field is auditable — `kubectl get iosxediag` shows it in the spec, so unauthorised retention is detectable post-hoc.

---

## 6. ConfigMap output sink

Default storage is inline in `.status.results[].commands[].output`. For multi-MB outputs that exceed etcd's per-object limit, opt into the ConfigMap sink:

```yaml
spec:
  outputSink:
    configMapRef:
      namePrefix: "running-snapshot-"
      namespace: ""                   # default: CR's namespace
```

### 6.1 What the sink does

For each capture, the reconciler:

1. Writes a fresh ConfigMap named `<namePrefix><RFC3339-timestamp>` (e.g. `running-snapshot-20260428-065338`) carrying ONE data key per command (sanitised: `show running-config` → `show-running-config`).
2. Sets `OwnerReferences` on the ConfigMap pointing at the IOSXEDiagnostic CR (when the sink namespace = CR namespace) — deletion of the CR cascades to every captured ConfigMap.
3. Labels the ConfigMap with `cisco.vk/diagnostic=<crname>` so operators can list captures via `kubectl get cm -l cisco.vk/diagnostic=<crname>`.
4. Replaces the inline `.status.results[].commands[].output` with a 2 KiB preview so `kubectl describe iosxediag` still surfaces the start of every capture without chasing the ConfigMap.
5. Sets `.status.results[].commands[].configMapRef` pointing at the captured ConfigMap.
6. When count of captures exceeds `retention.maxResults`, deletes the oldest ConfigMaps (sorted by the `cisco.vk/diagnostic-capturedAt` label).

### 6.2 Reading a captured ConfigMap

```bash
# Find the ConfigMaps for one CR
kubectl get cm -n cisco-vk-smoke -l cisco.vk/diagnostic=running-snapshot

# Read a specific capture's body
kubectl get cm running-snapshot-20260428-065338 -n cisco-vk-smoke \
  -o jsonpath='{.data.show-running-config}'

# Or follow the configMapRef from the CR
kubectl get iosxediag running-snapshot -n cisco-vk-smoke \
  -o jsonpath='{.status.results[0].commands[0].configMapRef}'
```

### 6.3 Cross-namespace sinks

Setting `outputSink.configMapRef.namespace` to a different namespace works but **disables the owner-reference cascade-delete** (Kubernetes forbids cross-namespace OwnerReferences). Operators must clean up manually:

```bash
kubectl delete cm -n <sink-ns> -l cisco.vk/diagnostic=<crname>
```

---

## 7. RBAC

Diagnostics are read-only — they invoke `cli-exec`-shaped commands only and refuse destructive prefixes both at the plugin (defence in depth) and at the spec level (admission won't accept `clear`/`reload`/`write erase` because these never make it to the SSH-CLI helper from `IOSXEDiagnostic`'s code path; they go through the destructive-ops sibling RFC).

The current chart RBAC grants the cisco-vk pod's ServiceAccount full diagnostic-CRD verbs and the ConfigMap-management verbs the sink needs. Operators adding their own RBAC tier should grant:

```yaml
- apiGroups: ["config.cisco.vk"]
  resources: ["iosxediagnostics", "iosxediagnostics/status"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
- apiGroups: [""]
  resources: ["configmaps"]
  verbs: ["get", "list", "watch"]                 # for reading captured outputs
- apiGroups: [""]
  resources: ["pods/portforward"]
  verbs: ["create"]                                # for the kubectl ciscovk plugin
```

The `pods/portforward` verb is the auth gate for the plugin path: operators without it can't reach the admin endpoint at all, regardless of CRD privileges.

---

## 8. Secret-redaction filter

By default the reconciler strips lines matching common Cisco-secret patterns from every captured output. The replaced line carries:

```
<redacted by cisco-vk — set spec.allowSecrets: true to disable>
```

Patterns currently caught (from [`internal/provider/diagnostic/redact.go`](../../internal/provider/diagnostic/redact.go)):

| Pattern | Example line that matches |
|---|---|
| `enable secret`, `enable password` | `enable secret 5 $1$abcd$xyz` |
| `username <name> [privilege <N>] (secret\|password) ...` | `username admin privilege 15 secret 9 $14$...` |
| `snmp-server community`, `snmp-server (host\|user)` | `snmp-server community public RO` |
| `tacacs-server (key\|host)`, `tacacs server <name>`, `radius-server (key\|host)`, `radius server <name>` | `tacacs server ux-kgs` (block header) |
| Indented `key 7 ...`, `key 0 ...`, `key chain`, bare `key <token>` | ` key tacacs123` (indented inside server stanza) |
| `crypto (isakmp\|ipsec\|key)` | `crypto isakmp key MYKEY address 10.0.0.1` |
| `pre-shared-key`, `shared-secret`, `server-private` | RADIUS / IPSec PSK lines |
| `ip ftp password`, `ip ssh pubkey-chain` | FTP / SSH key blocks |
| `ppp chap (password\|hostname)` | PPP-CHAP lines |

The line is replaced wholesale rather than just the secret token, because IOS encodes secrets in many shapes (`5 $1$...`, `7 abcdef`, hashed digests) and a per-token regex would inevitably drift behind format changes.

### 8.1 Disabling redaction (audit-mode)

`spec.allowSecrets: true` (CRD path) or `--allow-secrets` (plugin path) bypasses the filter. Use only when retention is internal and audit requirements demand the unredacted output.

### 8.2 Reporting a missed pattern

If a secret leaks through despite `allowSecrets: false`, file an issue with:

1. The line that leaked (with the secret value scrubbed manually before sharing)
2. The Cisco command that produced it
3. The output of `kubectl get iosxediag <name> -o jsonpath='{.status.results[*].commands[*].redacted}'` to confirm the filter ran

The fix is a one-line addition to the `secretLineRe` regex.

---

## 9. Events catalog

The reconciler emits these Kubernetes events against the `IOSXEDiagnostic` resource. List them with:

```bash
kubectl get events -n <ns> --field-selector involvedObject.name=<crname> --sort-by='.lastTimestamp'
```

| Type | Reason | When |
|---|---|---|
| `Normal` | `Captured` | Capture succeeded; message includes the per-command count |
| `Warning` | `TransportError` | SSH dial / framing failure; per-command failures stay inside `commands[].err` |
| `Warning` | `TransportUnsupported` | Device's transport doesn't implement DiagnosticExecer (gNMI today) |
| `Warning` | `BadSchedule` | `spec.schedule.interval` couldn't parse |
| `Warning` | `SinkError` | ConfigMap sink write failed (RBAC, conflict, etc.) |
| `Warning` | `SinkPruneFailed` | Retention trim failed; sink itself worked |

---

## 10. Metrics

The cisco-vk pod exposes Prometheus metrics on `:8080/metrics`. Diagnostics-relevant counters/histograms inherited from the configdriver core:

| Metric | Labels | What it answers |
|---|---|---|
| `cisco_vk_config_reconcile_duration_seconds` | `device`, `phase` | "How long is a diagnostic capture taking?" (filter `phase="Capturing"` or `phase="Completed"`) |
| `cisco_vk_config_apply_duration_seconds` | `device`, `family` | Not directly populated by diagnostics today; may surface in a future Phase D-fu |

`kubectl ciscovk` plugin invocations are a separate code path and are NOT recorded in these metrics today (they don't go through the reconciler). To audit plugin usage, watch the pod logs for `admin server: handled /v1/exec ...` entries.

---

## 11. Troubleshooting

### 11.1 Plugin: `error: no pod found for device "<name>"`

The plugin resolves pods by label `app.kubernetes.io/instance=<device>`. Check:
```bash
kubectl get pod -n <ns> -l app.kubernetes.io/instance=<device>
```

If empty, the per-device kubelet hasn't been scheduled — see [`./deployment-modes.md`](./deployment-modes.md) §7 (per-pod kubelet).

### 11.2 Plugin: `admin endpoint not ready: timed out`

The admin server's `/healthz` requires `transport.SupportsDiagnosticExec=true`. If the device's transport hasn't completed deferred-dial yet, healthz returns 503. Check:
```bash
kubectl logs -n <ns> -l app.kubernetes.io/instance=<device> | grep -i 'transport\|diag'
```

### 11.3 CRD: `phase=Failed reason=TransportUnsupported`

The device's transport doesn't implement DiagnosticExecer. Today this is gNMI only — switch the device to RESTCONF or NETCONF transport, or wait for the gNOI-equivalent capability to ship.

### 11.4 CRD: `phase=Completed` but `commands[*].output` is empty

Cisco IOS-XE returns an empty body for some commands that produce no actual output (`show running-config | section nothing`). If you expected output:
- Check `commands[*].err` for a per-command rpc-error or SSH parsing error
- Check `transportError` for a session-level failure
- Try `kubectl ciscovk exec <device> -- <command>` to bypass the reconciler and see the raw plugin behavior

### 11.5 CRD: `phase=Completed` but `Ready=False reason=TransportError` (SSH dial broken)

`phase` stays `Completed` (the reconcile loop ran) but the capture's `transportError` carries the SSH failure. Verify:
- `kubectl get iosxediag <name> -o jsonpath='{.status.results[-1:].transportError}'`
- The device has a CLI listener on port 22 (`show ip ssh` on the device)
- The pod's network can reach the device IP
- The user has privilege 15 (commands like `show running-config` need it)
- A `TransportError` Warning event was recorded: `kubectl get events --field-selector involvedObject.name=<crname>,reason=TransportError`

`phase=Failed` is reserved for the harder failure case where the device's transport doesn't implement DiagnosticExecer at all (today: gNMI), surfaced as `Ready=False reason=TransportUnsupported`.

### 11.6 CRD: Secret leaked despite `allowSecrets: false`

Filter pattern doesn't yet cover this Cisco syntax. See §8.2.

### 11.7 ConfigMap sink: ConfigMap not garbage-collected

Owner-reference cascade only works in same-namespace sinks (Kubernetes forbids cross-namespace OwnerReferences). For cross-namespace sinks, manual cleanup is required:
```bash
kubectl delete cm -n <sink-ns> -l cisco.vk/diagnostic=<crname>
```

### 11.8 Plugin or CRD: `destructive command "<x>" is not supported by exec`

By design — see §3.6 and the [destructive-ops RFC](./device-operations-rfc.md) for `clear`/`reload`/`write erase` paths.

---

## 12. Live-device evidence

Validated end-to-end against Cat9300 / IOS-XE 17.18.01 on 2026-04-28:

- One-shot `show version` / `show ip route summary` / `show ip ospf neighbor` — real output captured
- Default secret-redaction — zero hashes leaked across `username … privilege … secret 9 $...$`, `tacacs server`, indented `key tacacs123`
- `allowSecrets: true` — opt-out works; 4 username hashes visible
- Scheduled (30 s interval, `maxResults: 3`) — exactly 3 captures retained, oldest evicted
- ConfigMap sink — full 26 117-byte `show running-config` body in CM, 2 KiB inline preview
- Plugin `kubectl ciscovk exec` — `show clock`, `show version`, `show running-config | include username` all round-trip cleanly

Full capture YAMLs + run logs: [`./final/evidence/2026-04-28-live-c9300-diagnostics/`](./final/evidence/2026-04-28-live-c9300-diagnostics/SUMMARY.md).

---

## 13. Forward work

Items the diagnostics design RFC scopes but this guide does not cover today:

- **Cron-style schedules** — `spec.schedule.cron` is in the API but the reconciler currently rejects it with a clear error. Phase B-fu will land cron via `robfig/cron`.
- **APIService aggregation (Phase E)** — the diagnostics RFC's §3.3 Option B (replace port-forward + plugin with native `kubectl exec ciscodevice/<name>`). Deferred until real-world usage shows the port-forward UX is insufficient.
- **Cross-device fan-out** — `IOSXEDiagnosticBundle` (parallel to `IOSXEConfigBundle`). Operator-tracked but not in scope for the current diagnostics-rfc PR.
- **gNMI / gNOI integration** — when Cisco's gNMI surface ships an equivalent of `cli-exec` (or gNOI's `cli.exec`), the existing `DiagnosticExecer` interface absorbs it without engine-side changes.
- **Plugin subcommands beyond `exec`** — `diff`, `explain`, `replay`, `health` from the operator-CLI guide §13.6 roadmap.

For destructive operations (`clear ip ospf process`, `reload`, `write erase`) see [`./device-operations-rfc.md`](./device-operations-rfc.md).

---

## See also

- [`./diagnostics-rfc.md`](./diagnostics-rfc.md) — design RFC (Phases A–D delivery + the SSH-CLI architectural note in §11)
- [`./device-operations-rfc.md`](./device-operations-rfc.md) — destructive-ops sibling: `IOSXEMaintenance` (clears) and `IOSXEDeviceOp` (reload, write-erase)
- [`./transport-architecture.md`](./transport-architecture.md) — `transport.Interface` reference and the per-transport implementation notes
- [`./deployment-modes.md`](./deployment-modes.md) — per-mode setup; the per-pod-kubelet topology is what the diagnostics surface depends on
- [`./operator-cli-guide.md`](./operator-cli-guide.md) — kubectl reference for all cisco-vk CRDs (the `IOSXEDiagnostic` entry will land alongside this guide)
- [`./final/evidence/2026-04-28-live-c9300-diagnostics/SUMMARY.md`](./final/evidence/2026-04-28-live-c9300-diagnostics/SUMMARY.md) — live retest evidence
