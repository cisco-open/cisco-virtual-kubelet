# Test 03 — `configPrereqs` deletion-driven cleanup

**§6.D.ii row:** "`configPrereqs` deletion-driven cleanup → device clean of any prereq state created by the controller."
**Closing waves:** 4A-fu + 7A.2 + 7A.4.

## What this test proves

When a `CiscoDevice` carries `spec.configPrereqs`, the `CiscoDeviceReconciler` materialises a synthetic `IOSXEConfig` from those prereqs that day-0 reconciles before any apphosting Pod lands on the synthetic node. When the `CiscoDevice` is deleted, the deletion-finalizer path must:

1. **Wave 4A-fu**: keep the family list intact (do NOT clear `ManagedFamilies`); empty `source.inline` instead. The engine then runs each family with empty desired + per-family PruneCapable.PruneDiff to remove the device-side state.
2. **Wave 7A.2**: gate teardown step on `Status.ObservedGeneration == Generation` AND `Status.Phase == InSync` so a stale prior-generation status cannot trigger premature CR deletion.
3. **Wave 7A.4**: set `pruneOnRelinquish=true` ONLY on the teardown step's empty-source CR — the steady-state day-0 reconcile sets it `false` so additive day-0 doesn't accidentally prune unrelated entries.

This is the most invasive test: it exercises the actual deletion finalizer end-to-end, including device-side state removal. **Run it last** in the runbook order.

## Device surface used

The test uses a **separate `CiscoDevice`** (`test-03-prereq-device`) that targets the same physical Cat9K (10.1.1.1) but with a different K8s name and namespace, so the existing `cat9k-smoke` device is untouched. The test's prereqs add:

- VLAN 999 (named `cisco-vk-test-03`)
- Loopback 9998 (description `cisco-vk release-blocker test 03`, IP `10.255.255.98/32`)

These are the device-side artefacts the test will create and then delete to prove the cleanup path.

**The test does NOT spawn a per-device `cisco-vk` pod for `test-03-prereq-device`** because the apphosting side is orthogonal here. The synthetic `IOSXEConfig` reconciles via the existing `cat9k-smoke-vk` pod (it targets the same physical device address). This avoids needing a second pod that would otherwise need its own credentials Secret.

## Pre-state

```sh
bash ./pre-state.sh > pre-state.txt
```

Confirms VLAN 999 and Loopback 9998 are NOT present pre-test. If either exists, abort.

## Apply

```sh
# Apply the synthetic CiscoDevice with configPrereqs.
kubectl apply -f 00-apply-device.yaml

# Watch for the synthetic IOSXEConfig to reach InSync.
kubectl get iosxeconfig -n cisco-vk-smoke -w
# Stop with Ctrl+C once status.phase=InSync for the synthetic CR.
```

The CiscoDeviceReconciler creates a child `IOSXEConfig` named `<device-name>-prereqs` (or similar — check the controller's actual naming) with `pruneOnRelinquish=false`, applies the prereqs (VLAN 999 + Loopback 9998), and reaches `InSync`.

After confirming `InSync`, capture the mid-test state:

```sh
bash ./pre-state.sh > mid-state.txt
diff pre-state.txt mid-state.txt
```

The diff should show VLAN 999 and Loopback 9998 newly present.

## Trigger cleanup

```sh
kubectl delete -f 00-apply-device.yaml
```

This kicks the deletion finalizer. The controller flips the synthetic IOSXEConfig to `pruneOnRelinquish=true` with empty source, which drives the engine's prune path. The CiscoDeviceReconciler waits for the engine's reconcile to converge (via the `ObservedGeneration + Phase=InSync` gate from Wave 7A.2) before letting the finalizer complete and the `CiscoDevice` actually delete.

```sh
# Watch the deletion progress.
kubectl get ciscodevice test-03-prereq-device -n cisco-vk-smoke -w
# Eventually exits when the resource is gone.
```

## Expected

See [`expected.md`](./expected.md). Summary:

- During apply: VLAN 999 + Loopback 9998 created on device; synthetic IOSXEConfig `phase=InSync`.
- During delete: synthetic IOSXEConfig flips `pruneOnRelinquish=true`, ManagedFamilies stays intact, source goes empty, engine runs PruneDiff for both families, device-side artefacts removed.
- After delete: VLAN 999 and Loopback 9998 absent from device, no other state changed, `CiscoDevice` and synthetic `IOSXEConfig` resources both gone.

## Verify

```sh
bash ./verify.sh
```

`verify.sh` confirms:
- VLAN 999 absent.
- Loopback 9998 absent.
- No other VLAN or Loopback was added/removed (compare against pre-state.txt).
- No stuck `cisco.vk/configprereqs-cleanup` finalizer left behind.

## Rollback

If the test passes, **no rollback is needed** — the cleanup IS the final state. Run `pre-state.sh` once more and diff against `pre-state.txt`; should be empty.

If the test FAILS partway (e.g. the deletion finalizer hangs, or a family's PruneDiff regresses), the device may be left with VLAN 999 or Loopback 9998 still present. Run:

```sh
bash ./rollback.sh
```

`rollback.sh` issues manual RESTCONF DELETEs for both artefacts as the explicit fallback.
