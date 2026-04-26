# Test 09 — atomic replace, cross-family removal

**§6.D.ii row added per [`../../../wave10-confirmed-commit-and-atomic-replace.md`](../../../wave10-confirmed-commit-and-atomic-replace.md) §3.3:**
"Partial-drift scenario; remove a VLAN and a VRF in one transaction; verify neither leaves an intermediate state."
**Closing waves:** 10.3 (engine atomic-replace + cross-family ordering).

## What this test proves

`spec.atomicReplace: true` on a transactional reconcile must:

1. Treat the resolved intent as the AUTHORITATIVE state for the CR's managed families. Device-side entries not in the intent are deleted.
2. Order the per-family ops by `depends_on`. Removal-of-bound-by-removal must not leave a half-deleted intermediate state.

The test runs in two passes:

- **Pass 1 (apply):** create a VRF, a VLAN, and an `interface_loopback` that binds to the VRF. All three families' device-side state matches the intent.
- **Pass 2 (atomic-replace cleanup):** update the CR's source to a minimal intent (no VRF, no VLAN, no Loopback). With `atomicReplace: true`, the engine should:
  - Remove the loopback's VRF binding first (interface depends_on vrf — interface comes first in the topo-sort for the *parent* direction; for *removal* direction, the engine reverses).
  - Remove the VRF.
  - Remove the VLAN.
  - All three in one transaction (one `<lock>` + multiple `<edit-config>` + one `<commit>` + one `<unlock>`).

The point is: **at no intermediate point should the device have a Loopback referencing a deleted VRF.** That is the partial-drift the test guards against.

## Device surface used

Three new device-side entries:

- VLAN 998 named `cisco-vk-test-09`
- VRF `TEST-09-VRF` (RD `65009:1`)
- Loopback 9996 with description, `vrf forwarding TEST-09-VRF`, IP `10.255.255.96/32`

All three are well outside any plausible production range. Operator should still verify they are unused before applying.

## Pre-state

```sh
bash ./pre-state.sh > pre-state.txt
```

Asserts none of the three test entities exist before the test starts.

## Apply (two-phase)

Phase 1 — establish state:

```sh
kubectl apply -f 00-apply-establish.yaml
# wait for status.phase=InSync
kubectl get iosxeconfig test-09-atomic-replace -n cisco-vk-smoke -w
```

Phase 2 — atomic remove:

```sh
kubectl apply -f 01-apply-empty-with-atomic.yaml
# wait for status.phase=InSync (apply succeeded; intent now empty for these families)
```

The second YAML is the same CR with `atomicReplace: true` AND a source ConfigMap that declares the families' intent as empty. The engine must drive the deletion ops in the correct order.

## Expected

See [`expected.md`](./expected.md). Summary:

- After phase 1: VLAN 998, VRF TEST-09-VRF, Loopback 9996 all present.
- After phase 2: all three absent. Engine emitted the deletes in dependency order. Status phase=InSync.
- Device transition was atomic — no intermediate state where Loopback 9996 referenced a deleted VRF.

## Verify

```sh
bash ./verify.sh
```

Asserts post-phase-2 device state matches pre-state.

## Rollback

If phase 2 succeeded, rollback is the same as phase 2 — atomic removal already happened. If phase 2 failed midway, the engine's deferred Discard runs and the candidate is rolled back, leaving phase-1 state on the device. `rollback.sh` then cleans phase-1 state.

```sh
bash ./rollback.sh
```
