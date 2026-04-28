# Diagnostics-RFC live-device evidence — Cat9300 / IOS-XE 17.18.1

**Date:** 2026-04-28
**Device:** cat9k-smoke (Cat9300, IOS-XE 17.18.01) at 10.1.1.1
**Cluster:** kind-kind, namespace `cisco-vk-smoke`
**Image:** `localhost:5001/cisco-vk:phase9-crb-fix` — branch `pr/johalley/ciscoconfig_xe`
**Branch tip at retest:** the Phases-A-D series commits + this session's three follow-up commits (SSH-CLI helper, secret-regex tightening, plugin port-forward fix, ConfigMap NamePrefix regex relaxation)

## Result

| # | Test | Outcome |
|---|---|---|
| 1 | One-shot diagnostic — `show version`, `show ip route summary`, `show ip ospf neighbor` | ✅ phase=Completed; real Cisco show output captured (IOS-XE 17.18.01 banner present) |
| 2 | Default secret-redaction — `show running-config \| include enable\|username\|...` | ✅ `redacted=true`, zero hashes leaked (after tightening — first round had 4 leaks; root cause + fix recorded below) |
| 3 | `spec.allowSecrets: true` opt-out | ✅ `redacted=false`, all 4 `username … secret 9 $...$…` hashes visible |
| 4 | Scheduled diagnostic + retention trim — `show clock` every 30 s, `maxResults: 3` | ✅ exactly 3 captures retained; oldest evicted; `nextCapture` populated |
| 5 | ConfigMap output sink — full `show running-config` | ✅ ConfigMap `running-snapshot-20260428-065338` carries full 26 117-byte body; inline preview clipped to 2 KiB; `redacted=true` (filter survives the sink path) |
| 6 | `kubectl ciscovk exec` plugin against the live admin endpoint | ✅ port-forward + POST `/v1/exec` returns formatted output with `# device=cat9k-smoke transport=netconf captured=...` header; `--allow-secrets` flag bypasses redaction; multi-command invocations work |

## Discoveries forced by the live retest

Three production-code fixes landed during this session because the unit-test fixtures mocked away device-side behaviour that turned out to matter on real IOS-XE.

### 1. `cli-exec` is gone in 17.18.x

The Phase-A code shipped with the assumption (carried over from older Cisco docs) that the Cisco-IA NETCONF agent exposed a `<cli-exec>` element parallel to the still-working `<cli-config-data>`. The retest's first apply got `rpc-error: unknown-element <bad-element>cli-exec</bad-element>`. A YANG-bundle search confirmed: `cli-exec` was a ConfD built-in (no YANG model), tightened off in 17.18 even though the namespace `http://cisco.com/yang/cisco-ia` still services config writes via `cli-config-data`.

### 2. `Cisco-IOS-XE-cli-rpc:config-ios-cli-rpc` accepts the input but its `result` is just status

Switched to the model-validated `config-ios-cli-rpc` (input leaf `config-clis`, output leaf `result`). Initial leaf name `clis` (sibling RPC's leaf) gave `unknown element: clis`. Once corrected to `config-clis`, the device accepted the request — but `result` came back as the single string "RPC request successful", not the actual show output. The RPC was designed for config writes, not exec capture. There is no YANG-modelled RPC in IOS-XE 17.18.x that returns textual show output.

### 3. SSH-CLI is the architecturally correct surface

Implemented `runShowCommandsViaSSH` ([`internal/drivers/iosxe/configdriver/transport/ssh_cli.go`](../../../../internal/drivers/iosxe/configdriver/transport/ssh_cli.go)) that opens a vt100 PTY shell on port 22, disables pagination (`terminal length 0` / `terminal width 0`), runs each command, and parses output between the command echo and the next prompt. Both `netconfTransport.DiagnosticExec` and `restconfTransport.DiagnosticExec` now delegate to this helper — same credentials as the configdriver session, no new auth surface. Mirrors what `gnoi cli.exec` does on platforms that ship gNOI.

### 4. Secret-redaction regex was too narrow

Test 2's first run leaked all four `username <name> privilege 15 secret 9 $...$…` hashes plus the indented `key tacacs123` line. Root cause: the old regex required `secret|password` to immediately follow the username (`username\s+\S+\s+(secret|password)\b`) but Cisco IOS-XE optionally inserts `privilege <N>` between them. Tightened to `username\s+\S+\b.*\b(secret|password)\b`. Also added patterns for `tacacs server <name>`, `radius server <name>`, and indented `key <token>` lines under those stanzas.

### 5. `kubectl port-forward` doesn't accept label selectors

The plugin's first try invoked `kubectl port-forward pod -l <selector> :8082` which kubectl rejects (port-forward takes a concrete pod name). Fixed: plugin now resolves the pod name via `kubectl get pod -l … -o jsonpath={.items[0].metadata.name}` first, then runs port-forward against that name.

### 6. `NamePrefix` validation regex was over-strict

The CRD's `outputSink.configMapRef.namePrefix` field rejected trailing dashes (`running-snapshot-`), but the reconciler appends a timestamp suffix immediately so a trailing dash yields a clean separator (`running-snapshot-20260428-065338`). Relaxed the regex to allow trailing dash.

## Files in this bundle

| File | Contents |
|---|---|
| `t1-basic-shows.yaml` | Test 1 IOSXEDiagnostic CR with `show version` / `show ip route summary` / `show ip ospf neighbor` results (real IOS-XE output) |
| `t2-redaction-default.yaml` | Test 2 CR with `redacted=true` and every secret-bearing line replaced by the redaction marker |
| `t3-allow-secrets.yaml` | Test 3 CR with `allowSecrets=true` and four `username … secret 9 $...$…` hashes left visible (per design) |
| `t4-schedule.yaml` | Test 4 CR with three retained captures, `nextCapture` populated, deterministic 30 s intervals |
| `t5-cm-sink.yaml` | Test 5 CR with inline 2 KiB preview + `configMapRef` pointing at the captured ConfigMap |
| `t5-cm-sink-configmap.yaml` | The full 26 117-byte `show running-config` body the ConfigMap holds |
| `t6-plugin.txt` | Three `kubectl ciscovk exec` invocations: `show clock`, `show version`, `show running-config \| include username` (default redaction + `--allow-secrets` opt-out) |

## What this evidence proves

- Phase A's `DiagnosticExecer` interface contract is correct; the implementation behind it is now SSH-CLI rather than the originally-assumed `cli-exec` RPC, but the surface is unchanged and writers can still depend on it.
- Phase B's reconciler runs end-to-end on real IOS-XE: state machine, scheduling, retention trim, and (with the tightened regex) the secret-redaction default-on path are all operational.
- Phase C's admin server + plugin work in production: pods/portforward RBAC is the auth gate, `--allow-secrets` propagates as expected, and the multi-command output formatting is operator-friendly.
- Phase D's ConfigMap sink correctly fans full outputs out of CR status while preserving an inline preview AND the secret-redaction filter.
- Phase E remains explicitly deferred — no new evidence suggests the port-forward UX is insufficient.

## Cleanup

All test CRs (`t1`-`t5`) and the `running-snapshot-…` ConfigMap remain in the cluster for inspection. To clear:

```
kubectl delete iosxediag --all -n cisco-vk-smoke
kubectl delete cm -n cisco-vk-smoke -l cisco.vk/diagnostic
```
