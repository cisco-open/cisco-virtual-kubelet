# Tier-1 NETCONF dial probe — root-cause narrowing for #6(a)

**Date:** 2026-04-27
**Device:** C9K-4 — Cat9300-24P, IOS-XE 17.18.2 — at 198.51.100.103
**Branch tip:** `2571676` (probe diagnostic + deferred-dial + pre-flight TCP)

This bundle records the tier-1 narrowing experiment for finding
**#6(a) — from-pod NETCONF dial returns immediate EOF**. The bug is
the only remaining production-readiness blocker for in-pod NETCONF
transport on the per-pod kubelet topology.

---

## What we set out to determine

Whether the bug lives in:

- **Environment** (Cilium / network namespace / source-IP / device-side ACL) — ruled out
- **Upstream virtual-kubelet runtime** (kubelet API server, pod informer, etc.) — ruled out
- **cisco-vk apphosting RESTCONF poll loop** (high-frequency HTTP/2 keep-alives) — leading suspect
- **A specific code path in cisco-vk's NETCONF transport** (dialSSHNetconf wrappers) — leading suspect

---

## Method

Three tests, all on the same Cat9300 at the same wall clock:

| Test | Vehicle | Expected if env-side | Observed |
|---|---|---|---|
| **Probe pod** | Distroless pod with same SA, node, namespace, image base; runs raw `ssh.Dial` × 5 | Same outcome as cisco-vk | **5/5 SUCCESS** — banner reads cleanly |
| **In-process probe goroutine** | Goroutine inside cisco-vk pod using stripped-down `ssh.Dial` (no BannerCallback, 10 s timeout, no schema load) | Same outcome as configdriver | **SUCCESS on every 30-second tick after ~60-second startup window** |
| **Deferred-dial loop** | Goroutine inside cisco-vk pod using `transport.For` → `dialSSHNetconf` → `ssh.Dial` | Same outcome as in-process probe | **FAIL on every tick** (overflow + read-empty: EOF) |

The **probe pod** comparison rules out network, identity, image base.
The **in-process probe** comparison narrows the bug to *something in
the cisco-vk binary's call path* — same process, same goroutine
runtime, same x/crypto/ssh version.

The deferred-dial KEEPS failing while the in-process probe SUCCEEDS
on the same wall clock. Side-by-side log excerpt
([cisco-vk-pod.log](./cisco-vk-pod.log)):

```
13:28:18 config driver dial: FAIL — overflow + EOF (attempt #3)
13:28:38 netconf-probe:      OK   — banner reads cleanly
13:28:47 config driver dial: FAIL — overflow + EOF (attempt #4)
```

Both go through `ssh.Dial("tcp", "198.51.100.103:830", conf)`.
ClientConfigs differ only in `Timeout` (10 s vs 30 s) and the
*absence* of `BannerCallback` on the probe path.

---

## Two narrowing iterations

### Iteration 1 — drop `BannerCallback`

Hypothesis: the no-op `BannerCallback: func(string) error { return nil }`
in dialSSHNetconf was the only material difference vs the probe's
ClientConfig and might be triggering an x/crypto/ssh edge case.

Removing `BannerCallback` did NOT fix the deferred-dial path. Probe
still succeeds while configdriver still fails. **Hypothesis 1 falsified.**

### Iteration 2 — add the probe's pre-flight raw-TCP read

Hypothesis: the probe pod implementation does a raw `net.Dial` +
read + close BEFORE the `ssh.Dial`. Replicating this two-step shape
inside `dialSSHNetconf` should "warm up" the device-side connection
state.

Adding the pre-flight read inside `dialSSHNetconf` did NOT fix the
deferred-dial path either. **Hypothesis 2 falsified.**

---

## Where the investigation stands

The behavior reduces to: **two functionally-identical `ssh.Dial`
calls in the same process at the same instant produce different
device-side outcomes.** That isolates the root cause to one of:

- Some still-unidentified state difference between the two goroutines
  (TCP source-port allocation pattern, syscall serialisation order, …)
