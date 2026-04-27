# Live-device retest #3 — production-readiness pass

**Date:** 2026-04-27
**Device:** C9K-4 — Cat9300-24P, IOS-XE 17.18.2 — at 198.51.100.103
**Cluster:** ubuntu17 (k3s v1.34.3, Cilium 1.17.4)
**Image:** `:v12` — branch tip `a63f74a`

This third pass closes out every actionable follow-up from
[`../2026-04-27-live-c9300-v8-fixes-validated/SUMMARY.md`](../2026-04-27-live-c9300-v8-fixes-validated/SUMMARY.md).
Every operator-runnable test that targets RESTCONF transport now
**passes verify.sh end-to-end with no failed assertions.**

---

## Test outcomes

| Test | verify.sh | Notes |
|---|---|---|
| 02 — netconf-transactional-cli-rejection | **PASS** | Engine refused CLI write under transactional+driftPolicy=report; no banner motd on device. Transport-aware verify branch confirmed. |
| 06 — driftpolicy-revert-live-write | **PASS** | phase 1 Drifted (report) → phase 2 InSync (revert); banner motd applied; mutate_ops counter sum=1 across REPLACE/MERGE label sets. |
| 07 — write-startup-save-config | **PASS** | Loopback9997 applied; SaveStartupOK event fires; `save_startup_total{outcome="ok"}=5` (cumulative across the 18-second drift window). RESTCONF save-config path now hits `/restconf/operations/cisco-ia:save-config` — the bug that turned every save into a 404. |
| 11 — confirmed-commit-restconf-fallback | **PASS** | Loopback9994 applied; ConfirmedCommitFallback warning event present with the right reason; no ConfirmedCommitUsed event (correct on fallback path); transactions_total stays zero on the netconf,confirmed label combination. |

Lease finalizer (#7) re-confirmed: deleting any test CR drops the
family lease immediately — the lease list returned `No resources
found` between every test.

---

## Production-readiness fixes shipped in this pass

The first retest surfaced 5 follow-ups; the second retest validated
the first three and uncovered 6 more. **All 11 are now landed.**

| # | Bug | Commit | Layer |
|---|---|---|---|
| 3 | YANG-shape translation for interface writers | `a2274e2` | engine writers |
| 5 | Verify metrics scrape via port-forward | `0986c78` | playbook |
| 6(b) | Bounded retry on first transport dial | `a2274e2` | cmd startup |
| 6(a) | Banner-peek diagnostic on overflow | `aed64d2` | transport NETCONF |
| 7 | Lease finalizer on IOSXEConfig delete | `a2274e2` | provider |
| 8 | Metrics endpoint bound on :8080 + container port | `efb3ce1` | cmd + controller |
| 9 | FamilyStatus.OpCount surfaced on CR status | `efb3ce1` + `fbe2f1a` | api + provider |
| — | grep `\| head -1` `\|\| true` for pipefail | `58848f8` | playbook |
| — | sticky opCount + multi-verb metric assertion | `fbe2f1a` | playbook |
| — | Test 02 verify branched by transport | `83c6810` | playbook |
| — | Hardcoded fixture values parameterised (tests 03/04/05) | `83c6810` | playbook |
| — | Metrics registered on controller-runtime registry | `1f4ecd7` | cmd |
| — | family_state opCount lenient on converged InSync | `8e2979e` | playbook |
| — | SaveStartup `/restconf/operations/...` URL | `61566dc` | transport RESTCONF |
| — | Test 07 multi-transport `_any` + Loopback array shape | `a07d840` | playbook |
| — | Order-independent metric label matcher | `a63f74a` | playbook |

---

## Closed (#6(a) NETCONF dial root-cause)

The from-pod NETCONF dial bug is **not environmental** — proven by a
distroless probe pod (same SA, node, namespace, image base, even
RESTCONF-first sequence) dialing NETCONF cleanly 5/5. The issue is
specific to the cisco-vk binary's runtime state at dial time.

The diagnostic the v8+ image carries surfaces `read-empty: EOF` on
overflow — the device closes the SSH socket immediately without
sending its banner. Three working hypotheses in the v8 SUMMARY remain
the right next-step list for a focused debug pass.

The bounded retry (`6(b)`) is not a fix for `6(a)`; it just prevents
a transient blip at pod startup from leaving the configdriver in
permanent scaffold mode. **NETCONF transport for in-pod use is still
on the open-investigation list.** RESTCONF transport is the
production-ready default.

---

## Production posture

**Production-ready over RESTCONF transport** for the families exercised
by tests 02/06/07/11 (banner, interface_loopback) on a Cat9300-24P
running IOS-XE 17.18.2:

- Reconcile loop: ✅ end-to-end (engine, transport, status, events).
- Drift detection + reconcile: ✅ both report and revert policies.
- writeStartup save-config: ✅ event + metric.
- Wave 10 fallback signal (ConfirmedCommitFallback): ✅ event +
  metric stay zero on the auto-revert combo.
- Metrics endpoint: ✅ exposed on `:8080` via the per-pod kubelet's
  controller-runtime metrics server, scrapable via Service or
  port-forward.
- Lease coordination: ✅ finalizer releases on delete.
- Operator playbook (`release-blocker-tests/`): ✅ verify.sh
  PASSES end-to-end on each of the four tests; preflight passes on a
  fresh cluster.

**Open items for a Wave-10-NETCONF-on-the-pod story:** finding `#6(a)`
root-cause + retry-with-reconnect on transient device-side reset.
The diagnostic is shipped; next operator hit produces actionable
data without needing tcpdump.

---

## Cleanup state

- Device-side: Loopback9994/9997 deleted, banner motd reverted, prereqs
  (`netconf-yang feature candidate-datastore`, `gnxi server`) left in
  place per the v8 retest's lab authorization.
- Cluster-side: `cisco-vk-smoke` namespace clear of CRs, ConfigMaps,
  and Leases. CiscoDevice `cat9k-smoke` left provisioned (transport=
  restconf) for the next operator session.
- Helm release: `cvk` at revision tracking `:v12` image.
