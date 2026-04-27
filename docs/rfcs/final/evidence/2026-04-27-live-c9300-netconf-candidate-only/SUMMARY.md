# NETCONF candidate-only — live-device evidence

**Device:** cat9k-smoke (10.1.1.1, C9300-24UX, IOS-XE 17.18.2)
**Date:** 2026-04-27
**Image:** v29 — `localhost:5001/cisco-vk:phase9-crb-fix`
  config sha256:1757... (manifest list 27c50b...) — local kind cluster
**Branch:** `pr/johalley/ciscoconfig_xe` @ commit `88ac685`
**Device-side prereqs at runtime:** `netconf-yang` + `netconf-yang feature candidate-datastore` enabled, `restconf` + `ip http secure-server` enabled (apphosting still uses RESTCONF).

## Result

| Test | Family | Phase | Notes |
|---|---|---|---|
| 06 — driftpolicy revert (banner) | banner | InSync | phase=InSync, family=InSync, generation=2 == observedGeneration |
| 07 — write-startup save-config (Loopback9997) | interface_loopback | InSync | phase=InSync, `SaveStartupOK` event present |
| 11 — confirmed-commit fallback | interface_loopback | **DEFERRED** | The cluster's installed CRD predates the Wave-10 chart bump, so `spec.confirmTimeoutSeconds` is rejected with `unknown field`. The chart's CRD at [`charts/cisco-virtual-kubelet/crds/config.cisco.vk_iosxeconfigs.yaml`](../../../../charts/cisco-virtual-kubelet/crds/config.cisco.vk_iosxeconfigs.yaml) already carries the field — applying it unblocks test 11. Operator-scheduled action, not a NETCONF candidate-only product issue. |

## Metric proofs

```
cisco_vk_config_mutate_ops_total{device="cat9k-smoke",transport="netconf",verb="MERGE"}       1
cisco_vk_config_transactions_total{device="cat9k-smoke",outcome="commit",transport="netconf"} 27
cisco_vk_config_transactions_total{device="cat9k-smoke",outcome="start_failed",transport="netconf"} 2
cisco_vk_config_save_startup_total{device="cat9k-smoke",outcome="ok",transport="netconf"}     27
```

The `start_failed` count reflects two transient `lock-denied` races
where a parallel RESTCONF session held the candidate datastore; the
engine retried and committed cleanly. The fix bundle does not
attempt to suppress those — they are correctly surfaced via
`ApplyFailed` events and the engine's reconcile-retry path absorbs
them.

## What this proves about #6(a) closure + candidate-only mode

The original #6(a) finding — NETCONF dial failed inside the cisco-vk
binary — was closed by commit `b2c1189`
(`fix(transport): port-default belongs in transport.For`). Once the
dial worked, the candidate-only path surfaced four follow-on bugs
that this evidence bundle confirms are now closed:

1. **Banner unknown-element** (commit `d27016d`): the writer's
   RESTCONF-shaped envelope was double-nested inside the path's
   last segment in NETCONF edit-config XML. Fixed via envelope
   unwrap in `pathToSubtreeFilterWithBody`.

2. **Perpetual drift on banner after write** (commit `88ac685`,
   #1 of bundle): NETCONF Fetch returned the path-prefixed wrapper
   instead of the requested resource at top level, so the writer's
   `unwrapYANGEnvelope` couldn't find its envelope key and
   `leavesEqual` reported drift even when the device matched.
   Fixed via `peelToLastPathSegment`.

3. **Same-namespace prefix bloat** (`88ac685` #2 + #3): the
   xml→json converter prefixed every element with its module,
   producing observed keys like `Cisco-IOS-XE-native:motd` while
   the desired had bare `motd`. RFC 7951 same-namespace
   inheritance is now honored, and `unwrapYANGEnvelope` accepts
   both the qualified and local-only forms so the same writer
   code drives both transports.

4. **Loopback unknown-element with =9997 syntax** (`88ac685` #4):
   NETCONF subtree XML builder treated RESTCONF's `<elem>=<value>`
   list-key syntax as a literal element name. Now uses the writer's
   structured `PathSpec` to emit `<Loopback><name>9997</name>...`
   — the YANG-list-key shape NETCONF requires.

The combined effect: NETCONF candidate-only is now a viable
production deployment construct alongside RESTCONF, satisfying the
user's directive to keep aggregator-mode topology as a
corner-case-only pattern. Per-pod kubelet over NETCONF
candidate-datastore is fully exercised by tests 06 and 07.

## Files in this bundle

| File | Contents |
|---|---|
| `metrics-snapshot.txt` | `/metrics` scrape filtered to the engine counters relevant to this evidence |
| `test-06-status.yaml` | Final IOSXEConfig CR for test 06 (phase=InSync) |
| `test-06-events.txt` | Event timeline including initial drift, intermittent lock-denied, then InSync |
| `test-07-status.yaml` | Final IOSXEConfig CR for test 07 (phase=InSync) |
| `test-07-events.txt` | Event timeline including initial unknown-element on Loopback=9997 (pre-fix), then SaveStartupOK |

## Out of scope here

Test 11 (confirmed-commit fallback) was deferred during this
session. The installed CRD on the kind-kind cluster predates the
Wave-10 chart bump, so the test manifest's `spec.confirmTimeoutSeconds`
gets `unknown field` from the API server. The chart-side CRD
already declares the field — the missing step is an operator-
scheduled `kubectl apply -f
charts/cisco-virtual-kubelet/crds/config.cisco.vk_iosxeconfigs.yaml`.
That schema bump is unrelated to NETCONF candidate-only mode and
intentionally not performed inside this evidence-capture session.
