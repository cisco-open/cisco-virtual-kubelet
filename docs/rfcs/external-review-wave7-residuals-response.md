# Response to Wave 7 residuals review

**Branch:** `pr/johalley/ciscoconfig_xe`
**Subject of response:** [`external-review-wave7-residuals.md`](external-review-wave7-residuals.md) (Codex, post-Wave-7)
**Author:** Josh Halley
**Status:** plan, not implementation. Each finding has a triage verdict, a remediation approach, and an acceptance criterion.

This is the fourth response RFC in the series:

1. [`external-review-response.md`](external-review-response.md) — closed Waves 1–5 against the original review.
2. [`external-review-followup-response.md`](external-review-followup-response.md) — closed Waves 1A-fu through 6B.
3. [`external-review-next-actions-response.md`](external-review-next-actions-response.md) — closed Waves 7A.1 through 7B.
4. **This document** — closes Wave 8.1 and 8.2 against the Wave 7 residuals review.

---

## 1. Bottom-line response

The Wave 7 residuals review is **accurate**. Both findings live in the lease arbitration path and verify at the cited file:line:

- **Finding 1 (P1)** — `internal/drivers/iosxe/configdriver/engine/lease.go:210-212`. `leaseName(device, family)` produces names like `cvk-edge-01-interface_ethernet`. Kubernetes object names require DNS-1123 subdomain compliance: `[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*`. Underscores are not allowed. `fake.Client` skips this validation, so every existing lease test passes while a real apiserver rejects every lease for any family with `_` in its name. That's most IOS-XE families: `interface_ethernet`, `interface_switchport`, `interface_loopback`, `interface_virtual_port_group`, `interface_port_channel`, `interface_tunnel`, `interface_vlan`, `access_list_extended`, `access_list_standard`, `ip_name_server`, `ip_nat_inside_source`, `ip_nat_pool`, `ipv6_access_list_extended`, `ipv6_access_list_standard`, `ipv6_prefix_list`, `ip_as_path_access_list`, `ip_community_list`, `ip_domain`, `ip_http`, `ip_ssh`, `crypto_ikev2_profile`, `crypto_ipsec_profile`, `crypto_ipsec_transform_set`, `crypto_map`, `crypto_pki_trustpoint`, `static_route`, `route_map`, `prefix_list`, `policy_map`, `class_map`, `radius_server`, `tacacs_server`, `snmp_server`, `event_manager`, `spanning_tree`. The end-to-end consequence is that lease-backend errors on every reconcile for any of these families, `acquireLeases` drops them to "skipped", and the engine never applies them.

- **Finding 2 (P2)** — `internal/provider/config_reconciler.go:400-409`. `reconcileOne` always calls `eng.Reconcile(ctx, leasedIntent)`. When all families are lease-blocked, `leasedIntent.ManagedFamilies` is empty; `engine.validate()` returns `errors.New("ManagedFamilies is empty")`; `recordResult` writes `Phase=Failed` and bumps `LastDeviceCheck`. The CR's status reads "Failed" but no device-side work happened. Wave 7A.3 (runtime-suffixed identity) made the lease-blocked path normal during rollouts — the new reconciler routinely loses contention to the old one for a brief window — so this misleading-status path is now hit on every rollout, not just rare events.

Both findings sit on top of Wave 7A.3's correct lease arbitration. The first hides every production lease attempt for underscore families behind a write error; the second turns the (correct, expected) post-Wave-7A.3 contention window into a status liar.

The reviewer's status recommendation — *"Do not yet treat `latest-update.md`'s 'shippable for day-0 AND day-2' claim as fully revalidated"* — is correct. [`implementation-status.md`](implementation-status.md) §1's day-2 claim must walk back again.

---

## 2. Per-finding triage

| # | Severity | Title | Status |
|---|---|---|---|
| W7R-1 | P1 | Lease names invalid for underscore families | confirmed |
| W7R-2 | P2 | Lease conflicts surface as engine success/failure rather than a first-class arbitration state | confirmed |

Nothing is contested.

---

## 3. Remediation plan