- A device-side per-source-quad or per-byte-pattern fingerprint that
  treats the two flows differently (very atypical for IOS-XE NETCONF)
- A subtle x/crypto/ssh internal state that resets after a successful
  banner read (the probe's pre-flight succeeds and probably perturbs
  some package-level state we haven't traced)

Bottoming this out further needs at least one of:

1. **Wireshark / tcpdump** on the cisco-vk pod's veth — capture the
   bytes both calls send/receive, side by side. Most informative
   single experiment; not feasible from this session's tooling.
2. **GODEBUG=schedtrace** or **runtime/pprof goroutine snapshot**
   right at the moment of dial — confirms or rules out goroutine
   scheduling involvement.
3. **A binary built with x/crypto/ssh@v0.50.0** (vs the repo's
   v0.47.0). Earlier reproducer in the v8 evidence bundle showed
   v0.50.0 also reads the banner cleanly from the host; rebuilding
   the cisco-vk binary against v0.50.0 might surface or rule out a
   v0.47-specific code path.

---

## What ships in the production binary on `:v18`

Even without root cause, the operator-visible behavior is now well-
contained:

- **Deferred-dial loop**: pod startup attempts the dial once, then
  retries every 30 s in a background goroutine. Cancelled by ctx.
- **Atomic transport slot** on `provider.ConfigReconciler` — the
  reconciler sees the freshest transport on every Reconcile tick the
  moment the deferred dial succeeds, no operator intervention.
- **Banner-peek diagnostic** on the dial-error wrap — surfaces hex
  + ASCII of the actual bytes the device sent (or sentinels like
  `read-empty: EOF`, `dial-failed: <err>`) directly in the wrapped
  error so the next operator hit produces actionable data without
  needing tcpdump.
- **Pre-flight raw-TCP read** in `dialSSHNetconf` — partial mitigation
  inspired by the probe's working pattern; harmless when the dial
  works, unhelpful when it doesn't.
- **CONFIG_NETCONF_PROBE diagnostic env** — operators can flip an
  in-process probe goroutine on without rebuilding the image.

So the production posture for in-pod NETCONF transport on a
Cat9300 / IOS-XE 17.18.2 today is:

- The first dial attempt at pod start fails (race window).
- Subsequent deferred-dial attempts at 30-second cadence also fail
  in this lab as long as the wrapped-call-stack path is in use.
- The aggregator-mode topology (`DISABLE_IN_POD_CONFIG_RECONCILER=1`
  on the per-pod kubelet, configdriver runs in a dedicated aggregator
  pod that does NOT host apphosting) **bypasses this code path
  entirely** and is the recommended production setting for
  Wave-10 NETCONF testing while #6(a) remains open.

The full RESTCONF test suite (tests 02, 06, 07, 11) passes
end-to-end on `:v18` with verify.sh green — separately validated in
[`../2026-04-27-live-c9300-v12-production-ready/`](../2026-04-27-live-c9300-v12-production-ready/).

---

## Files in this bundle

- [`captured.txt`](./captured.txt) — wall-clock timestamp of evidence capture
- [`standalone-probe.log`](./standalone-probe.log) — netconf-probe pod's 5/5 successes
- [`cisco-vk-pod.log`](./cisco-vk-pod.log) — cisco-vk pod's deferred-dial failures + in-process probe successes, side-by-side wall clock

---

## Index of fixes the experiment produced

| Commit | Fix |
|---|---|
| `62b00c1` | `cmd/cisco-vk/netconf_probe.go` — in-process diagnostic gated on `CONFIG_NETCONF_PROBE` |
| `2b2ce19` | `internal/controller/ciscodevice_controller.go` — forward `env.*` labels (initial; ultimately not used due to spec.labels YAML decoding edge case) |
| `5982359` | `provider.ConfigReconciler.SetTransport / GetTransport` + deferred-dial goroutine |
| `98e9475` | `dialSSHNetconf` — drop `BannerCallback` |
| `3c047a6` | retryConfigDriverDial bypasses `LoadYANGReleaseTags` schema load |
| `2571676` | `dialSSHNetconf` — pre-flight raw-TCP read |
