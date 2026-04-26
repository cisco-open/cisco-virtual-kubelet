# Test 13 — atomic replace + confirmed-commit combined (Wave 10 variation)

**§6.D.ii row added per Wave-10 variation matrix:**
"Both Wave 10 safety nets engaged on the same CR — atomic replace AND confirmed-commit. Proves the two features compose."
**Closing waves:** 10.1 + 10.2 + 10.3.

## What this test proves

This is the test that justifies the user's framing of Wave 10 as a *combined* primitive: "deploying any as-code solution without the ability to do commit confirmed is high risk." Atomic replace alone leaves the loss-of-management risk; confirmed-commit alone leaves the partial-drift risk. **Together** they are the recommended default for any operator running risk-sensitive families (BGP, ACL, mgmt-plane, VRF) under config-as-code.

The test exercises the same two-phase pattern as test 09 (establish state → atomic remove) but with `confirmTimeoutSeconds=30` set on both phases. The expected behaviour:

1. **Phase 1 (establish):** atomic-replace adds VLAN 997, VRF TEST-13-VRF, Loopback 9993. The engine takes the auto-revert path on the apply: CommitConfirmed → runningVerify (clean) → ConfirmCommit. `ConfirmedCommitUsed=true` event fires.
2. **Phase 2 (atomic remove):** same CR with empty source. Engine atomic-replaces the families' state to empty in one transaction, again with auto-revert protection. `ConfirmedCommitUsed=true` fires again.

Either phase fails → the device's auto-revert timer fires and reverts the change. Combined safety means a failed atomic-removal doesn't leave the device in a partial-drift state AND doesn't leave it in a broken state if the change disconnected the controller.

## Device surface used

VLAN 997, VRF TEST-13-VRF, Loopback 9993 — all chosen to be distinct from the other Wave-10 tests' surfaces (test 09 used 998/TEST-09/9996). All three reverted by the rollback step or by the auto-revert timer if anything fails.

## Pre-state

```sh
bash ./pre-state.sh > pre-state.txt
```

Asserts none of the three test entities exist before the test.

## Apply (two-phase)

```sh
# Phase 1: establish state with atomic replace + confirmed commit.
kubectl apply -f 00-apply-establish.yaml
kubectl get iosxeconfig test-13-combined -n cisco-vk-smoke -w
# Wait for status.phase=InSync.

# Phase 2: atomic-replace removal, again with confirmed commit.
kubectl apply -f 01-apply-empty.yaml
kubectl get iosxeconfig test-13-combined -n cisco-vk-smoke -w
# Wait for status.phase=InSync (or Failed if running-verify caught a regression).
```

## Expected

See [`expected.md`](./expected.md). Summary:

- After phase 1: VLAN 997, VRF TEST-13-VRF, Loopback 9993 all present. `ConfirmedCommitUsed` event fired. `outcome="confirmed"` counter incremented.
- After phase 2: all three absent. Second `ConfirmedCommitUsed` event fired. `outcome="confirmed"` counter incremented again. Cross-family ordering correct (no partial-drift on the device side).

## Verify

```sh
bash ./verify.sh
```

## Rollback

```sh
bash ./rollback.sh
```

If phase 2 succeeded, rollback IS the final state. If phase 2 failed (its job is to atomically remove all three; auto-revert restores phase-1 state if running-verify fails), the CR stays at phase-1 state and rollback runs defensive RESTCONF DELETEs.