Two waves. Wave 8.1 unblocks every shipped IOS-XE family for production lease usage (the "real apiserver rejects" problem); Wave 8.2 turns the contention window into honest status. Both close before re-claiming day-2 readiness.

### Wave 0-w7r — status walk-back (immediate, ~0.25 ed)

[`implementation-status.md`](implementation-status.md) §1 currently re-claims day-2. Walk back to: "close to day-2 readiness, pending lease-arbitration hardening for DNS-safe lease names and first-class lease-blocked status." Re-claim only when both Waves below close with passing tests.

### Wave 8.1 — DNS-safe lease names (P1, ~1 ed)

**What.** `leaseName(device, family)` produces literal `cvk-<device>-<family>`. Underscore-bearing family names violate DNS-1123 subdomain rules; real apiserver rejects every such lease.

**Approach.**

```go
// sanitiseLeaseSegment maps an arbitrary identifier into the DNS-1123
// subdomain alphabet ([a-z0-9-]) used by Kubernetes object names.
// Replaces every disallowed byte with '-', lowercases, then folds
// repeated/leading/trailing '-' to keep the result valid.
func sanitiseLeaseSegment(s string) string {
    // ...
}

// leaseName composes a stable, DNS-1123-safe Lease name. The
// sanitisation may collapse two distinct inputs to the same output
// (e.g. `interface_ethernet` and `interface-ethernet` both fold to
// `interface-ethernet`), so a short content-hash suffix
// disambiguates while keeping the prefix human-readable.
func leaseName(device, family string) string {
    return fmt.Sprintf("cvk-%s-%s-%s",
        sanitiseLeaseSegment(device),
        sanitiseLeaseSegment(family),
        shortHash(device + "/" + family),
    )
}
```

The full original `device` and `family` strings stay in the lease's labels (`cisco.vk/device`, `cisco.vk/family`) for operator-visible filtering — `kubectl get leases -l cisco.vk/family=interface_ethernet` still works.

**Acceptance.**

- Unit test: `leaseName("edge-01", "interface_ethernet")` validates as DNS-1123 subdomain via `k8s.io/apimachinery/pkg/util/validation.IsDNS1123Subdomain`.
- Unit test: collision case — two distinct inputs that fold to the same sanitised prefix produce different lease names due to the hash suffix.
- Unit test (table): every family in the shipped registry produces a valid DNS-1123 subdomain when paired with a representative device name.
- Lease labels keep the original (un-sanitised) `device` and `family` values.

### Wave 8.2 — Lease conflicts as first-class arbitration state (P2, ~1.5 ed)

**What.** `reconcileOne` always calls `eng.Reconcile`, even when every family was lease-blocked. Empty `leasedIntent.ManagedFamilies` then trips `engine.validate()`'s "empty" check → `Phase=Failed`. Or when only some families are blocked, the engine returns `Phase=InSync` for the subset and the CR appears Ready even though work was missed. Plus `recordResult` bumps `LastDeviceCheck` regardless of whether the device was actually contacted.

**Approach.**

1. **Detect the all-blocked case before calling the engine.** Branch in `reconcileOne`: if every requested family is in `leaseConflicts` (i.e. `len(leasedIntent.ManagedFamilies) == 0` AND `len(leaseConflicts) > 0`), short-circuit with a synthetic `Result` carrying `Phase=PhaseLeaseBlocked` and a per-family `Skipped` status with the holder name. Skip the engine call.

2. **Detect the partially-blocked case after the engine returns.** If `len(leaseConflicts) > 0` AND `result.Phase == PhaseInSync`, downgrade the phase. Two options:
   - Map InSync-with-blocked-families to a new `PhasePartiallyBlocked` status and a `Reason=LeaseBlocked` on the Ready condition. Ready=False.
   - Reuse `PhaseDrifted` since the CR isn't fully reconciled. Less precise but doesn't introduce a new phase value.
   I recommend the new phase for honesty: `PhasePartiallyBlocked` is rare enough that operator status pages can render it explicitly.

