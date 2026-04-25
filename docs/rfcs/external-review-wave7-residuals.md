# External review - Wave 7 residuals

**Branch:** `pr/johalley/ciscoconfig_xe`
**Date:** 2026-04-25
**Reviewer:** Codex
**Scope:** post-Wave-7 review of the fixes described in `docs/rfcs/latest-update.md`

This note captures the two remaining issues found after reviewing the Wave 7 code updates. The reviewed fixes for transactional CLI rejection, configPrereqs teardown generation gating, PathSpec on handwritten interface writers, credential Secret rotation, aggregator/per-pod exclusivity, and Helm RBAC are materially improved and covered by tests. The two gaps below are both in the lease arbitration path and should be actioned before re-claiming full day-2 readiness.

Verification run during review:

- `go vet ./...`
- `helm lint charts/cisco-virtual-kubelet`
- `helm template cvk charts/cisco-virtual-kubelet --set aggregator.enabled=true --set config.leaseNamespace=cvk-system --namespace cvk-system`
- `GOCACHE=/tmp/cvk-gocache go test -race -count=5 ./...`
- `GOCACHE=/tmp/cvk-gocache go test -race -count=20` across hot packages: `internal/drivers`, IOS-XE writers/transport/engine, `internal/aggregator`, `internal/controller`, and `internal/provider`

All verification commands completed cleanly. The first test attempt without elevated loopback permissions failed only because the desktop sandbox blocks `httptest` from binding local ports.

## Finding 1 - P1 - Lease names are invalid for underscore families

**File:** `internal/drivers/iosxe/configdriver/engine/lease.go`
**Lines:** `210-212`

`FamilyLeaser` builds the Kubernetes Lease name from the raw `(device, family)` pair:

```go
func leaseName(device, family string) string {
	return fmt.Sprintf("cvk-%s-%s", device, family)
}
```

That is valid for `vlan`, which is what the current lease tests use, but it is not valid for many shipped family names such as `interface_ethernet`, `access_list_extended`, `interface_switchport`, `ip_name_server`, and other underscore-bearing families. Kubernetes object names must be DNS-label-like and cannot contain `_`. The fake client does not perform apiserver name validation, so the current unit tests stay green while a real apiserver would reject Lease creation.

**Operational impact:** IOSXEConfig reconciles for underscore families can fail lease acquisition in production. `acquireLeases` treats lease backend errors as not-owned, drops the family from the resolved intent, and records a skip/conflict instead of applying the family. This affects a large portion of the advertised family set, including core day-0/day-2 interface and ACL use cases.

**Recommended action:**

- Replace the raw family segment in the Lease name with a DNS-safe value.
- Prefer a deterministic, human-readable prefix plus hash, for example `cvk-<safe-device>-<safe-family>-<short-hash>`, so long names and unusual characters remain safe.
- Apply the same safety to the device segment as defense in depth.
- Add a validation-backed test using at least `interface_ethernet` or `access_list_extended`. An envtest is ideal; a lightweight helper test using Kubernetes DNS label validation is acceptable as a near-term guard.
- Keep labels such as `cisco.vk/device` and `cisco.vk/family` with the original values for operator visibility.

## Finding 2 - P2 - Lease conflicts still flow through the engine as success/failure

**File:** `internal/provider/config_reconciler.go`
**Lines:** `400-409`

After `acquireLeases` filters out families this reconciler does not own, `reconcileOne` always calls the engine:

```go
leasedIntent, leaseConflicts := r.acquireLeases(ctx, resolved, cr)
result := eng.Reconcile(ctx, leasedIntent)
for family, holder := range leaseConflicts {
	result.FamilyStatuses = append(result.FamilyStatuses, engine.FamilyStatus{
		Name:    family,
		State:   "Skipped",
		Message: fmt.Sprintf("family leased by %q", holder),
	})
}
return r.recordResult(ctx, cr, result, h, conflicts, resolved)
```

This makes lease contention look like an engine outcome rather than a first-class arbitration state.

There are two problematic cases:

1. **All families skipped:** the filtered intent has `ManagedFamilies=[]`, so `Engine.validate` fails with `ManagedFamilies is empty`. `recordResult` then writes `Phase=Failed` and bumps `LastDeviceCheck`, even though no device fetch, diff, or apply happened.
2. **Some families skipped:** the engine may return `Phase=InSync` for the subset it did own, then `recordResult` can report the whole CR as Ready even though one or more requested families were not reconciled.

The runtime-suffixed lease identity fix makes this path more common during normal rollouts: the new pod or worker may briefly lose leases to the old instance. That is correct arbitration, but it should not produce misleading Failed or Ready status.

**Operational impact:** during credential rotation, image rollout, manager restart, or any overlap between old and new reconcilers, a losing reconciler can publish false status. In the all-skipped case it can also update status timestamps even though the device was not checked, which may interact badly with drift short-circuit logic and trigger noisy retries.

**Recommended action:**

- Treat lease conflicts as a first-class reconcile result before entering the engine.
- If all requested families are skipped, do not call `Engine.Reconcile`.
- Return a non-ready, non-failed transient phase or reason such as `LeaseBlocked`/`WaitingForLease`, with per-family `Skipped` statuses.
- If only some families are skipped, prevent `Phase=InSync` for the whole CR. Use `Drifted`, `Pending`, or a dedicated lease-blocked phase/reason so operators know the CR was not fully reconciled.
- Do not bump `LastDeviceCheck` unless the device was actually fetched/diffed/applied.
- Consider a shorter requeue while lease-blocked than the normal drift interval, ideally bounded by lease TTL.
- Add tests for both all-skipped and partially-skipped cases. Use an underscore family in at least one test after fixing lease-name safety.

## Status recommendation

Do not yet treat `latest-update.md`'s "shippable for day-0 AND day-2" claim as fully revalidated. Most Wave 7 closures are sound, but lease arbitration is still not production-hardened:

- P1 blocks real-apiserver operation for many shipped families.
- P2 can mislead operators during expected rollout overlap windows.

Once these two are fixed and covered with validation-aware tests, the Wave 7 readiness claim can be re-reviewed with a narrower focus on lease behavior.
