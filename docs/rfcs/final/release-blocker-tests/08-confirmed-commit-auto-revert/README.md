# Test 08 — confirmed-commit auto-revert (RFC 6241 §8.4)

**§6.D.ii row added per [`../../../wave10-confirmed-commit-and-atomic-replace.md`](../../../wave10-confirmed-commit-and-atomic-replace.md) §3.3:**
"Headline live test that proves auto-revert works end-to-end. Deliberately submit a config change that breaks the controller's session; auto-revert at timeout."
**Closing waves:** 10.1 (transport `ConfirmedCommitter`), 10.2 (engine state machine + `spec.confirmTimeoutSeconds`).

## What this test proves

The most operationally-meaningful test of the whole Wave-10 design. Without it, the auto-revert path is theoretical.

The test deliberately submits a config change that breaks the controller's session — typically an IPv4 ACL on the management interface that drops the controller's source IP. Confirmed-commit fires (`<commit><confirmed/><confirm-timeout>30</confirm-timeout></commit>`); the controller cannot reach the device to confirm; after 30s the device's own timer reverts running back to its pre-commit state. The test verifies four things:

1. **During the timeout window** (operator-side, console-attached): `show running-config interface <mgmt>` shows the broken ACL applied. The change DID land tentatively.
2. **After the timeout window** (controller-side or operator-side console): the broken ACL is gone — running matches pre-test exactly. The auto-revert worked.
3. **`cisco_vk_config_transactions_total{outcome="auto_reverted"}` counter increments by 1.**
4. **The CR's `status.phase` reports Failed** with an error message containing "auto-revert".

## Device surface used

**The management interface** — the entire point is to exercise the connectivity-loss recovery. The default test fixture targets `GigabitEthernet0/0` (the typical management port on a Cat9K-24UX) and applies an extended ACL that denies the controller's source IP from the lab cluster. Operators MUST verify the management interface choice and the ACL contents before applying — substituting environment-specific addresses where needed.

**This is the most invasive test in the runbook.** Run it only when:

- An out-of-band console is attached and verified working.
- A documented manual rollback procedure is at hand for the case where auto-revert fails to fire (e.g. the device's own timer mechanism is broken — extremely rare but possible on stripped-down NETCONF servers).
- Maintenance window spans long enough that a 30-second outage of the management interface is operationally acceptable.

## Pre-state

```sh
bash ./pre-state.sh > pre-state.txt
```

Captures the current ACL list, the management interface's `ip access-group` binding (if any), and a baseline of the controller's RESTCONF reachability. The post-test verify diffs against this for the "auto-revert restored baseline" assertion.

## Apply

```sh
kubectl apply -f 00-apply.yaml
```

`00-apply.yaml` creates an `IOSXEConfig` with:

- `spec.transactional: true`
- `spec.confirmTimeoutSeconds: 30`
- An ACL definition that includes a deny rule against the controller's source IP plus its binding on the management interface.

The expected sequence on the device side:

1. `<lock>` candidate datastore.
2. `<edit-config>` populates candidate with the new ACL + interface binding.
3. `<commit><confirmed/><confirm-timeout>30</confirm-timeout></commit>` applies tentatively to running. **At this point the management session drops** because the new ACL filters the controller out.
4. The engine's `runningVerify` cannot Fetch (session is dead) → returns false → engine declines `ConfirmCommit`.
5. After 30s, the device's own timer reverts running. Controller can now reach the device again.

## Expected

See [`expected.md`](./expected.md). Summary:

- `status.phase == Failed` with Err containing "auto-revert".
- `cisco_vk_config_transactions_total{outcome="auto_reverted"}` increments by 1.
- `cisco_vk_config_transactions_total{outcome="confirmed"}` does NOT increment.
- Post-test running-config matches pre-state exactly (auto-revert restored baseline).

## Verify

```sh
bash ./verify.sh
```

`verify.sh` waits up to 60 seconds for the auto-revert to complete, then asserts the four conditions above.

## Rollback

If auto-revert succeeded, no manual rollback is needed — the device is already at pre-test state. If auto-revert *failed* (the only way for this test to fail, by design), the operator's manual rollback procedure (out-of-band console) is required.

```sh
bash ./rollback.sh
```

The script just deletes the test CR and leaves a note for the operator to confirm device state via console.