3. **Don't bump `LastDeviceCheck` on lease-blocked ticks.** Add a `result.DeviceTouched bool` field; only set it when the engine actually called `Fetch`. `recordResult` updates `LastDeviceCheck` only when `result.DeviceTouched`. Lease-blocked all-families ticks have `DeviceTouched=false`.

4. **Shorter requeue when lease-blocked.** The lease TTL is 30s by default; requeue after `min(driftDetectInterval, leaseTTL/2) ≈ 15s` so the next tick has a fair chance to find the lease available. The controller-runtime path's `Result.RequeueAfter` already handles this; we just need to compute a shorter value when phase is LeaseBlocked.

5. **New phase string.** Add `PhaseLeaseBlocked = "LeaseBlocked"` and `PhasePartiallyBlocked = "PartiallyBlocked"` (or just LeaseBlocked for both — the per-family Skipped statuses already disambiguate). I'll start with `PhaseLeaseBlocked` only, applied in both cases; the per-family list distinguishes "all" vs "some".

**Acceptance.**

- Unit test: all-families-blocked → `Phase=LeaseBlocked`, no engine.Reconcile call (assertable via a counting mock engine), no `LastDeviceCheck` bump, per-family Skipped entries.
- Unit test: partial-block → engine runs for the owned subset, Phase ends up `LeaseBlocked` (not `InSync`), Ready condition is False with reason `LeaseBlocked`.
- Unit test: clean tick (no lease conflicts) → existing behavior unchanged, `LastDeviceCheck` bumped.
- Unit test: requeue interval is shorter under LeaseBlocked than under driftDetectInterval, but bounded above 1s.

---

## 4. Sequencing and effort

Total estimate: **~3 engineer-days**.

| Wave | Scope | Engineer-days | Severity |
|---|---|---|---|
| 0-w7r | Status walk-back | ~0.25 | required first |
| 8.1 | DNS-safe lease names | ~1 | P1 |
| 8.2 | Lease conflicts as first-class state | ~1.5 | P2 |

Recommended execution order:

1. **Wave 0-w7r walk-back** (immediate, single PR).
2. **Wave 8.1 first** — without this, every underscore-family lease is a lease backend error, and Wave 8.2's behaviour around lease errors becomes harder to reason about.
3. **Wave 8.2** — closes the misleading-status path now that lease names are valid.

---

## 5. What "shippable for day-2" means after THIS plan

Add two acceptance criteria to the existing list:

1. Lease names validate as DNS-1123 subdomain for every family in the shipped registry, against a real or validation-aware test (not just `fake.Client`).
2. Lease-blocked reconciles produce `Phase=LeaseBlocked` with per-family Skipped statuses, do not advance `LastDeviceCheck`, and requeue at a sub-TTL interval.

---

## 6. What this plan does NOT address

Out of scope:

- **Lease release on shutdown.** The reviewer's earlier suggestion (release held leases on graceful shutdown) is operationally nice but not a correctness gap. Defer until lease arbitration has been live-tested and any actual handover-time complaints surface.
- **`Deployment.spec.strategy.type=Recreate` for VK pods.** Operational belt-and-suspenders covered by Wave 7A.3's response RFC; can land later as a Helm-values change.
- **envtest infrastructure** (recurring follow-up). Wave 8.1's validation-aware test uses `IsDNS1123Subdomain` directly, which is the lighter form of envtest's name-validation check; an envtest is the durable closure.

---

## 7. Process notes

- This document is a plan; each Wave is a separate PR-shaped commit.
- The status walk-back lands first, before Wave 8.1.
- One architectural lesson to internalise (in commit messages and inline comments) so it doesn't recur:
  - **`fake.Client` does not enforce Kubernetes object-name validation.** When a name is composed from arbitrary strings, the test must explicitly validate the result against `k8s.io/apimachinery/pkg/util/validation`. Otherwise tests pass, the apiserver rejects, and the failure surfaces only in a live cluster.

The reviewer's bottom line — *"Once these two are fixed and covered with validation-aware tests, the Wave 7 readiness claim can be re-reviewed with a narrower focus on lease behavior"* — is the operating principle of this plan.
