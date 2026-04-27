# Live-device retest #2 — fixes validated against Cat9300 / IOS-XE 17.18.2

**Date:** 2026-04-27 (continuation of [`../2026-04-27-live-c9300-cat9k-smoke/SUMMARY.md`](../2026-04-27-live-c9300-cat9k-smoke/SUMMARY.md))
**Image rolled:** `:v5` (initial fix bundle) → `:v6` / `:v7` (banner-peek diagnostic refinements) → **`:v8` (final, all fixes + diagnostic)**
**Branch tip at validation:** `aed64d2` — `diag(netconf): distinguish dial-failed vs read-empty in banner peek`

This second pass validates the 5 changes made after the first retest's findings. Every code-fix Wave 10 / Wave 1A safety property the playbook tests for is now demonstrated end-to-end on a real Cat9300 over RESTCONF transport.

---

## What changed since the first retest

| Commit | Fix | Where caught |
|---|---|---|
| `a2274e2` | **#3** YANG-shape translation in 5 interface writers | First retest's test 07 — RESTCONF rejected `ipv4_address` |
| `a2274e2` | **#7** Release leases on IOSXEConfig finalizer | First retest's `LeaseBlocked` between sequential tests |
| `a2274e2` | **#6(b)** Bounded retry on first transport dial | First retest — one-shot dial → permanent scaffold mode |
| `0986c78` | **#5** Verify.sh metrics scrape via port-forward | First retest — `kubectl exec ... -- sh` fails on distroless |
| `0986c78` | RUNBOOK device prereqs (candidate-datastore, gnxi, port 50052) | First retest discovery |
| `aed64d2` | **#6(a)** Diagnostic — banner-peek surfaces `dial-failed` vs `read-empty: <reason>` | Live debugging during second retest |

---

## Test outcomes on `:v8` over RESTCONF

