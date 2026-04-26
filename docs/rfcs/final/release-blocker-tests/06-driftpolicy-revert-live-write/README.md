# Test 06 — `driftPolicy: revert` live write of one family

**§6.D.ii row:** "`driftPolicy: revert` live write → flip a CR from `report` to `revert` for one family, watch the device-side change, flip back."
**Closing waves:** the original Phase-1 drift-detect machinery (the `revert` policy itself was always shipped); this test exercises it end-to-end against a real device.

## What this test proves

`driftPolicy: revert` is the production-default policy. When the device-side state for a managed family diverges from the resolved intent, the engine emits ops to bring the device back to the intent. This test verifies that:

1. The drift-detect interval bypass (Wave 1B) actually triggers the revert path, not just reports drift.
2. The revert is **scoped to the one family** named in the CR's `managedFamilies` — it does not touch any other family or any out-of-band configuration on the device.
3. The post-revert `status.phase` returns to `InSync`.
4. The applied ops on the device are reversible — `rollback.sh` returns the device to pre-test state.

This is the **most operator-visible** of the release-blocker tests: the device's running config will visibly change during the apply step. The change is constrained to the `system.banner` family (a free-form login banner string) so the operational impact is zero.

## Device surface used

**`system.banner` family — the login banner motd string only.** No interface, no routing, no VRF, no ACL. The pre-state captures the existing banner; the apply sets a test banner; the verify confirms the device-side banner is the test string; the rollback restores the pre-test banner.

If the device has no pre-existing banner, the rollback removes the field entirely (which is identical to the pre-state).

## Pre-state

```sh
bash ./pre-state.sh > pre-state.txt
```

Captures the current `banner motd` exactly. Save this; the rollback feeds it back in.

## Apply (two-phase)

Phase 1 — apply the CR with `driftPolicy: report`:

```sh
kubectl apply -f 00-apply-report.yaml
```

Wait until `status.phase` is `Drifted` (the engine sees that the CR's banner differs from the device's actual banner; reports it but does not write).

```sh
kubectl get iosxeconfig test-06-driftpolicy-revert -n cisco-vk-smoke -w
# Stop with Ctrl+C once phase=Drifted
```

Phase 2 — flip to `revert`:

```sh
kubectl apply -f 01-flip-to-revert.yaml
```

This patches `spec.driftPolicy` from `report` to `revert`. The next reconcile applies the diff to the device.

```sh
# Watch until phase returns to InSync.
kubectl get iosxeconfig test-06-driftpolicy-revert -n cisco-vk-smoke -w
```

## Expected

See [`expected.md`](./expected.md). Summary:

- Under `report`: `phase=Drifted`, `status.drift[]` contains an entry for `system/banner/motd`, device-side banner is unchanged.
- After flip to `revert`: `phase=InSync`, device-side banner equals the CR's intended value.

## Verify

```sh
bash ./verify.sh
```

`verify.sh` confirms:
- `phase == InSync`.
- Device-side `banner motd` text matches the test string.
- No other system-level field changed (hostname, domain, ip-http enable state, etc.).

## Rollback

```sh
bash ./rollback.sh
```

`rollback.sh` deletes the test CR (which alone does NOT revert the banner — `pruneOnRelinquish=false` by default). It then issues a RESTCONF write to set the banner back to the value captured in `pre-state.txt`. This is the explicit reverse of what the test wrote.

If `pre-state.txt` shows no banner, the rollback issues a RESTCONF DELETE on the banner subtree.
