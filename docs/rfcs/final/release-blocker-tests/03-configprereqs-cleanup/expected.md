# Test 03 — Expected outcome

## Apply phase

After `kubectl apply -f 00-apply-device.yaml`:

1. `CiscoDeviceReconciler` observes the new `CiscoDevice` and creates a synthetic `IOSXEConfig` (named per the controller's convention — likely `test-03-prereq-device-prereqs` or similar, in the same namespace).
2. The synthetic CR has `pruneOnRelinquish=false` (additive day-0).
3. Engine reconciles: VLAN 999 created, Loopback 9998 created.
4. Synthetic CR reaches `status.phase=InSync`.

Mid-test device state:
- VLAN 999 named `cisco-vk-test-03` exists.
- Loopback 9998 with description and IP exists.
- All other device state identical to pre-test.

## Delete phase

After `kubectl delete -f 00-apply-device.yaml`:

1. `CiscoDeviceReconciler.deletionFinalizer` runs.
2. Wave 4A-fu: synthetic IOSXEConfig is patched — `source.inline` cleared (or set to a minimal envelope), `ManagedFamilies` stays `[vlan, interface_loopback]` so the engine still knows which families it's responsible for.
3. Wave 7A.4: `pruneOnRelinquish` flipped to `true` *only on this teardown step*.
4. Wave 7A.2: the controller waits until the synthetic CR reaches `Status.ObservedGeneration == Generation` AND `Status.Phase == InSync` before completing the finalizer. This prevents premature deletion when the engine hasn't yet observed the teardown spec change.
5. Engine runs PruneCapable.PruneDiff for vlan and interface_loopback against an empty intent. Result: ops to delete VLAN 999 and Loopback 9998.
6. Device-side artefacts removed.
7. Synthetic CR's reconcile concludes; finalizer completes; `CiscoDevice` resource deletes.

Post-delete state:
- VLAN 999 absent from device.
- Loopback 9998 absent from device.
- All other VLAN/Loopback entries unchanged from pre-test.
- `CiscoDevice test-03-prereq-device` gone.
- Synthetic IOSXEConfig gone.
- No `cisco.vk/configprereqs-cleanup` (or similar) finalizer stuck on any resource.

## What would fail

| Failure | Likely cause |
|---|---|
| VLAN 999 or Loopback 9998 remains after delete | PruneDiff regression (Wave 4A-fu) — the engine ran with `ManagedFamilies` cleared and so didn't know to prune. |
| `CiscoDevice` resource stuck in `Terminating` for >5 min | Finalizer is waiting on a stale `Status.Phase` or `ObservedGeneration` (Wave 7A.2 regression). |
| Other VLANs or Loopbacks (not 999 / 9998) deleted | Steady-state `pruneOnRelinquish` got accidentally set to `true` (Wave 7A.4 regression). |
| Synthetic IOSXEConfig left behind after `CiscoDevice` is gone | Owner-reference garbage collection broken — the synthetic CR was likely missing its `ownerReferences` to the `CiscoDevice`. |

## Counters

- `cisco_vk_engine_apply_ops_total{family="vlan"}` increments by 1 during apply, by 1 again during delete (the prune op).
- `cisco_vk_engine_apply_ops_total{family="interface_loopback"}` likewise.
- `cisco_vk_finalizer_runs_total{outcome="success"}` increments by 1.