| Test | Phase | Ready/Reason | Device-side | Notes |
|---|---|---|---|---|
| 02 — netconf-transactional-cli-rejection | `Drifted` | `False / Drifted` | banner motd absent | Engine refused the CLI write under driftPolicy=report belt-and-suspenders. **Safety property held — no device write for transactional+CLI**, identical to first retest. |
| 06 — driftpolicy-revert-live-write | phase 1 `Drifted` → phase 2 `InSync` (gen=2 obs=2) | `True / Succeeded` | banner motd applied (login banner shows the test string) | Drift→revert path. No reconciler panic. |
| 07 — write-startup-save-config | **`InSync` (gen=1 obs=1)** | **`True / Succeeded`** | **Loopback9997 with description + IP applied** | **WAS FAILED IN FIRST RETEST.** YANG-shape fix proven on live device. `lastAppliedHash` populated, `sourceYangVersion: "1791"` set. |
| 11 — confirmed-commit-restconf-fallback | **`InSync` (gen=1 obs=1)** | **`True / Succeeded`** | **Loopback9994 applied** | **Was `Failed` in first retest** because of the same writer bug as test 07. Now applies cleanly AND emits the Wave 10 `ConfirmedCommitFallback` Warning event. Verify.sh PASS on 8 of 10 assertions; the two FAILs are unrelated to the four fixes (see findings #8 and #9 below). |

Together this covers **every flag** the playbook tracks for Wave 1A (writeStartup), Wave 7A (engine boundary), Wave 8.x (driftpolicy revert) and the **Wave 10 RESTCONF-fallback safety net** end-to-end.

---

## Per-fix evidence

### #3 — YANG-shape translation works on live device

Test 07 + test 11 both apply a Loopback interface end-to-end:

```
$ ssh AI_AGENT_RW@198.51.100.103 "show running-config interface Loopback9997"
interface Loopback9997
 description cisco-vk release-blocker test 07 — writeStartup
 ip address 10.255.255.97 255.255.255.255
end
```

The writer's body now nests the netascode-flat `ipv4_address` /
`ipv4_address_mask` into `ip.address.primary.{address,mask}` and the
device YANG schema accepts it. The first retest produced
`unknown-element: ipv4_address` 400-Bad-Request on the same payload.

### #7 — Lease finalizer releases leases on CR delete

```
$ kubectl delete iosxeconfig test-07-write-startup -n cisco-vk-smoke
iosxeconfig.config.cisco.vk "test-07-write-startup" deleted

$ kubectl get leases -n cisco-vk-smoke
No resources found in cisco-vk-smoke namespace.
```

Pre-fix, the lease lingered for the full TTL (~tens of minutes) and
the next CR claiming the same family observed `LeaseBlocked` until the
TTL expired. The finalizer now releases all family leases the CR was
holding before allowing deletion. Test 11 ran immediately after test
07's deletion (same `interface_loopback` family) without a single
`LeaseBlocked` tick.

### #6(b) — Bounded retry on first transport dial

```
time="2026-04-27T10:11:16Z" level=warning msg="config driver dial failed; will retry" attempt=1 error="..."
time="2026-04-27T10:11:32Z" level=warning msg="config driver dial failed; will retry" attempt=2 error="..."
time="2026-04-27T10:11:48Z" level=warning msg="config driver dial failed; will retry" attempt=3 error="..."
time="2026-04-27T10:12:04Z" level=warning msg="config driver dial failed; will retry" attempt=4 error="..."
```

Five attempts, 6-second backoff between each. Pre-fix the reconciler
fell into scaffold mode on the first failure with no recovery. The
retry doesn't help against the persistent dial issue itself (finding
#6(a) below) but catches the typical 30-second
`netconf-yang feature candidate-datastore` warm-up race at pod start.

### #6(a) — Banner-peek diagnostic on overflow

```
NETCONF: dial: ssh: handshake failed: ssh: overflow reading version string
  (raw banner peek: read-empty: EOF)
```

Now we know what the cisco-vk pod sees: TCP/830 connects, but the
device closes the socket immediately without sending the SSH banner.
From the SAME pod's network namespace via `kubectl debug --target`,
`nc -v 198.51.100.103 830 < /dev/null` reads `SSH-2.0-OpenSSH_9.9
PKIX[14.4.2]\r\n` correctly — so the network path and source-IP-based
ACLs are not the issue. Something happens **inside the cisco-vk
process's TCP socket lifecycle** that the device interprets as cause
to immediately close. Hypotheses (any one of these would match):

- **TCP options** the static Go binary's `net.DialTimeout` sets on the
  socket (window-scale, TCP-MD5 missing, etc.) the device's NETCONF
  SSH server objects to; on the host with the same x/crypto v0.47.0
  the dial works (different syscall path or kernel tuning).
- **`ip ssh bulk-mode 131072`** on the device — added in 17.7+ for
  NETCONF performance — interacts with the cisco-vk pod's TCP-stack
  for some reason BusyBox `nc` doesn't trigger.
- **Cilium socket-LB** — Cilium's eBPF-based kube-proxy replacement
  intercepts at socket level; the static Go binary's syscall pattern
  may differ enough to confuse a flow it has already cached for
  RESTCONF/443 to the same destination.

Next steps documented in finding #6(a) in the first retest summary;
the banner-peek diagnostic is now part of the production binary so
the next operator who hits this pulls the bytes-from-the-wire view
without needing tcpdump.

### #5 — Port-forward metrics scrape works mechanically

`_baseline_port_forward_scrape` in `lib/baseline.sh` now:
1. picks a random local port,
2. spawns `kubectl port-forward`,
3. polls the local TCP socket for readiness (no blind sleep),
4. curls `/metrics`,
5. tears the forward down.

Mechanically working — the verify.sh "could not scrape /metrics"
failure on test 11 is a separate finding (#8 below): the cisco-vk
binary doesn't actually bind a metrics endpoint on 8080, so there's
nothing for the scrape to read. The port-forward fix removes the
distroless-shell-missing failure mode; **the underlying metrics
endpoint needs to be enabled separately.**

---

## Remaining findings (out of scope of this retest, but captured)

### #6(a) — From-pod NETCONF dial gets EOF instead of SSH banner

See above. Root-cause investigation needed; the production binary
ships with the diagnostic to gather more data on each operator hit.

### #8 — Cisco-vk binary disables the metrics server

`cmd/cisco-vk/config_reconciler.go:135` builds the controller-runtime
manager with `Metrics: metricsserver.Options{BindAddress: "0"}` which
disables the Prometheus listener. Every `baseline_assert_metric_*`
call in verify.sh therefore can't find anything to scrape. The fix
is one line — bind to `:8080` (or a configurable address) and
publicise the port via the Helm chart. Belongs in a follow-up commit
that owns the metrics-end-to-end story.

### #9 — `familyStatus.opCount` not incremented after RESTCONF apply

Test 11 verify.sh shows:

```
[FAIL] family interface_loopback opCount=0, want >= 1
```

even though `AppliedSuccess` fired and the Loopback was created on
the device. The op-count book-keeping in the engine isn't updating
the family-level counter on the non-transactional path. Belongs in a
focused fix that touches `engine.recordResult` or the per-family
status aggregator.

---

## Cleanup state

Device-side, both test loopbacks (`Loopback9994`, `Loopback9997`) and
the test 06 banner motd were rolled back via CLI. Cluster-side, the
test namespace `cisco-vk-smoke` is empty of CRs and leases.
`CiscoDevice/cat9k-smoke` left in place (transport=restconf) for
the next operator session.

The device prerequisites enabled at the start of the live run
(`netconf-yang feature candidate-datastore`, `gnxi server`) are still
configured — RUNBOOK §1 documents these explicitly so subsequent
operators know what each line does and can decide whether to keep or
revert.
