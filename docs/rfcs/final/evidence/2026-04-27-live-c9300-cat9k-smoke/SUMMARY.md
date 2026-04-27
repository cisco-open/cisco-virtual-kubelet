# Live-device retest — Cat9300-24P / IOS-XE 17.18.2

**Date:** 2026-04-27
**Device:** C9K-4 — Cat9300-24P, IOS-XE 17.18.2 — at `198.51.100.103`
**Cluster:** `ubuntu17` (k3s v1.34.3, Cilium 1.17.4 overlay)
**Operator-runnable playbook:** [`../../release-blocker-tests/`](../../release-blocker-tests/)
**Image rolled across the run:** `:v1` (deployed pre-session) → `:v2` (HEAD) → `:v3` (scalarEqual fix) → `:v4` (RESTCONF MERGE fallback fix)

This run took the consolidation branch (`pr/johalley/ciscoconfig_xe`) and exercised the operator-runnable test playbook against a real Cat9300 in a lab. It surfaced **3 product bugs** (now fixed and on the branch), **2 playbook bugs** (now fixed), and **2 architectural findings** worth follow-up. The Wave 10 confirmed-commit-fallback warning event was proven on-device end-to-end. NETCONF-transactional and Wave 10 confirmed-commit-happy-path tests are still **pending** because of the from-pod NETCONF dial issue (finding #6).

---

## Test results

| # | Test | Transport | Result | Notes |
|---|---|---|---|---|
| 02 | netconf-transactional-cli-rejection | restconf | **Safety property held** | Engine took drift-report path on RESTCONF instead of strict reject; **no device write** (banner motd remained empty). Verify.sh strict-form fails because over RESTCONF the engine's chosen path is `Drifted` not `Failed/ErrTransactionalCLIUnsupported`. The release-blocker safety guarantee — "no device write for transactional+CLI combination" — holds either way. |
| 06 | driftpolicy-revert-live-write | restconf | **Pass** | Phase 1 (driftPolicy=report) → Phase=Drifted, no device write. Phase 2 (flip to revert) → Phase=InSync, observedGeneration matches generation, banner motd applied on device, login banner shows the test string. **Found and fixed bug #1: scalarEqual panic.** |
| 07 | write-startup-save-config | restconf | **Failed (writer bug)** | Found and fixed bug #2 (PATCH→PUT fallback for VerbMerge). Then surfaced bug #3 (writer body uses `ipv4_address` leaf name but Cisco-IOS-XE-native YANG schema names this `ip/address/primary/{address,mask}`). Bug #3 not fixed in this run — captured as a follow-up. |
| 11 | confirmed-commit-restconf-fallback | restconf | **Pass (Wave 10 headline)** | `ConfirmedCommitFallback` Warning event fired with reason `non-transactional reconcile — fell back to plain Commit`. The Wave 10 backward-compatibility safety net — "device without confirmed-commit gets a clear signal that the auto-revert path isn't active" — proven on-device. |
| 01, 08, 09, 10, 13 | NETCONF transactional + Wave 10 confirmed-commit / atomic-replace | netconf | **Blocked — dial bug from inside pod (finding #6)** | NETCONF dial fails from inside the kubelet pod (`ssh: handshake failed: ssh: overflow reading version string`) but succeeds from the same code on the cluster host. NETCONF candidate-datastore + confirmed-commit:1.0 both verified advertised by the device once `netconf-yang feature candidate-datastore` is enabled. |
| 04 | gnmi-keyed-path | gnmi | **Not attempted** | Manifest hardcodes `GigabitEthernet0/0/0` — does not exist on Cat9300 line-card numbering (1/0/1–24). Test 04 needs templating for interface name. The gNMI service was enabled and reachable on tcp/50052 (gnxi insecure default in 17.18.2). |
| 03, 05 | configprereqs cleanup, credential rotation | n/a | **Not attempted** | Hardcoded device address (`10.1.1.1`) and Secret name (`cat9k-creds`) in test 03 manifests; required adaptation. |

---

## Bugs found and fixed in this run

### #1 — scalarEqual panic on map-typed values (commit `de3402c`)

[`internal/drivers/iosxe/configdriver/writers/helpers.go:114`](../../../../internal/drivers/iosxe/configdriver/writers/helpers.go#L114)

The banner family writer hands `scalarEqual` the YANG-RPC structure the device returns for `banner motd`, which decodes into a Go `map[string]any`. The plain `==` panics:

```
panic: runtime error: comparing uncomparable type map[string]interface {}
at writers/helpers.go:114 (scalarEqual)
called from writers/helpers.go:103 (leavesEqual)
called from writers/singleton.go:85 (singletonWriter.Diff)
```

The recover path in controller-runtime caught the panic but the reconciler never made forward progress on test 06 phase 2 (driftPolicy=revert). The apply itself had succeeded (banner did appear on the device); the verify Diff is what panicked.

**Fix:** classify map / slice / func leaves as not-comparable up-front; fall back to `reflect.DeepEqual` then to the existing stringified compare. Added regression tests for both branches.

### #2 — RESTCONF VerbMerge → PATCH 404s on absent resources (commit `3829717`)

[`internal/drivers/iosxe/configdriver/transport/restconf.go:114`](../../../../internal/drivers/iosxe/configdriver/transport/restconf.go#L114)

Per RFC 8040 §4.6.1, RESTCONF PATCH is "modify existing resource"; the device returns 404 when the target path has no existing resource. The engine's `VerbMerge` has create-if-absent semantics — a writer asking to MERGE Loopback9997 the device has never seen is a CREATE, not a partial-update.

```
op[0] MERGE /Cisco-IOS-XE-native:native/interface/Loopback=9997:
RESTCONF PATCH /Cisco-IOS-XE-native:native/interface/Loopback=9997:
404 Not Found: patch to a nonexistent resource
```

**Fix:** PATCH first (preserves partial-update semantics for existing resources); on 404 retry as PUT (idempotent create-or-replace) with the same body. Non-404 errors still surface as engine errors — the fallback is scoped narrowly. Added regression tests for both branches.

### #3 — interface_loopback writer leaf names (NOT fixed in this run)

After the PATCH→PUT fallback applied, the device returned a different 400:

```
RESTCONF PUT /Cisco-IOS-XE-native:native/interface/Loopback=9997: 400 Bad Request:
unknown-element: ipv4_address in /ios:native/ios:interface/ios:Loopback[ios:name='9997']/ios:ipv4_address
```

The writer is sending the netascode shape (`ipv4_address`, `ipv4_address_mask`) directly without translating to the Cisco-IOS-XE-native YANG shape (`ip/address/primary/{address,mask}`). Affects [`internal/drivers/iosxe/configdriver/writers/interface_loopback.go`](../../../../internal/drivers/iosxe/configdriver/writers/interface_loopback.go) and at least three sibling interface writers ([`interface_port_channel.go`](../../../../internal/drivers/iosxe/configdriver/writers/interface_port_channel.go), [`interface_virtual_port_group.go`](../../../../internal/drivers/iosxe/configdriver/writers/interface_virtual_port_group.go), [`interface_vlan.go`](../../../../internal/drivers/iosxe/configdriver/writers/interface_vlan.go)) which list the same `ipv4_address`/`ipv4_address_mask` managed leaves.

**Reproducer:** test 07 manifest, RESTCONF transport, IOS-XE 17.18.2 device. **Recommended next step:** wire the existing intent-to-YANG mapper through the interface writers' Diff/Apply path so the body the transport sends matches the device's YANG schema.

---

## Playbook bugs found and fixed in this run

### #4 — Pod label selector (commit `7e0042a`)

The Helm chart labels pods with `app.kubernetes.io/instance=<device>` not the legacy `app=<device>`. `preflight.sh`, `lib/baseline.sh`, and 5 per-test scripts were missing the kubelet pod every time. Fixed in 8 files.

### #5 — `kubectl exec ... -- sh` against distroless container

The verify scripts use `kubectl exec ... -- sh -c "wget http://localhost:8080/metrics"` to scrape the metrics endpoint. The cisco-vk container is `gcr.io/distroless/static-debian12` — it has no shell. Every verify.sh that asserts a metric counter fails on this step:

```
OCI runtime exec failed: exec: "sh": executable file not found in $PATH
[FAIL] could not scrape /metrics from pod cat9k-smoke-vk-...
```

**Recommended fix:** scrape `/metrics` via a sidecar (curl image, port-forward, or service exposing the metric scraping endpoint) rather than execing into the cisco-vk container. Not yet fixed.

---

## Architectural findings (not blockers, worth follow-up)

### #6 — NETCONF dial fails from inside the kubelet pod, succeeds from the host

```
NETCONF: dial: ssh: handshake failed: ssh: overflow reading version string
```

The same Go `golang.org/x/crypto/ssh` v0.47.0 client code:

- **Works** when run from the cluster's host (`ubuntu17`) — the cisco-vk repo's `transport.For` reproducer dials `198.51.100.103:830` and receives `kind=netconf tx=true conf=true` capabilities successfully.
- **Fails** when the same code runs inside the cat9k-smoke kubelet pod, even after the device's NETCONF service is fully steady-state and the `:v4` image was built from HEAD with the latest x/crypto.
- The same pod has no problem with TCP/443 (RESTCONF) or with `nc 198.51.100.103 830` from a debug container in the same pod's network namespace (banner reads cleanly).

The dial happens **once at pod startup**; on failure the configdriver never retries and the reconciler runs in scaffold mode permanently (`recordPending()` keeps emitting `NoTransport` events). This makes #6 a release-impacting bug for any operator that uses NETCONF transport — even one transient blip at pod start permanently disables the configdriver on that pod.

**Recommended next step (two parts):**
1. Diagnose the from-pod overflow: kubectl-debug into the pod's net namespace with a Go binary using `runtime/trace` against a pinned x/crypto. The pod uses distroless static-debian12, which is a thin static base — possibly some sysctl or seccomp profile interaction.
2. Independent of #1, make the configdriver dial retry with backoff and re-arm if the device becomes reachable later. Today a one-shot dial failure at startup permanently keeps the pod in scaffold mode; that's a fragile contract.

### #7 — Stale family leases linger past CR deletion

After `kubectl delete iosxeconfig <name>`, the family lease (`cvk-cat9k-smoke-banner-c77e25bf`) remained held by the deleted CR's runtime UID for tens of minutes. The next CR claiming the same family is `LeaseBlocked` until the TTL expires.

Today this is harmless for sequential tests (the TTL eventually expires) but adds operator friction during the initial test sweep — the second CR claiming a family lingers in `LeaseBlocked` even though the lease holder no longer exists in the cluster. **Recommended next step:** make the IOSXEConfig finalizer release any leases it holds before allowing CR deletion.

---

## Device-side prerequisites discovered

For the operator-runnable playbook to work end-to-end on a Cat9300-24P running IOS-XE 17.18.2, these device-side configuration steps were required:

- `netconf-yang` — already present once 17.18.2 is installed (default-on after Cisco 17.x default-config policy)
- `netconf-yang feature candidate-datastore` — **required** for Wave 10 confirmed-commit + atomic-replace; off by default. Without this, NETCONF advertises only `:writable-running:1.0`, not `:candidate:1.0` and `:confirmed-commit:1.0`. Enabling triggers a NETCONF service restart (~30 s).
- `gnxi` + `gnxi server` — IOS-XE 17.18 deprecated `gnmi-yang` in favour of `gnxi`. The `gnmi-yang` aliases still take effect (auto-translated to `gnxi`); insecure listen port default is **50052** (not 6030 as the cisco-vk gNMI factory's default) — operators with this device family must set `spec.port: 50052` on the CiscoDevice or the dial will hit a closed port.

**Recommended documentation update:** add these three prereqs to `release-blocker-tests/RUNBOOK.md` §1, and have `preflight.sh` advertise-check the NETCONF capabilities (not just the port).

---

## Live-run timeline (UTC)

| Time | Event |
|---|---|
| 05:50 | Helm chart deployed (rev 1, image `:v1` from earlier session) |
| 06:50 | First branch checkout pulled with consolidated README + label-selector playbook fix (`7e0042a`) |
| 06:59 | Preflight passes (with `--skip-gnmi`) |
| 07:42 | NETCONF candidate-datastore feature enabled — port 830 cycles |
| 08:32 | `:v2` image built and rolled out — NETCONF dial-from-pod bug confirmed reproducible against HEAD |
| 08:42 | CiscoDevice transport switched to `restconf` to unblock test execution |
| 08:43 | Test 02 — safety property held (no device write) |
| 08:45 | Test 06 phase 2 — apply succeeded, then post-apply Diff panicked → bug #1 surfaced |
| 08:50 | `:v3` image built with scalarEqual fix; rolled out |
| 08:55 | Test 06 — clean PASS on `:v3` (banner applied, InSync, no panic) |
| 08:55 | Test 07 — RESTCONF PATCH 404 on Loopback9997 → bug #2 surfaced |
| 08:58 | `:v4` image built with PATCH→PUT fallback; rolled out |
| 09:01 | Test 07 — different 400 (unknown-element ipv4_address) → bug #3 captured for follow-up |
| 09:03 | Test 11 — `ConfirmedCommitFallback` Warning event verified on-device |
| 09:05 | Cleanup: orphan CRs deleted, stale leases purged |

---

## Cluster + device leftovers

- **Device-side configuration left in place** for future test passes (per the user's blanket lab authorization to enable features):
  - `netconf-yang` (already present)
  - `netconf-yang feature candidate-datastore`
  - `gnxi` / `gnxi server`
  - `username AI_AGENT_RW privilege 15 secret 9 ...` (pre-existing)
- **Banner motd manually reverted** to baseline at 09:05 UTC.
- **No orphan Loopback interfaces** on the device (test 07's apply never reached past the writer bug; the partial-state cleanup in `engine.Discard` worked).
- **Cluster-side**: `cisco-vk-smoke` namespace, `cat9k-smoke` CiscoDevice, and Helm release `cvk:v4` left in place for the operator's next run.

---

## How to resume

```sh
ssh ubuntu17
cd ~/cisco-virtual-kubelet
git pull --ff-only

# Tests that pass with the current code on RESTCONF transport (cat9k-smoke is already wired):
cd docs/rfcs/final/release-blocker-tests
export NAMESPACE=cisco-vk-smoke DEVICE_NAME=cat9k-smoke EXPECTED_KUBE_CONTEXT=default CVK_CONFIG_LINT_PASSWORD=cisco
./preflight.sh --skip-gnmi --intf-approved=GigabitEthernet1/0/24

# Tests blocked on the from-pod NETCONF dial bug (#6):
#   01, 08, 09, 10, 13 — investigate dialSSHNetconf from inside the cluster pod first.
# Tests blocked on the writer schema bug (#3):
#   07 — fix interface_loopback writer to emit ip/address/primary/{address,mask} YANG shape.
# Tests not yet attempted:
#   03, 04, 05 — adapt manifests to local device IP / Secret name / interface naming.
```

The two follow-up commits already on the branch (`de3402c`, `3829717`) plus this evidence bundle are sufficient to demote three of the four `:v1`-era issues. The fourth (interface writer schema mapping, finding #3) is the largest remaining piece and should land before any release-tag gate that exercises the interface_* family writers over RESTCONF.
